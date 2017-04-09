package template

import (
	"fmt"
	"io/ioutil"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/adohe/kube2haproxy/proxy"
	"github.com/adohe/kube2haproxy/util/abool"
	utilhaproxy "github.com/adohe/kube2haproxy/util/haproxy"
	"github.com/adohe/kube2haproxy/util/ipaddr"
	utilkeepalived "github.com/adohe/kube2haproxy/util/keepalived"
	"github.com/adohe/kube2haproxy/util/ratelimiter"
	"github.com/adohe/kube2haproxy/util/template"

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
		config:                             cfg,
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

	proxier.enableKeepalivedRateLimiter(cfg.KeepalivedConfig.ReloadInterval, proxier.commitAndReloadKeepalived)
	proxier.enableHaproxyRateLimiter(cfg.HaproxyConfig.ReloadInterval, proxier.commitAndReloadHaproxy)

	return proxier, nil
}

func (proxier *Proxier) enableKeepalivedRateLimiter(interval time.Duration, handlerFunc ratelimiter.HandlerFunc) {
	proxier.rateLimitedCommitFuncForKeepalived = ratelimiter.NewRateLimitedFunction("proxier_keepalived", interval, handlerFunc)
	proxier.rateLimitedCommitFuncForKeepalived.RunUntil(proxier.rateLimitedCommitStopChannel)
}

func (proxier *Proxier) enableHaproxyRateLimiter(interval time.Duration, handlerFunc ratelimiter.HandlerFunc) {
	proxier.rateLimitedCommitFuncForHaproxy = ratelimiter.NewRateLimitedFunction("proxier_haproxy", interval, handlerFunc)
	proxier.rateLimitedCommitFuncForHaproxy.RunUntil(proxier.rateLimitedCommitStopChannel)
}

func (proxier *Proxier) handleServiceAdd(service *api.Service) {
	if !api.IsServiceIPSet(service) {
		glog.V(4).Infof("Skipping service %s due to ClusterIP = %q", fmt.Sprintf("%s/%s", service.Namespace, service.Name), service.Spec.ClusterIP)
		return
	}

	proxier.lock.Lock()
	defer proxier.lock.Unlock()

	svcName := types.NamespacedName{
		Namespace: service.Namespace,
		Name:      service.Name,
	}
	glog.V(4).Infof("handle service %s add event", svcName.String())

	// add service cluster IP to vip table
	if !proxier.vipTable[service.Spec.ClusterIP] {
		proxier.vipTable[service.Spec.ClusterIP] = true
		proxier.commitKeepalived()
	}

	for i := range service.Spec.Ports {
		servicePort := &service.Spec.Ports[i]
		if servicePort.Protocol == api.ProtocolUDP {
			// Skip UDP service, HAProxy doesn't support UDP
			glog.V(4).Infof("Skipping service port %s due to UDP protocol", fmt.Sprintf("%s:%s:%s", service.Namespace, service.Name, servicePort.Name))
			continue
		}

		svcPortName := proxy.ServicePortName{
			NamespacedName: svcName,
			Port:           servicePort.Name,
		}
		svc := proxy.Service{
			ClusterIP: service.Spec.ClusterIP,
			Port:      servicePort.Port,
			Protocol:  string(servicePort.Protocol),
		}

		oldSvcUnit, exist := proxier.findServiceUnit(svcPortName.String())
		if exist {
			if !reflect.DeepEqual(oldSvcUnit.ServiceInfo, svc) {
				glog.V(4).Infof("%s changed. Update it", svcPortName.String())
				oldSvcUnit.ServiceInfo = svc
			}
		} else {
			glog.V(4).Infof("add %s service info", svcPortName.String())
			proxier.routeTable[svcPortName.String()] = &proxy.ServiceUnit{
				Name:        svcPortName.String(),
				ServiceInfo: svc,
				Endpoints:   []proxy.Endpoint{},
			}
		}
	}
	proxier.commitHaproxy()
}

