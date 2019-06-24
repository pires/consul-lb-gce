package main

import (
	"flag"
	"fmt"
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
	Project           string
	Network           string
	AllowedZones      []string `toml:"allowed_zones"`
	UrlMap            string   `toml:"url_map"`
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
	go func(updates <-chan *registry.ServiceUpdate, done chan struct{}) {
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
					glog.Infof("Initializing service with tag [%s]..", update.Tag)

					// NOTE: Create necessary DNS record sets yourself.

					if err := client.CreateNetworkEndpointGroup(networkEndpointGroupName); err != nil {
						continue
					}

					if err := client.AddHealthCheck(networkEndpointGroupName, getHealthCheckPath(update.Tag)); err != nil {
						continue
					}

					if err = client.CreateBackendService(networkEndpointGroupName, tagInfo.Affinity, tagInfo.Cdn); err != nil {
						continue
					}

					if err := client.UpdateUrlMap(cfg.Cloud.UrlMap, networkEndpointGroupName, tagInfo.Host, tagInfo.Path); err != nil {
						continue
					}

					isRunning = true
					glog.Infof("Watching service with tag [%s].", update.Tag)
				}
				lock.Unlock()
			// todo(max): deal with delete branch
			//case registry.DELETED:
			//	lock.Lock()
			//	if isRunning {
			//		// remove everything
			//		// todo(max): remove usage of backend service in url_map
			//		// note: we can't remove url_map coz we didn't create it
			//		//if err := client.RemoveLoadBalancer(serviceName); err != nil {
			//		//	glog.Errorf("HUMAN INTERVENTION REQUIRED: There was an error while propagating network changes for service [%s] port [%s]. %s", serviceName, servicePort, err)
			//		//}
			//		if err := client.RemoveInstanceGroup(serviceName); err != nil {
			//			glog.Errorf("HUMAN INTERVENTION REQUIRED: There was an error while removing instance group for service [%s]. %s", serviceName, err)
			//		}
			//		glog.Infof("Stopped watching service [%s].", serviceName)
			//		// reset state
			//		serviceName = ""
			//		servicePort = ""
			//		isRunning = false
			//		instances = make(map[string]*registry.ServiceInstance)
			//	}
			//	lock.Unlock()
			case registry.CHANGED:
				lock.Lock()

				// validate if we've created the instance group for this service
				if !isRunning {
					glog.Warningf("Ignoring received event for service with tag [%s] because it's not running.", tag)
					lock.Unlock()
					break
				}

				var toAdd, toRemove []cloud.NetworkEndpoint

				// have all instances been removed?
				if len(update.ServiceInstances) == 0 {
					for _, v := range instances {
						toRemove = append(toRemove, cloud.NetworkEndpoint{
							Instance: util.NormalizeInstanceName(v.Host),
							Ip:       v.Address,
							Port:     v.Port,
						})
					}
				} else {
					// identify any deleted instances and remove from instance group
					if len(update.ServiceInstances) < len(instances) {
						glog.Warningf("Removing %d instances.", len(instances)-len(update.ServiceInstances))
						for k := range instances {
							if v, ok := update.ServiceInstances[k]; !ok {
								// need to split k because Consul stores FQDN
								toRemove = append(toRemove, cloud.NetworkEndpoint{
									Instance: strings.Split(v.Host, ".")[0],
									Ip:       v.Address,
									Port:     v.Port,
								})
								delete(instances, k)
								glog.Warningf("Removing instance [%s].", k)
							}
						}
					}

					// find new or changed instances and create or change accordingly in cloud
					for k, v := range update.ServiceInstances {
						if _, ok := instances[k]; !ok {
							// new instance, create
							glog.Warningf("Adding instance [%s].", k)
							instances[k] = v
							// mark as new instance for further processing
							// need to split k because Consul stores FQDN
							toAdd = append(toAdd, cloud.NetworkEndpoint{
								Instance: strings.Split(v.Host, ".")[0],
								Ip:       v.Address,
								Port:     v.Port,
							})
						}
					}

				}

				//do we have instances to remove from the instance group?
				if len(toRemove) > 0 {
					if err := client.RemoveEndpointsFromNetworkEndpointGroup(toRemove, networkEndpointGroupName); err != nil {
						glog.Errorf("Failed removing instances from network endpoint group [%s]. %s", networkEndpointGroupName, err)
					}
				}

				// do we have new instances to add to the instance group?
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
