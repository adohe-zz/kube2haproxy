package controller

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/adohe/kube2haproxy/proxy/template"
	khcache "github.com/adohe/kube2haproxy/util/cache"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/cache"
	kubeclient "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/fields"
	utilruntime "k8s.io/kubernetes/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/util/wait"
	"k8s.io/kubernetes/pkg/watch"

	"github.com/golang/glog"
)

// ProxyController abstracts the details of watching Services and
// Endpoints from the Proxy implementation being used.
type ProxyController struct {
	lock sync.Mutex

	Proxier       *template.Proxier
	NextService   func() (watch.EventType, *api.Service, error)
	NextEndpoints func() (watch.EventType, *api.Endpoints, error)

	ServiceListConsumed   func() bool
	EndpointsListConsumed func() bool
	serviceListConsumed   bool
	endpointsListConsumed bool
}

func New(client *kubeclient.Client, proxier *template.Proxier, resyncPeriod time.Duration) *ProxyController {
	serviceEventQueue := khcache.NewEventQueue(cache.MetaNamespaceKeyFunc)
	cache.NewReflector(createServiceLW(client), &api.Service{}, serviceEventQueue, resyncPeriod).Run()
	endpointsEventQueue := khcache.NewEventQueue(cache.MetaNamespaceKeyFunc)
	cache.NewReflector(createEndpointsLW(client), &api.Endpoints{}, endpointsEventQueue, resyncPeriod).Run()

	return &ProxyController{
		Proxier: proxier,
		NextService: func() (watch.EventType, *api.Service, error) {
			eventType, obj, err := serviceEventQueue.Pop()
			if err != nil {
				return watch.Error, nil, err
			}
			return eventType, obj.(*api.Service), nil
		},
		NextEndpoints: func() (watch.EventType, *api.Endpoints, error) {
			eventType, obj, err := endpointsEventQueue.Pop()
			if err != nil {
				return watch.Error, nil, err
			}
			return eventType, obj.(*api.Endpoints), nil
		},
		ServiceListConsumed: func() bool {
			return serviceEventQueue.ListConsumed()
		},
		EndpointsListConsumed: func() bool {
			return endpointsEventQueue.ListConsumed()
		},
	}
}

// Run begins watching and syncing.
func (c *ProxyController) Run(stopCh <-chan struct{}, signalCh <-chan os.Signal) {
	glog.V(4).Infof("Running proxy controller")
	go wait.Until(c.HandleService, 0, stopCh)
	go wait.Until(c.HandleEndpoints, 0, stopCh)

	// Handle MASTER transition event
	go func() {
		for {
			sig := <-signalCh
			if sig == syscall.SIGUSR1 {
				glog.Infof("receive SIGUSR1 set MASTER true")
				// SIGUSR1 means in MASTER state
				c.Proxier.SetMaster(true)
			} else {
				glog.Infof("receive SIGUSR2 set MASTER false")
				c.Proxier.SetMaster(false)
			}
		}
	}()

	<-stopCh
}

// HandleServices handles a single Service event and refreshes the proxy backend.
func (c *ProxyController) HandleService() {
	eventType, service, err := c.NextService()
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("unable to handle service: %v", err))
		return
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	// Change the local sync state within the lock to ensure that all
	// event handlers have the same view of the sync state.
	c.serviceListConsumed = c.ServiceListConsumed()
	c.updateLastSyncProcessed()

	if err := c.Proxier.HandleService(eventType, service); err != nil {
		utilruntime.HandleError(err)
	}
}

// HandleEndpoints handles a single Endpoints event and refreshes the proxy backend.
func (c *ProxyController) HandleEndpoints() {
	eventType, endpoints, err := c.NextEndpoints()
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("unable to handle endpoints: %v", err))
		return
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	// Change the local sync state within the lock to ensure that all
	// event handlers have the same view of the sync state.
	c.endpointsListConsumed = c.EndpointsListConsumed()
	c.updateLastSyncProcessed()

	if err := c.Proxier.HandleEndpoints(eventType, endpoints); err != nil {
		utilruntime.HandleError(err)
	}
}

// updateLastSyncProcessed notifies the proxier if the most recent sync
// of resource has been completed.
func (c *ProxyController) updateLastSyncProcessed() {
	lastSyncProcessed := c.endpointsListConsumed && c.serviceListConsumed
	c.Proxier.SetSkipCommit(!lastSyncProcessed)
}

func createServiceLW(client *kubeclient.Client) *cache.ListWatch {
	return cache.NewListWatchFromClient(client, "services", api.NamespaceAll, fields.Everything())
}

func createEndpointsLW(client *kubeclient.Client) *cache.ListWatch {
	return cache.NewListWatchFromClient(client, "endpoints", api.NamespaceAll, fields.Everything())
}
