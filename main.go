package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/dffrntmedia/consul-lb-gce/cloud"
	"github.com/dffrntmedia/consul-lb-gce/cloud/gce"
	"github.com/dffrntmedia/consul-lb-gce/registry"
	"github.com/dffrntmedia/consul-lb-gce/registry/consul"
	"github.com/golang/glog"
)

const (
	maximumNameLength = 63
)

func main() {
	var configFilePath string
	flag.StringVar(&configFilePath, "config", "config.json", "Configuration path")
	flag.Parse()
	if configFilePath == "" {
		glog.Fatal("Configuration path is required")
	}

	glog.Infof("Reading configuration from %q ...", configFilePath)
	var c configuration
	configFile, err := os.Open(configFilePath)
	if err != nil {
		glog.Fatal(err)
	}
	configBytes, err := ioutil.ReadAll(configFile)
	if err != nil {
		glog.Fatal(err)
	}
	if err := json.Unmarshal(configBytes, &c); err != nil {
		glog.Fatal(err)
	}

	glog.Infof(
		"Initializing cloud client with project ID: %q, network: %q, zone: %q ...",
		c.Cloud.Project,
		c.Cloud.Network,
		c.Cloud.Zone,
	)
	client, err := cloud.New(c.Cloud.Project, c.Cloud.Network, c.Cloud.Zone)
	if err != nil {
		glog.Fatal(err)
	}

	glog.Infof("Connecting to Consul at %q ...", c.Consul.URL)
	r, err := consul.NewRegistry(&registry.Config{
		Addresses:   []string{c.Consul.URL},
		TagsToWatch: c.GetTagNames(),
	})
	if err != nil {
		glog.Fatal(err)
	}

	updates := make(chan *registry.ServiceUpdate)
	done := make(chan struct{})

	glog.Info("Listening for service updates...")
	go r.Run(updates, done)

	go dispatchUpdates(&c, client, updates, done)

	// wait for Ctrl-c to stop server
	osSignalChannel := make(chan os.Signal, 1)
	signal.Notify(osSignalChannel, os.Interrupt, os.Kill)
	<-osSignalChannel

	glog.Info("Terminating...")
	close(done)
	glog.Info("Terminated")
}

// Dispatches updates for all services between per service channels.
func dispatchUpdates(c *configuration, cloud cloud.Cloud, updates <-chan *registry.ServiceUpdate, done chan struct{}) {
	var wg sync.WaitGroup
	serviceUpdatesMap := make(map[string]chan *registry.ServiceUpdate)

	for {
		select {
		case update := <-updates:
			// is there a channel for service updates?
			if serviceUpdates, ok := serviceUpdatesMap[update.ServiceName]; !ok {
				// no handler
				serviceUpdates = make(chan *registry.ServiceUpdate)
				serviceUpdatesMap[update.ServiceName] = serviceUpdates
				// start handler in its own goroutine
				wg.Add(1)
				glog.Infof("Initializing a handler for %q service...", update.ServiceName)
				go handleServiceUpdates(c, cloud, update.Tag, serviceUpdates, &wg, done)
			}
			// send update to channel
			serviceUpdatesMap[update.ServiceName] <- update
		case <-done:
			// wait for all updates are processed
			wg.Wait()
			return
		}
	}
}

// todo(max): Think about one more layer so that this function just sends updates into loadbalancer as data into that layer, hecne it's possible to test logic of the function.

// Handles service updates in a consistent way.
// It's up until service is deleted or done is closed.
func handleServiceUpdates(
	c *configuration,
	client cloud.Cloud,
	tag string,
	updates <-chan *registry.ServiceUpdate,
	wg *sync.WaitGroup,
	done chan struct{},
) {
	tagConfig, err := c.GetTagConfiguration(tag)
	if err != nil {
		glog.Error(err)
		return
	}

	shouldReuseResources := tagConfig.ReuseResources

	var tagInfo *tagInfo
	if !shouldReuseResources {
		tagInfo, err = parseTag(tag)
		if err != nil {
			glog.Error(err)
			return
		}
	}

	var serviceGroupName string
	if !shouldReuseResources {
		serviceGroupName = tagInfo.String()
	}

	negName := func() string {
		if shouldReuseResources {
			return tagConfig.NetworkEndpointGroupName
		}
		return makeName("neg", serviceGroupName)
	}()

	// todo(max): Why do we need lock if all service updates are processed one by one? It makes more sense to have one lock for all services if we update common resource as e.g. url map.
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
					glog.Infof("Initializing service with tag %q ...", tag)

					if shouldReuseResources {
						glog.Infof("Service with tag %q will re-use resources", tag)
					} else {
						negName := makeName("neg", serviceGroupName)
						glog.Infof("Creating network endpoint group %q ...", negName)
						if err := client.CreateNetworkEndpointGroup(negName); err != nil {
							glog.Errorf("Can't create network endpoint group %q: %+v", negName, err)
							lock.Unlock()
							continue
						}

						hcName := makeName("hc", serviceGroupName)
						glog.Infof("Creating health check %q ...", hcName)
						if err := client.CreateHealthCheck(hcName, tagConfig.HealthCheck.Path); err != nil {
							glog.Errorf("Can't create health check %q: %+v", hcName, err)
							lock.Unlock()
							continue
						}

						bsName := makeName("bs", serviceGroupName)
						glog.Infof("Creating backend service %q ...", bsName)
						if err = client.CreateBackendService(
							bsName,
							negName,
							hcName,
							tagInfo.Affinity,
							tagInfo.CDN,
						); err != nil {
							glog.Errorf("Can't create backend service %q: %+v", bsName, err)
							lock.Unlock()
							continue
						}

						glog.Infof("Updating URL map %q ...", c.Cloud.URLMap)
						if err := client.UpdateURLMap(
							c.Cloud.URLMap,
							bsName,
							tagInfo.Host,
							tagInfo.Path,
						); err != nil {
							glog.Errorf("Can't update URL map %q: %+v", c.Cloud.URLMap, err)
							lock.Unlock()
							continue
						}
					}

					isRunning = true
					glog.Infof("Watching service with tag [%q]", tag)
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
						if err := client.DetachEndpointsFromGroup(toRemove, negName); err != nil {
							glog.Errorf("Failed detaching endpoints from %q network endpoint group: %+v", negName, err)
						}
					}

					isRunning = false
				}
				lock.Unlock()
			case registry.CHANGED:
				lock.Lock()
				glog.Infof("Changed %q", tag)

				// validate if we've created the instance group for this service
				if !isRunning {
					glog.Warningf("Ignoring received event for service with tag %q because it's not running", tag)
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
					if err := client.DetachEndpointsFromGroup(toRemove, negName); err != nil {
						glog.Errorf("Failed detaching endpoints from %q network endpoint group: %+v", negName, err)
					}
				}

				// do we have new instances to add to the NEG?
				if len(toAdd) > 0 {
					if err := client.AttachEndpointsToGroup(toAdd, negName); err != nil {
						glog.Errorf("Failed adding endpoints to %q network endpoint group: %+v", negName, err)
					}
				}

				lock.Unlock()
			default:
				continue
			}
		case <-done:
			glog.Warningf("Received termination signal for service with tag %q", tag)
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
