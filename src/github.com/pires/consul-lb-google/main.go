package main

import (
	"flag"
	"fmt"
	"github.com/pires/consul-lb-google/cloud/gce"
	"github.com/pires/consul-lb-google/util"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/golang/glog"
	"github.com/pires/consul-lb-google/cloud"
	"github.com/pires/consul-lb-google/registry"
	"github.com/pires/consul-lb-google/registry/consul"
	"github.com/pires/consul-lb-google/tagparser"
)

var (
	config = flag.String("config", "config.toml", "Path to the configuration file")

	cfg configuration

	client cloud.Cloud

	tagParser tagparser.TagParser

	err error
)

type tagParserConfiguration struct {
	TagPrefix string `toml:"tag_prefix"`
}

type consulConfiguration struct {
	Url         string
	TagsToWatch []string `toml:"tags_to_watch"`
	// NOTE: Since we can't retrieve health checks definitions from Consul we must explicitly define them in configuration
	HealthChecksPaths map[string]string `toml:"health_checks_paths"`
}

type cloudConfiguration struct {
	Project      string
	Network      string
	AllowedZones []string `toml:"allowed_zones"`
	UrlMap       string   `toml:"url_map"`
}

type configuration struct {
	TagParser tagParserConfiguration
	Consul    consulConfiguration
	Cloud     cloudConfiguration
}

func main() {
	flag.Parse()

	glog.Info("Starting..")

	// read configuration
	if _, err := toml.DecodeFile(*config, &cfg); err != nil {
		panic(err)
	}

	tagParser = tagparser.New(cfg.TagParser.TagPrefix)

	// provision cloud client
	glog.Infof("Initializing cloud client [Project ID: %s, Network: %s, Allowed Zones: %#v]..", cfg.Cloud.Project, cfg.Cloud.Network, cfg.Cloud.AllowedZones)
	client, err = cloud.New(cfg.Cloud.Project, cfg.Cloud.Network, cfg.Cloud.AllowedZones)
	if err != nil {
		panic(err)
	}

	// connect to Consul
	glog.Infof("Connecting to Consul at %s..", cfg.Consul.Url)
	r, err := consul.NewRegistry(&registry.Config{
		Addresses:   []string{cfg.Consul.Url},
		TagsToWatch: cfg.Consul.TagsToWatch,
	})
	if err != nil {
		panic(err)
	}

	glog.Info("Initiating registry..")
	updates := make(chan *registry.ServiceUpdate)
	done := make(chan struct{})
	// register for service updates
	go r.Run(updates, done)

	glog.Info("Waiting for service updates..")
	go handleServices(updates, done)

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
func handleService(tag string, updates <-chan *registry.ServiceUpdate, wg sync.WaitGroup, done chan struct{}) {
	tagInfo, err := tagParser.Parse(tag)

	if err != nil {
		glog.Errorf("Failed parsing tag [%s]. %s", tag, err)
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

					if err := client.AddHealthCheck(networkEndpointGroupName, getHealthCheckPath(tag)); err != nil {
						lock.Unlock()
						continue
					}

					if err = client.CreateBackendService(networkEndpointGroupName, tagInfo.Affinity, tagInfo.Cdn); err != nil {
						lock.Unlock()
						continue
					}

					if err := client.UpdateUrlMap(cfg.Cloud.UrlMap, networkEndpointGroupName, tagInfo.Host, tagInfo.Path); err != nil {
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
func handleServices(updates <-chan *registry.ServiceUpdate, done chan struct{}) {
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
				go handleService(update.Tag, handler, wg, done)
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

func getNetworkEndpointGroupName(tagInfo tagparser.TagInfo) string {
	return fmt.Sprintf("neg-%s-%s-%s", getCdnType(tagInfo.Cdn), tagInfo.Affinity, normalizeHost(tagInfo.Host))
}

func getCdnType(cdn bool) string {
	if cdn {
		return "cdn"
	} else {
		return "nocdn"
	}
}

func normalizeHost(host string) string {
	return strings.Replace(host, ".", "-", -1)
}

func getHealthCheckPath(tag string) string {
	if healthCheckPath, ok := cfg.Consul.HealthChecksPaths[tag]; ok {
		return healthCheckPath
	}

	return ""
}