func (proxier *Proxier) handleServiceUpdate(service *api.Service) {
	if !api.IsServiceIPSet(service) {
		glog.V(4).Infof("Skipping service %s due to ClusterIP = %q", fmt.Sprintf("%s/%s", service.Namespace, service.Name), service.Spec.ClusterIP)
		return
	}

	proxier.lock.Lock()
	defer proxier.lock.Unlock()

	svcName := types.NamespacedName{
		Namespace: service.Namespace,
		Name:      service.Name,
	}
	glog.V(4).Infof("handle service %s update event", svcName.String())

	// Normally we should check this to avoid service cluster IP change,
	// but this is not always right. service update event may comes before
	// service add event.
	if !proxier.vipTable[service.Spec.ClusterIP] {
		proxier.vipTable[service.Spec.ClusterIP] = true
		proxier.commitKeepalived()
	}

	oldSvcPortsList := proxier.getServicePorts(fmt.Sprintf("%s:%s", svcName.Namespace, svcName.Name))
	newSvcPortsList := []string{}
	for i := range service.Spec.Ports {
		servicePort := &service.Spec.Ports[i]
		if servicePort.Protocol == api.ProtocolUDP {
			// Skip UDP service, HAProxy doesn't support UDP
			glog.V(4).Infof("Skipping service port %s due to UDP protocol", fmt.Sprintf("%s:%s:%s", service.Namespace, service.Name, servicePort.Name))
			continue
		}

		svcPortName := proxy.ServicePortName{
			NamespacedName: svcName,
			Port:           servicePort.Name,
		}
		svc := proxy.Service{
			ClusterIP: service.Spec.ClusterIP,
			Port:      servicePort.Port,
			Protocol:  string(servicePort.Protocol),
		}

		if oldServiceUnit, exist := proxier.findServiceUnit(svcPortName.String()); exist {
			if !reflect.DeepEqual(oldServiceUnit.ServiceInfo, svc) {
				glog.V(4).Infof("service info %s changed, needs update %#v", svcPortName.String(), svc)
				oldServiceUnit.ServiceInfo = svc
			}
		} else {
			glog.V(4).Infof("service info %s not exist, needs add %#v", svcPortName.String(), svc)
			proxier.routeTable[svcPortName.String()] = &proxy.ServiceUnit{
				Name:        svcPortName.String(),
				ServiceInfo: svc,
				Endpoints:   []proxy.Endpoint{},
			}
		}
		newSvcPortsList = append(newSvcPortsList, svcPortName.String())
	}

	// Calculate service port should be deleted
	diffList := diff(oldSvcPortsList, newSvcPortsList)
	for _, diff := range diffList {
		delete(proxier.routeTable, diff)
	}
	proxier.commitHaproxy()
}

