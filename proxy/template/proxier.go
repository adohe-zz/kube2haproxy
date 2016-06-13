package template

import (
	"fmt"
	"io/ioutil"
	"reflect"
	"sync"

	"github.com/AdoHe/kube2haproxy/proxy"
	"github.com/AdoHe/kube2haproxy/util/abool"
	utilhaproxy "github.com/AdoHe/kube2haproxy/util/haproxy"
	"github.com/AdoHe/kube2haproxy/util/ipaddr"
	utilkeepalived "github.com/AdoHe/kube2haproxy/util/keepalived"
	"github.com/AdoHe/kube2haproxy/util/ratelimiter"
	"github.com/AdoHe/kube2haproxy/util/template"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/watch"

	"github.com/golang/glog"
)

// ProxierConfig is just a wrapper of kinds of config
// that proxier needs to work properly.
type ProxierConfig struct {
	Device           string
	KeepalivedConfig utilkeepalived.KeepalivedConfig
	HaproxyConfig    utilhaproxy.HaproxyConfig
}

// Proxier is a HAProxy based proxy implementations. It will generate
// HAProxy configuration file via template and manage the HAProxy with
// a reload script.
type Proxier struct {
	config ProxierConfig

	// the Proxier only reload when in MASTER state
	master *abool.AtomicBool
	// if last sync has not processed, we should skip commit
	skipCommit bool

	// lock is a mutex used to protect below fields
	lock       sync.Mutex
	vipTable   map[string]bool
	routeTable map[string]*proxy.ServiceUnit

	ip                 ipaddr.Interface
	haproxyInstance    *utilhaproxy.Haproxy
	keepalivedInstance *utilkeepalived.Keepalived

	// rateLimitedCommitFuncForKeepalived is a rate limited commit function
	// that coalesces and controls how often the Keepalived is reloaded.
	rateLimitedCommitFuncForKeepalived *ratelimiter.RateLimitedFunction
	// rateLimitedCommitFuncForKeepalived is a rate limited commit function
	// that coalesces and controls how often the HAProxy is reloaded.
	rateLimitedCommitFuncForHaproxy *ratelimiter.RateLimitedFunction
	// rateLimitedCommitStopChannel is the stop/terminate channel.
	rateLimitedCommitStopChannel chan struct{}
}

func NewProxier(cfg ProxierConfig) (*Proxier, error) {
	proxier := &Proxier{
		master:                             abool.New(),
		vipTable:                           make(map[string]bool),
		routeTable:                         make(map[string]*proxy.ServiceUnit),
		rateLimitedCommitFuncForHaproxy:    nil,
		rateLimitedCommitFuncForKeepalived: nil,
		rateLimitedCommitStopChannel:       make(chan struct{}),
	}

	ip, err := ipaddr.New(cfg.Device)
	if err != nil {
		return nil, err
	}
	proxier.ip = ip

	keepalived, err := utilkeepalived.NewInstance(cfg.KeepalivedConfig)
	if err != nil {
		return nil, err
	}
	proxier.keepalivedInstance = keepalived

	haproxy, err := utilhaproxy.NewInstance(cfg.HaproxyConfig)
	if err != nil {
		return nil, err
	}
	proxier.haproxyInstance = haproxy

	numSeconds := int(cfg.KeepalivedConfig.ReloadInterval.Seconds())
	proxier.enableKeepalivedRateLimiter(numSeconds, proxier.commitAndReloadKeepalived)
	numSeconds = int(cfg.HaproxyConfig.ReloadInterval.Seconds())
	proxier.enableHaproxyRateLimiter(numSeconds, proxier.commitAndReloadHaproxy)

	return proxier, nil
}

func (proxier *Proxier) enableKeepalivedRateLimiter(interval int, handlerFunc ratelimiter.HandlerFunc) {
	keyFunc := func(_ interface{}) (string, error) {
		return "proxier_keepalived", nil
	}

	proxier.rateLimitedCommitFuncForKeepalived = ratelimiter.NewRateLimitedFunction(keyFunc, interval, handlerFunc)
	proxier.rateLimitedCommitFuncForKeepalived.RunUntil(proxier.rateLimitedCommitStopChannel)
}

func (proxier *Proxier) enableHaproxyRateLimiter(interval int, handlerFunc ratelimiter.HandlerFunc) {
	keyFunc := func(_ interface{}) (string, error) {
		return "proxier_haproxy", nil
	}

	proxier.rateLimitedCommitFuncForHaproxy = ratelimiter.NewRateLimitedFunction(keyFunc, interval, handlerFunc)
	proxier.rateLimitedCommitFuncForHaproxy.RunUntil(proxier.rateLimitedCommitStopChannel)
}

