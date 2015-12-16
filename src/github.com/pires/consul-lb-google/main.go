package main

import (
	"flag"
	"os"
	"os/signal"
	"sync"

	"github.com/pires/consul-lb-google/registry"
	"github.com/pires/consul-lb-google/registry/consul"

	"github.com/golang/glog"
)

const ()

var (
	consulAddr = flag.String("consul", "consul.service.consul:8500", "Consul server adress (host:port)")
)

func main() {
	flag.Parse()

	glog.Info("Starting..")

	// TODO GCP stuff

	// connect to Consul
	glog.Infof("Connecting to Consul at %s..", *consulAddr)
	r, err := consul.NewRegistry(&registry.Config{
		Addresses: []string{*consulAddr},
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
				}
				lock.Unlock()
			case registry.DELETED:
				lock.Lock()
				if isRunning {
					isRunning = false
					glog.Infof("Stopped watching service [%s].", serviceName)
				}
				lock.Unlock()
			case registry.CHANGED:
				lock.Lock()
				// have all instances been removed?
				if len(update.ServiceInstances) == 0 {
					// TODO delete all instances from instance group
				} else {
					// identify any deleted instances and remove from instance group
					if len(update.ServiceInstances) < len(instances) {
						glog.Warningf("Removing %d instances.", len(instances)-len(update.ServiceInstances))
						for k := range instances {
							if _, ok := update.ServiceInstances[k]; !ok {
								delete(instances, k)
								// TODO remove from cloud
								glog.Warningf("Removing instance [%s].", k)
							}
						}
					}

					// find new or changed instances and create or change accordingly in cloud
					for k, v := range update.ServiceInstances {
						if instance, ok := instances[k]; !ok {
							// new instance, create
							glog.Warningf("Creating instance [%s].", k)
							instances[k] = v
							// TODO create in cloud
						} else {
							glog.Warningf("Probable changes in [%s].", instance)
							// already exists, compare
							// TODO handle service port changes
							// TODO change in cloud
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
