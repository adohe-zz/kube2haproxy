package proxy

import (
	"fmt"

	"k8s.io/kubernetes/pkg/types"
)

// ServiceUnit is an encapsulation of a service, the endpoints that back that service.
// This is the data that drives the creation of HAProxy configuration file.
type ServiceUnit struct {
	// Name corresponds to ServicePortName. Uniquely identifies the ServiceUnit.
	Name string

	// Internal service info of this ServiceUnit, this translates into a real
	// frontend implementation of HAProxy.
	ServiceInfo Service

	// Endpoints are endpoints that back the service, this translates into a final
	// backend implementation of HAProxy.
	Endpoints []Endpoint
}

// ServicePortName is a combination of service.Namespace, service.Name and service.Ports[*].Name
type ServicePortName struct {
	types.NamespacedName
	Port string
}

func (spn ServicePortName) String() string {
	return fmt.Sprintf("%s:%s:%s", spn.NamespacedName.Namespace, spn.NamespacedName.Name, spn.Port)
}

// Service is an internal representation of k8s Service.
type Service struct {
	ClusterIP string
	Port      int
	Protocol  string
}

// Endpoint is an internal representation of k8s Endpoint.
type Endpoint struct {
	IP   string
	Port int
}