func (proxier *Proxier) handleServiceAdd(service *api.Service) {
	if !api.IsServiceIPSet(service) {
		glog.V(3).Infof("Skipping service %s due to ClusterIP = %q", fmt.Sprintf("%s/%s", service.Namespace, service.Name), service.Spec.ClusterIP)
		return
	}

	proxier.lock.Lock()
	defer proxier.lock.Unlock()

	svcName := types.NamespacedName{
		Namespace: service.Namespace,
		Name:      service.Name,
	}

	if !proxier.vipTable[service.Spec.ClusterIP] {
		proxier.vipTable[service.Spec.ClusterIP] = true
		proxier.commitKeepalived()
	}

	for i := range service.Spec.Ports {
		servicePort := &service.Spec.Ports[i]

		serviceName := proxy.ServicePortName{
			NamespacedName: svcName,
			Port:           servicePort.Name,
		}
		svc := proxy.Service{
			ClusterIP: service.Spec.ClusterIP,
			Port:      servicePort.Port,
			Protocol:  servicePort.Protocol,
		}

		if oldServiceUnit, ok := proxier.findServiceUnit(serviceName.String()); ok {
			if !reflect.DeepEqual(oldServiceUnit.ServiceInfo, svc) {
				oldServiceUnit.ServiceInfo = svc
			}
		} else {
			proxier.routeTable[serviceName.String()] = &proxy.ServiceUnit{
				Name:        serviceName.String(),
				ServiceInfo: svc,
				Endpoints:   []proxy.Endpoint{},
			}
		}
	}
	proxier.commitHaproxy()
}

func (proxier *Proxier) handleServiceDelete(service *api.Service) {
	proxier.lock.Lock()
	defer proxier.lock.Unlock()

	delete(proxier.vipTable, service.Spec.ClusterIP)
	proxier.commitKeepalived()

	svcName := types.NamespacedName{
		Namespace: service.Namespace,
		Name:      service.Name,
	}
	for i := range service.Spec.Ports {
		servicePort := &service.Spec.Ports[i]
		serviceName := proxy.ServicePortName{
			NamespacedName: svcName,
			Port:           servicePort.Name,
		}

		delete(proxier.routeTable, serviceName.String())
	}
	proxier.commitHaproxy()
}

func (proxier *Proxier) handleEndpointsAdd(endpoints *api.Endpoints) {
	proxier.lock.Lock()
	defer proxier.lock.Unlock()

	// We need to build a map of portname -> all ip:ports for that
	// portname. Explode Endpoints.Subsets[*] into this structure.
	portsToEndpoints := map[string][]proxy.Endpoint{}
	for i := range endpoints.Subsets {
		ss := &endpoints.Subsets[i]
		for i := range ss.Ports {
			port := &ss.Ports[i]
			for i := range ss.Addresses {
				addr := &ss.Addresses[i]
				portsToEndpoints[port.Name] = append(portsToEndpoints[port.Name], proxy.Endpoint{addr.IP, port.Port})
			}
		}
	}

	for portname := range portsToEndpoints {
		svcPort := proxy.ServicePortName{NamespacedName: types.NamespacedName{Namespace: endpoints.Namespace, Name: endpoints.Name}, Port: portname}
		newEndpoints := portsToEndpoints[portname]
		if oldServiceUnit, ok := proxier.findServiceUnit(svcPort.String()); ok {
			if !reflect.DeepEqual(oldServiceUnit.Endpoints, newEndpoints) {
				oldServiceUnit.Endpoints = newEndpoints
			}
		} else {
			newServiceUnit := proxier.createServiceUnit(svcPort.String())
			newServiceUnit.Endpoints = append(newServiceUnit.Endpoints, newEndpoints...)
		}
	}
	proxier.commitHaproxy()
}

func (proxier *Proxier) handleEndpointsDelete(endpoints *api.Endpoints) {
	proxier.lock.Lock()
	defer proxier.lock.Unlock()

	portsNameMap := map[string]bool{}
	for i := range endpoints.Subsets {
		ss := &endpoints.Subsets[i]
		for i := range ss.Ports {
			port := &ss.Ports[i]
			portsNameMap[port.Name] = true
		}
	}

	for portname := range portsNameMap {
		svcPort := proxy.ServicePortName{NamespacedName: types.NamespacedName{Namespace: endpoints.Namespace, Name: endpoints.Name}, Port: portname}
		if oldServiceUnit, ok := proxier.findServiceUnit(svcPort.String()); ok {
			oldServiceUnit.Endpoints = []proxy.Endpoint{}
		}
	}
	proxier.commitHaproxy()
}

