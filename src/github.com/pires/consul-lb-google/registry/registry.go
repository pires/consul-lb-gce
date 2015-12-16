package registry

const (
	UPDATED = "UPDATED"
	DELETED = "DELETED"
)

// Service represents a registered service
type Service struct {
	Name      string
	Instances []*ServiceInstance
}

// ServiceInstance represents an instance of a service
type ServiceInstance struct {
	Host    string
	Address string
	Tags    []string
	Port    string // cloud providers usually use string, not numbers
}

// ServiceUpdate represents a service update event
type ServiceUpdate struct {
	UpdateType       string
	ServiceInstances []*ServiceInstance
}

// Config represents a registry's configuration
type Config struct {
	Addresses []string
}

// Registry represents a registry for services
type Registry interface {
	// Run starts the registry returning a channel for registry cancelation
	Run(upstream chan<- *ServiceUpdate, done <-chan struct{})
}
