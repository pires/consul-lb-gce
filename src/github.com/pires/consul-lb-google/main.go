package main

import (
	"flag"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/pires/consul-lb-google/cloud"
	"github.com/pires/consul-lb-google/registry"
	"github.com/pires/consul-lb-google/registry/consul"

	"github.com/BurntSushi/toml"
	"github.com/golang/glog"
)

var (
	config = flag.String("config", "config.toml", "Path to the configuration file")

	client cloud.Cloud

	err error
)

type consulConfiguration struct {
	Url string
}

type cloudConfiguration struct {
	Project      string
	Network      string
	AllowedZones []string `toml:"allowed_zones"`
}

type configuration struct {
	Consul consulConfiguration
	Cloud  cloudConfiguration
}

func main() {
	flag.Parse()

	glog.Info("Starting..")

	// read configuration
	var cfg configuration
	if _, err := toml.DecodeFile(*config, &cfg); err != nil {
		panic(err)
	}

	// provision cloud client
	glog.Infof("Initializing cloud client [Project ID: %s, Network: %s, Allowed Zones: %#v]..", cfg.Cloud.Project, cfg.Cloud.Network, cfg.Cloud.AllowedZones)
	client, err = cloud.New(cfg.Cloud.Project, cfg.Cloud.Network, cfg.Cloud.AllowedZones)
	if err != nil {
		panic(err)
	}

	// connect to Consul
	glog.Infof("Connecting to Consul at %s..", cfg.Consul.Url)
	r, err := consul.NewRegistry(&registry.Config{
		Addresses: []string{cfg.Consul.Url},
	})
	if err != nil {
		panic(err)
	}

	glog.Info("Initiating registry..")
	updates := make(chan *registry.ServiceUpdate)
	done := make(chan struct{})
	// register for service updates
	// TODO add filtering for selecting just a limited set of services, e.g based on tags
	go r.Run(updates, done)

	glog.Info("Waiting for service updates..")
	go func(updates <-chan *registry.ServiceUpdate, done chan struct{}) {
		var wg sync.WaitGroup
		handlers := make(map[string]chan *registry.ServiceUpdate)
		for {
			select {
			case update := <-updates:
				// is there and handler for updated service?
				if handler, ok := handlers[update.ServiceName]; !ok {
					// no so provision handler
					handler = make(chan *registry.ServiceUpdate)
					handlers[update.ServiceName] = handler
					// start handler in its own goroutine
					wg.Add(1)
					go handleService(update.ServiceName, handler, wg, done)
				}
				// send update to handler
				handlers[update.ServiceName] <- update
			case <-done:
				// wait for all handlers to terminate
				wg.Wait()
				return
			}
		}
	}(updates, done)

	// wait for Ctrl-c to stop server
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	<-c

	glog.Info("Terminating all pending jobs..")
	close(done)
	glog.Info("Terminated")
}

// handleService handles service updates in a consistent way.
// It will run until service is deleted or done is closed.
func handleService(name string, updates <-chan *registry.ServiceUpdate, wg sync.WaitGroup, done chan struct{}) {
	// service model
	lock := &sync.RWMutex{}
	var serviceName string
	isRunning := false
	instances := make(map[string]*registry.ServiceInstance)

	for {
		select {
		case update := <-updates:
			switch update.UpdateType {
			case registry.NEW:
				lock.Lock()
				if !isRunning {
					serviceName = update.ServiceName
					isRunning = true
					glog.Infof("Watching service [%s].", serviceName)
					client.CreateInstanceGroup(serviceName)
				}
				lock.Unlock()
			case registry.DELETED:
				lock.Lock()
				if isRunning {
					isRunning = false
					instances = make(map[string]*registry.ServiceInstance)
					glog.Infof("Stopped watching service [%s].", serviceName)
				}
				// remove everything
				// do it outside of the conditional block above, just in case there's
				// stalled stuff in the cloud that should be removed
				client.RemoveInstanceGroup(serviceName)
				lock.Unlock()
			case registry.CHANGED:
				lock.Lock()

				// TODO validate if we've created the instance group for this service
				// if not, print warning.. possible recreate

				// have all instances been removed?
				if len(update.ServiceInstances) == 0 {
					toRemove := make([]string, 0, len(instances))
					for k := range instances {
						// need to split k because Consul stores FQDN
						toRemove = append(toRemove, strings.Split(k, ".")[0])
					}
					if len(toRemove) > 0 {
						client.RemoveInstancesFromInstanceGroup(toRemove, serviceName)
					}
				} else {
					// identify any deleted instances and remove from instance group
					if len(update.ServiceInstances) < len(instances) {
						glog.Warningf("Removing %d instances.", len(instances)-len(update.ServiceInstances))
						var toRemove []string
						for k := range instances {
							if _, ok := update.ServiceInstances[k]; !ok {
								// need to split k because Consul stores FQDN
								toRemove = append(toRemove, strings.Split(k, ".")[0])
								delete(instances, k)
								glog.Warningf("Removing instance [%s].", k)
							}
						}

						// do we have instances to remove from the instance group?
						if len(toRemove) > 0 {
							client.RemoveInstancesFromInstanceGroup(toRemove, serviceName)
						}
					}

					// find new or changed instances and create or change accordingly in cloud
					for k, v := range update.ServiceInstances {
						var toAdd []string
						if instance, ok := instances[k]; !ok {
							// new instance, create
							glog.Warningf("Creating instance [%s].", k)
							instances[k] = v
							// mark as new instance for further processing
							// need to split k because Consul stores FQDN
							toAdd = append(toAdd, strings.Split(k, ".")[0])
						} else {
							glog.Warningf("Probable changes in [%s].", instance)
							// already exists, compare
							// TODO handle service port changes
							// TODO change in cloud
						}

						// do we have new instances to add to the instance group?
						if len(toAdd) > 0 {
							client.AddInstancesToInstanceGroup(toAdd, serviceName)
						}
					}

				}

				lock.Unlock()
			default:
				continue
			}
		case <-done:
			glog.Warningf("Received termination signal for service [%s]", serviceName)
			wg.Done()
			return
		}
	}
}