func (proxier *Proxier) commitKeepalived() {
	if !proxier.master.IsSet() || proxier.skipCommit {
		glog.V(4).Infof("Skipping Keepalived commit for state: %s,%s", proxier.master.IsSet(), proxier.skipCommit)
	} else {
		proxier.rateLimitedCommitFuncForKeepalived.Invoke(proxier.rateLimitedCommitFuncForKeepalived)
	}
}

func (proxier *Proxier) commitHaproxy() {
	if !proxier.master.IsSet() || proxier.skipCommit {
		glog.V(4).Infof("Skipping HAProxy commit for state: %s,%s", proxier.master.IsSet(), proxier.skipCommit)
	} else {
		proxier.rateLimitedCommitFuncForHaproxy.Invoke(proxier.rateLimitedCommitFuncForHaproxy)
	}
}

func (proxier *Proxier) commitAndReloadKeepalived() error {
	templateContent, err := ioutil.ReadFile(proxier.config.KeepalivedConfig.TemplatePath)
	if err != nil {
		return err
	}
	proxier.lock.Lock()
	cfgBytes, err := template.RenderTemplate("keepalived_tpl", string(templateContent), proxier.vipTable)
	if err != nil {
		proxier.lock.Unlock()
		return err
	}
	proxier.lock.Unlock()

	// Reload Keepalived
	if err := proxier.keepalivedInstance.Reload(cfgBytes); err != nil {
		return err
	}
	return nil
}

func (proxier *Proxier) commitAndReloadHaproxy() error {
	templateContent, err := ioutil.ReadFile(proxier.config.HaproxyConfig.TemplatePath)
	if err != nil {
		return err
	}
	ips, err := proxier.ip.GetAddrs()
	if err != nil {
		return err
	}

	proxier.lock.Lock()
	templateData := template.TemplateData{IPs: ips, RouteTable: proxier.routeTable}
	cfgBytes, err := template.RenderTemplateWithFuncs("haproxy_tpl", string(templateContent), templateData)
	if err != nil {
		proxier.lock.Unlock()
		return err
	}
	proxier.lock.Unlock()

	// Reload HAProxy
	if err := proxier.haproxyInstance.Reload(cfgBytes); err != nil {
		return err
	}
	return nil
}

// createServiceUnit creates a new service unit with given name.
func (proxier *Proxier) createServiceUnit(name string) *proxy.ServiceUnit {
	service := &proxy.ServiceUnit{
		Name:      name,
		Endpoints: []proxy.Endpoint{},
	}

	proxier.routeTable[name] = service
	return service
}

// findServiceUnit finds the service unit with given name.
func (proxier *Proxier) findServiceUnit(name string) (*proxy.ServiceUnit, bool) {
	v, ok := proxier.routeTable[name]
	return v, ok
}

func (proxier *Proxier) HandleService(eventType watch.EventType, service *api.Service) error {
	switch eventType {
	case watch.Added, watch.Modified:
		proxier.handleServiceAdd(service)
	case watch.Deleted:
		proxier.handleServiceDelete(service)
	}

	return nil
}

func (proxier *Proxier) HandleEndpoints(eventType watch.EventType, endpoints *api.Endpoints) error {
	switch eventType {
	case watch.Added, watch.Modified:
		proxier.handleEndpointsAdd(endpoints)
	case watch.Deleted:
		proxier.handleEndpointsDelete(endpoints)
	}

	return nil
}

// SetMaster indicates to the proxier whether in MASTER
// state or BACKUP state.
func (proxier *Proxier) SetMaster(master bool) {
	if master {
		proxier.master.Set()
		// When we transfered to MASTER
		// we need to sync configuration manually
		proxier.commitAndReloadKeepalived()
		proxier.commitAndReloadHaproxy()
	} else {
		proxier.master.UnSet()
	}
}

// SetSkipCommit indicates to the proxier whether requests to
// commit/reload should be skipped.
func (proxier *Proxier) SetSkipCommit(skipCommit bool) {
	if proxier.skipCommit != skipCommit {
		glog.V(4).Infof("Updating skipCommit to %s", skipCommit)
		proxier.skipCommit = skipCommit
	}
}