func (proxier *Proxier) handleServiceDelete(service *api.Service) {
	proxier.lock.Lock()
	defer proxier.lock.Unlock()

	svcName := types.NamespacedName{
		Namespace: service.Namespace,
		Name:      service.Name,
	}

	glog.V(4).Infof("handle service %s delete event", svcName.String())

	delete(proxier.vipTable, service.Spec.ClusterIP)
	proxier.commitKeepalived()

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

	epName := types.NamespacedName{
		Namespace: endpoints.Namespace,
		Name:      endpoints.Name,
	}
	glog.V(4).Infof("handle endpoints %s add event", epName.String())

	if len(endpoints.Subsets) == 0 {
		// Skip endpoints as endpoints has no subsets
		glog.V(4).Infof("Skipping endpoints %s due to empty subsets", epName.String())
		return
	}

	// We need to build a map of portname -> all ip:ports for that
	// portname. Explode Endpoints.Subsets[*] into this structure.
	portsToEndpoints := map[string][]proxy.Endpoint{}
	for i := range endpoints.Subsets {
		ss := &endpoints.Subsets[i]
		if len(ss.NotReadyAddresses) != 0 {
			glog.V(4).Infof("endpoints %s has not ready address, please check this", epName.String())
		}
		for i := range ss.Ports {
			port := &ss.Ports[i]
			for i := range ss.Addresses {
				addr := &ss.Addresses[i]
				portsToEndpoints[port.Name] = append(portsToEndpoints[port.Name], proxy.Endpoint{addr.IP, port.Port})
			}
		}
	}

	for portname := range portsToEndpoints {
		svcPortName := proxy.ServicePortName{NamespacedName: epName, Port: portname}
		newEndpoints := portsToEndpoints[portname]
		if oldServiceUnit, exist := proxier.findServiceUnit(svcPortName.String()); exist {
			if !reflect.DeepEqual(oldServiceUnit.Endpoints, newEndpoints) {
				glog.V(4).Infof("endpoints of %s changed, needs update %v", svcPortName.String(), newEndpoints)
				oldServiceUnit.Endpoints = newEndpoints
			}
		} else {
			glog.V(4).Infof("endpoints of %s not exist, needs add %v", svcPortName.String(), newEndpoints)
			newServiceUnit := proxier.createServiceUnit(svcPortName.String())
			newServiceUnit.Endpoints = append(newServiceUnit.Endpoints, newEndpoints...)
		}
	}
	proxier.commitHaproxy()
}

func (proxier *Proxier) handleEndpointsUpdate(endpoints *api.Endpoints) {
	proxier.lock.Lock()
	defer proxier.lock.Unlock()

	epName := types.NamespacedName{
		Namespace: endpoints.Namespace,
		Name:      endpoints.Name,
	}
	glog.V(4).Infof("handle endpoints %s update event", epName.String())

	if len(endpoints.Subsets) == 0 {
		glog.V(4).Infof("endpoints %s has empty subsets, this may happen when deploy", epName.String())
	}

	oldSvcPortsList := proxier.getServicePorts(fmt.Sprintf("%s:%s", epName.Namespace, epName.Name))
	newSvcPortsList := []string{}

	// We need to build a map of portname -> all ip:ports for that
	// portname. Explode Endpoints.Subsets[*] into this structure.
	portsToEndpoints := map[string][]proxy.Endpoint{}
	for i := range endpoints.Subsets {
		ss := &endpoints.Subsets[i]
		if len(ss.NotReadyAddresses) != 0 {
			glog.V(4).Infof("endpoints %s has not ready address, please check this", epName.String())
		}
		for i := range ss.Ports {
			port := &ss.Ports[i]
			for i := range ss.Addresses {
				addr := &ss.Addresses[i]
				portsToEndpoints[port.Name] = append(portsToEndpoints[port.Name], proxy.Endpoint{addr.IP, port.Port})
			}
		}
	}

	for portname := range portsToEndpoints {
		svcPortName := proxy.ServicePortName{NamespacedName: epName, Port: portname}
		newEndpoints := portsToEndpoints[portname]
		if oldServiceUnit, exist := proxier.findServiceUnit(svcPortName.String()); exist {
			if !reflect.DeepEqual(oldServiceUnit.Endpoints, newEndpoints) {
				glog.V(4).Infof("endpoints of %s changed, needs update %v", svcPortName.String(), newEndpoints)
				oldServiceUnit.Endpoints = newEndpoints
			}
		} else {
			glog.V(4).Infof("endpoints of %s not exist, needs add %v", svcPortName.String(), newEndpoints)
			newServiceUnit := proxier.createServiceUnit(svcPortName.String())
			newServiceUnit.Endpoints = append(newServiceUnit.Endpoints, newEndpoints...)
		}
		newSvcPortsList = append(newSvcPortsList, svcPortName.String())
	}

	diffList := diff(oldSvcPortsList, newSvcPortsList)
	for _, diff := range diffList {
		proxier.routeTable[diff].Endpoints = []proxy.Endpoint{}
	}
	proxier.commitHaproxy()
}

