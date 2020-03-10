package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/dffrntmedia/consul-lb-gce/cloud/gce"
	"github.com/dffrntmedia/consul-lb-gce/util"

	"github.com/BurntSushi/toml"
	"github.com/dffrntmedia/consul-lb-gce/cloud"
	"github.com/dffrntmedia/consul-lb-gce/registry"
	"github.com/dffrntmedia/consul-lb-gce/registry/consul"
	"github.com/golang/glog"
)

func main() {
	var configurationPath string
	flag.StringVar(&configurationPath, "config", "config.toml", "Configuration path")
	flag.Parse()
	if configurationPath == "" {
		glog.Fatal("Configuration path is required")
	}

	glog.Infof("Reading configuration from %s", configurationPath)
	var c configuration
	if _, err := toml.DecodeFile(configurationPath, &c); err != nil {
		glog.Fatal(err)
	}

	tagParser := newTagParser(c.TagParser.TagPrefix)

	glog.Infof("Initializing cloud client [Project ID: %s, Network: %s, Allowed Zones: %v]", c.Cloud.Project, c.Cloud.Network, c.Cloud.AllowedZones)
	client, err := cloud.New(c.Cloud.Project, c.Cloud.Network, c.Cloud.AllowedZones)
	if err != nil {
		glog.Fatal(err)
	}

	glog.Infof("Connecting to Consul at %s", c.Consul.URL)
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

	glog.Info("Terminating all pending jobs..")
	close(done)
	glog.Info("Terminated")
}

// handleService handles service updates in a consistent way.
// It will run until service is deleted or done is closed.
func handleService(c *configuration, client cloud.Cloud, tagParser *TagParser, tag string, updates <-chan *registry.ServiceUpdate, wg sync.WaitGroup, done chan struct{}) {
	tagInfo, err := tagParser.Parse(tag)
	if err != nil {
		glog.Errorf("Failed parsing tag [%s]. %s", tag, err)
		return
	}

	networkEndpointGroupName := getNetworkEndpointGroupName(tagInfo)

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
					glog.Infof("Initializing service with tag [%s]..", tag)

					// NOTE: Create necessary DNS record sets yourself.

					if err := client.CreateNetworkEndpointGroup(networkEndpointGroupName); err != nil {
						lock.Unlock()
						continue
					}

					if err := client.AddHealthCheck(networkEndpointGroupName, getHealthCheckPath(c, tag)); err != nil {
						lock.Unlock()
						continue
					}

					if err = client.CreateBackendService(networkEndpointGroupName, tagInfo.Affinity, tagInfo.CDN); err != nil {
						lock.Unlock()
						continue
					}

					if err := client.UpdateUrlMap(c.Cloud.URLMap, networkEndpointGroupName, tagInfo.Host, tagInfo.Path); err != nil {
						lock.Unlock()
						continue
					}

					isRunning = true
					glog.Infof("Watching service with tag [%s].", tag)
				}
				lock.Unlock()
			case registry.DELETED:
				lock.Lock()
				if isRunning {
					var toRemove []gce.NetworkEndpoint

					for k, instance := range instances {
						toRemove = append(toRemove, gce.NetworkEndpoint{
							Instance: util.NormalizeInstanceName(instance.Host),
							Ip:       instance.Address,
							Port:     instance.Port,
						})
						delete(instances, k)
					}

					//do we have instances to remove from the NEG?
					if len(toRemove) > 0 {
						if err := client.RemoveEndpointsFromNetworkEndpointGroup(toRemove, networkEndpointGroupName); err != nil {
							glog.Errorf("Failed removing instances from network endpoint group [%s]. %s", networkEndpointGroupName, err)
						}
					}

					isRunning = false
				}
				lock.Unlock()
			case registry.CHANGED:
				lock.Lock()

				// validate if we've created the instance group for this service
				if !isRunning {
					glog.Warningf("Ignoring received event for service with tag [%s] because it's not running.", tag)
					lock.Unlock()
					break
				}

				var toAdd, toRemove []gce.NetworkEndpoint

				// finding instances to remove
				for k, instance := range instances {
					// Doesn't instance exist in update?
					if _, ok := update.ServiceInstances[k]; !ok {
						toRemove = append(toRemove, gce.NetworkEndpoint{
							Instance: util.NormalizeInstanceName(instance.Host),
							Ip:       instance.Address,
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
							Instance: util.NormalizeInstanceName(instance.Host),
							Ip:       instance.Address,
							Port:     instance.Port,
						})
						instances[k] = instance
					}
				}

				//do we have instances to remove from the NEG?
				if len(toRemove) > 0 {
					if err := client.RemoveEndpointsFromNetworkEndpointGroup(toRemove, networkEndpointGroupName); err != nil {
						glog.Errorf("Failed removing instances from network endpoint group [%s]. %s", networkEndpointGroupName, err)
					}
				}

				// do we have new instances to add to the NEG?
				if len(toAdd) > 0 {
					if err := client.AddEndpointsToNetworkEndpointGroup(toAdd, networkEndpointGroupName); err != nil {
						glog.Errorf("Failed adding instances to network endpoint group [%s]. %s", networkEndpointGroupName, err)
					}
				}

				lock.Unlock()
			default:
				continue
			}
		case <-done:
			glog.Warningf("Received termination signal for service with tag [%s]", tag)
			wg.Done()
			return
		}
	}
}

// Handles updates for all services and dispatch them between specific per service handlers.
func handleServices(c *configuration, client cloud.Cloud, tagParser *TagParser, updates <-chan *registry.ServiceUpdate, done chan struct{}) {
	var wg sync.WaitGroup
	handlers := make(map[string]chan *registry.ServiceUpdate)

	for {
		select {
		case update := <-updates:
			// is there a handler for updated service?
			if handler, ok := handlers[update.ServiceName]; !ok {
				// no so provision handler
				handler = make(chan *registry.ServiceUpdate)
				handlers[update.ServiceName] = handler
				// start handler in its own goroutine
				wg.Add(1)
				go handleService(c, client, tagParser, update.Tag, handler, wg, done)
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

func getNetworkEndpointGroupName(tagInfo *TagInfo) string {
	return fmt.Sprintf("neg-%s-%s-%s", getCDNType(tagInfo.CDN), tagInfo.Affinity, normalizeHost(tagInfo.Host))
}

func getCDNType(cdn bool) string {
	if cdn {
		return "cdn"
	}
	return "nocdn"
}

func normalizeHost(host string) string {
	return strings.Replace(host, ".", "-", -1)
}

func getHealthCheckPath(c *configuration, tag string) string {
	if healthCheckPath, ok := c.Consul.HealthChecksPaths[tag]; ok {
		return healthCheckPath
	}

	return ""
}
