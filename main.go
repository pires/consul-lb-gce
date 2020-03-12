package main

import (
	"flag"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/dffrntmedia/consul-lb-gce/cloud/gce"

	"github.com/BurntSushi/toml"
	"github.com/dffrntmedia/consul-lb-gce/cloud"
	"github.com/dffrntmedia/consul-lb-gce/registry"
	"github.com/dffrntmedia/consul-lb-gce/registry/consul"
	"github.com/golang/glog"
)

const (
	maximumNameLength = 63
)

func main() {
	var configurationPath string
	flag.StringVar(&configurationPath, "config", "config.toml", "Configuration path")
	flag.Parse()
	if configurationPath == "" {
		glog.Fatal("Configuration path is required")
	}

	glog.Infof("Reading configuration from %s ...", configurationPath)
	var c configuration
	if _, err := toml.DecodeFile(configurationPath, &c); err != nil {
		glog.Fatal(err)
	}

	tagParser := newTagParser(c.TagParser.TagPrefix)

	glog.Infof(
		"Initializing cloud client with project ID: %s, network: %s, zone: %s ...",
		c.Cloud.Project,
		c.Cloud.Network,
		c.Cloud.Zone,
	)
	client, err := cloud.New(c.Cloud.Project, c.Cloud.Network, c.Cloud.Zone)
	if err != nil {
		glog.Fatal(err)
	}

	glog.Infof("Connecting to Consul at %s ...", c.Consul.URL)
	r, err := consul.NewRegistry(&registry.Config{
		Addresses:   []string{c.Consul.URL},
		TagsToWatch: c.Consul.TagsToWatch,
	})
	if err != nil {
		glog.Fatal(err)
	}

	updates := make(chan *registry.ServiceUpdate)
	done := make(chan struct{})

	glog.Info("Listening for service updates...")
	go r.Run(updates, done)
	// service updates handler
	go handleServices(&c, client, tagParser, updates, done)

	// wait for Ctrl-c to stop server
	osSignalChannel := make(chan os.Signal, 1)
	signal.Notify(osSignalChannel, os.Interrupt, os.Kill)
	<-osSignalChannel

	glog.Info("Terminating...")
	close(done)
	glog.Info("Terminated")
}

// Handles updates for all services and dispatch them between specific per service handlers.
func handleServices(c *configuration, cloud cloud.Cloud, tagParser *tagParser, updates <-chan *registry.ServiceUpdate, done chan struct{}) {
	var wg sync.WaitGroup
	handlers := make(map[string]chan *registry.ServiceUpdate)

	for {
		select {
		case update := <-updates:
			glog.Infof("Handling %s service update", update.ServiceName)
			// is there a handler for updated service?
			if handler, ok := handlers[update.ServiceName]; !ok {
				// no handler
				handler = make(chan *registry.ServiceUpdate)
				handlers[update.ServiceName] = handler
				// start handler in its own goroutine
				wg.Add(1)
				glog.Infof("Initializing a handler for %s service", update.ServiceName)
				go handleService(c, cloud, tagParser, update.Tag, handler, &wg, done)
			}
			// send update to handler
			handlers[update.ServiceName] <- update
		case <-done:
			// wait for all handlers to terminate
			wg.Wait()
			return
		}
	}
}