func (proxier *Proxier) handleEndpointsDelete(endpoints *api.Endpoints) {
	proxier.lock.Lock()
	defer proxier.lock.Unlock()

	epName := types.NamespacedName{
		Namespace: endpoints.Namespace,
		Name:      endpoints.Name,
	}
	glog.V(4).Infof("handle endpoints %s delete event", epName.String())

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
		glog.V(8).Infof("Skipping Keepalived commit for state: Master(%t),SkipCommit(%t)", proxier.master.IsSet(), proxier.skipCommit)
	} else {
		glog.V(4).Infof("commit keepalived")
		proxier.rateLimitedCommitFuncForKeepalived.Invoke(proxier.rateLimitedCommitFuncForKeepalived)
	}
}

func (proxier *Proxier) commitHaproxy() {
	if !proxier.master.IsSet() || proxier.skipCommit {
		glog.V(8).Infof("Skipping HAProxy commit for state: Master(%t),SkipCommit(%t)", proxier.master.IsSet(), proxier.skipCommit)
	} else {
		glog.V(4).Infof("commit haproxy")
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

// getServicePorts returns a service ports name list.
func (proxier *Proxier) getServicePorts(svcName string) []string {
	names := []string{}
	for name, _ := range proxier.routeTable {
		svcNameExist := name[:strings.LastIndex(name, ":")]
		if svcNameExist == svcName {
			names = append(names, name)
		}
	}
	return names
}

// findServiceUnit finds the service unit with given name.
func (proxier *Proxier) findServiceUnit(name string) (*proxy.ServiceUnit, bool) {
	v, ok := proxier.routeTable[name]
	return v, ok
}

func (proxier *Proxier) HandleService(eventType watch.EventType, service *api.Service) error {
	switch eventType {
	case watch.Added:
		proxier.handleServiceAdd(service)
	case watch.Modified:
		proxier.handleServiceUpdate(service)
	case watch.Deleted:
		proxier.handleServiceDelete(service)
	}

	return nil
}

func (proxier *Proxier) HandleEndpoints(eventType watch.EventType, endpoints *api.Endpoints) error {
	switch eventType {
	case watch.Added:
		proxier.handleEndpointsAdd(endpoints)
	case watch.Modified:
		proxier.handleEndpointsUpdate(endpoints)
	case watch.Deleted:
		proxier.handleEndpointsDelete(endpoints)
	}

	return nil
}

// SetMaster indicates to the proxier whether in MASTER
// state or BACKUP state.
func (proxier *Proxier) SetMaster(master bool) {
	if master {
		// When we transfered to MASTER
		// we need to sync configuration manually
		glog.V(4).Infof("Entering MASTER state, reload manually")
		proxier.commitAndReloadKeepalived()
		// This should be long enough for virtual IP binding happen
		time.Sleep(2000 * time.Millisecond)
		proxier.commitAndReloadHaproxy()
		// Set Master state TRUE to avoid frequently keepalived reload
		proxier.master.Set()
	} else {
		proxier.master.UnSet()
	}
}

// SetSkipCommit indicates to the proxier whether requests to
// commit/reload should be skipped.
func (proxier *Proxier) SetSkipCommit(skipCommit bool) {
	if proxier.skipCommit != skipCommit {
		glog.V(4).Infof("Updating skipCommit to %t", skipCommit)
		proxier.skipCommit = skipCommit
	}
}

// diff will calculate the diff between the old&new list.
func diff(oldList, newList []string) []string {
	result := []string{}
	for _, oldItem := range oldList {
		found := false
		for _, newItem := range newList {
			if oldItem == newItem {
				found = true
				break
			}
		}
		if !found {
			result = append(result, oldItem)
		}
	}
	return result
}