// handleService handles service updates in a consistent way.
// It will run until service is deleted or done is closed.
func handleService(
	c *configuration,
	client cloud.Cloud,
	tagParser *tagParser,
	tag string,
	updates <-chan *registry.ServiceUpdate,
	wg *sync.WaitGroup,
	done chan struct{},
) {
	tagInfo, err := tagParser.Parse(tag)
	if err != nil {
		glog.Error(err)
		return
	}

	serviceGroupName := tagInfo.String()

	lock := &sync.RWMutex{}
	isRunning := false
	instances := make(map[string]*registry.ServiceInstance)

	for {
		select {
		case update := <-updates:
			switch update.UpdateType {
			case registry.NEW:
				lock.Lock()
				if !isRunning {
					glog.Infof("Initializing service with tag %s ...", tag)

					negName := makeName("neg", serviceGroupName)
					glog.Infof("Creating network endpoint group %s ...", negName)
					if err := client.CreateNetworkEndpointGroup(negName); err != nil {
						glog.Errorf("Can't create network endpoint group %s: %v", negName, err)
						lock.Unlock()
						continue
					}

					hcPath, err := c.Consul.GetHealthCheckPath(tag)
					if err != nil {
						glog.Error(err)
						continue
					}
					hcName := makeName("hc", serviceGroupName)
					glog.Infof("Creating health check %s ...", hcName)
					if err := client.CreateHealthCheck(hcName, hcPath); err != nil {
						glog.Errorf("Can't create health check %s: %v", hcName, err)
						lock.Unlock()
						continue
					}

					bsName := makeName("bs", serviceGroupName)
					glog.Infof("Creating backend service %s ...", bsName)
					if err = client.CreateBackendService(
						bsName,
						negName,
						hcName,
						tagInfo.Affinity,
						tagInfo.CDN,
					); err != nil {
						glog.Errorf("Can't create backend service %s: %v", bsName, err)
						lock.Unlock()
						continue
					}

					glog.Infof("Updating URL map %s ...", c.Cloud.URLMap)
					if err := client.UpdateURLMap(
						c.Cloud.URLMap,
						bsName,
						tagInfo.Host,
						tagInfo.Path,
					); err != nil {
						glog.Errorf("Can't update URL map %s: %v", c.Cloud.URLMap, err)
						lock.Unlock()
						continue
					}

					isRunning = true
					glog.Infof("Watching service with tag [%s]", tag)
				}
				lock.Unlock()
			case registry.DELETED:
				lock.Lock()
				if isRunning {
					var toRemove []gce.NetworkEndpoint

					for k, instance := range instances {
						toRemove = append(toRemove, gce.NetworkEndpoint{
							Instance: normalizeInstanceName(instance.Host),
							IP:       instance.Address,
							Port:     instance.Port,
						})
						delete(instances, k)
					}

					//do we have instances to remove from the NEG?
					if len(toRemove) > 0 {
						if err := client.DetachEndpointsFromGroup(toRemove, makeName("neg", serviceGroupName)); err != nil {
							glog.Errorf("Failed detaching endpoints from %s network endpoint group: %v", makeName("neg", serviceGroupName), err)
						}
					}

					isRunning = false
				}
				lock.Unlock()
			case registry.CHANGED:
				lock.Lock()

				// validate if we've created the instance group for this service
				if !isRunning {
					glog.Warningf("Ignoring received event for service with tag %s because it's not running", tag)
					lock.Unlock()
					break
				}

				var toAdd, toRemove []gce.NetworkEndpoint

				// finding instances to remove
				for k, instance := range instances {
					// Doesn't instance exist in update?
					if _, ok := update.ServiceInstances[k]; !ok {
						toRemove = append(toRemove, gce.NetworkEndpoint{
							Instance: normalizeInstanceName(instance.Host),
							IP:       instance.Address,
							Port:     instance.Port,
						})
						delete(instances, k)
					}
				}

				// finding instances to add
				for k, instance := range update.ServiceInstances {
					// Doesn't instance exist in running instances?
					if _, ok := instances[k]; !ok {
						toAdd = append(toAdd, gce.NetworkEndpoint{
							Instance: normalizeInstanceName(instance.Host),
							IP:       instance.Address,
							Port:     instance.Port,
						})
						instances[k] = instance
					}
				}

				//do we have instances to remove from the NEG?
				if len(toRemove) > 0 {
					if err := client.DetachEndpointsFromGroup(toRemove, makeName("neg", serviceGroupName)); err != nil {
						glog.Errorf("Failed detaching endpoints from %s network endpoint group: %v", makeName("neg", serviceGroupName), err)
					}
				}

				// do we have new instances to add to the NEG?
				if len(toAdd) > 0 {
					if err := client.AttachEndpointsToGroup(toAdd, makeName("neg", serviceGroupName)); err != nil {
						glog.Errorf("Failed adding endpoints to %s network endpoint group: %v", makeName("neg", serviceGroupName), err)
					}
				}

				lock.Unlock()
			default:
				continue
			}
		case <-done:
			glog.Warningf("Received termination signal for service with tag %s", tag)
			wg.Done()
			return
		}
	}
}

// NOTE: Maximum length of resource name is 63 and name must start and end with letter or digit
func makeName(prefix string, name string) string {
	n := strings.Join([]string{prefix, name}, "-")
	if len(n) > maximumNameLength {
		n = strings.TrimLeft(n[:maximumNameLength], "-")
	}
	return n
}

func normalizeInstanceName(name string) string {
	return strings.Split(name, ".")[0]
}
