package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
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
	var servicePort string
	isRunning := false
	instances := make(map[string]*registry.ServiceInstance)

	for {
		select {
		case update := <-updates:
			tagInfo, parseErr := tagParser.Parse(update.Tag)

			if parseErr != nil {
				glog.Errorf("There was an error while parsing service tag [%s]. %s", update.Tag, err)
			}

			instanceGroupName := getInstanceGroupName(tagInfo)

			switch update.UpdateType {
			case registry.NEW:
				lock.Lock()
				if !isRunning {
					glog.Infof("Initializing service [%s]..", update.ServiceName)

					if err := client.CreateInstanceGroup(instanceGroupName); err != nil {
						glog.Errorf("There was an error while initializing service [%s]. %s", update.ServiceName, err)
					} else {
						serviceName = update.ServiceName
						isRunning = true
						glog.Infof("Watching service [%s].", serviceName)
					}
				}
				lock.Unlock()
			case registry.DELETED:
				lock.Lock()
				if isRunning {
					// remove everything
					if err := client.RemoveLoadBalancer(serviceName); err != nil {
						glog.Errorf("HUMAN INTERVENTION REQUIRED: There was an error while propagating network changes for service [%s] port [%s]. %s", serviceName, servicePort, err)
					}
					if err := client.RemoveInstanceGroup(serviceName); err != nil {
						glog.Errorf("HUMAN INTERVENTION REQUIRED: There was an error while removing instance group for service [%s]. %s", serviceName, err)
					}
					glog.Infof("Stopped watching service [%s].", serviceName)
					// reset state
					serviceName = ""
					servicePort = ""
					isRunning = false
					instances = make(map[string]*registry.ServiceInstance)
				}
				lock.Unlock()
			case registry.CHANGED:
				lock.Lock()

				// validate if we've created the instance group for this service
				if !isRunning {
					glog.Warningf("Ignoring received event for unwatched service [%s].", serviceName)
					lock.Unlock()
					break
				}

				currentPort := servicePort
				var toAdd, toRemove []string

				// have all instances been removed?
				if len(update.ServiceInstances) == 0 {
					for k := range instances {
						// need to split k because Consul stores FQDN
						toRemove = append(toRemove, strings.Split(k, ".")[0])
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
					}

					// find new or changed instances and create or change accordingly in cloud
					for k, v := range update.ServiceInstances {
						if instance, ok := instances[k]; !ok {
							// new instance, create
							glog.Warningf("Adding instance [%s].", k)
							instances[k] = v
							// mark as new instance for further processing
							// need to split k because Consul stores FQDN
							toAdd = append(toAdd, strings.Split(k, ".")[0])

							// check if service port is new
							if currentPort != v.Port {
								glog.Infof("Service has new port [%s]", v.Port)
								currentPort = v.Port
							}
						} else {
							// already exists, compare instance with v
							if instance.Port != v.Port { // compare port
								if currentPort != v.Port {
									glog.Infof("Service has new port [%s]", v.Port)
									currentPort = v.Port
								}
							}
						}
					}

				}

				// do we have instances to remove from the instance group?
				if len(toRemove) > 0 {
					if err := client.RemoveInstancesFromInstanceGroup(toRemove, serviceName); err != nil {
						glog.Errorf("There was an error while removing instances from instance group [%s]. %s", serviceName, err)
					}
				}

				// do we have new instances to add to the instance group?
				if len(toAdd) > 0 {
					if err := client.AddInstancesToInstanceGroup(toAdd, instanceGroupName); err != nil {
						glog.Errorf("There was an error while adding instances to instance group [%s]. %s", instanceGroupName, err)
					}
				}

				glog.Infof("Service %s ports: old %s, new %s", update.ServiceName, servicePort, currentPort)
				// todo(max): to take only identical ports (temporal)
				// do we need to change instance group port?
				if servicePort == "" && servicePort != currentPort {
					if port, err := strconv.ParseInt(currentPort, 10, 64); err != nil {
						glog.Errorf("There was an error while setting service [%s] port. %s", serviceName, err)
					} else {
						if err := client.SetPortForInstanceGroup(port, instanceGroupName); err != nil {
							glog.Errorf("HUMAN INTERVENTION REQUIRED: There was an error while setting service [%s] port [%s]. %s", serviceName, currentPort, err)
						}
						servicePort = currentPort

						finalhealthCheckPath := ""

						if healthCheckPath, ok := cfg.Consul.HealthChecksPaths[update.Tag]; ok {
							finalhealthCheckPath = healthCheckPath
						}

						// propagate networking changes
						if err := client.CreateOrUpdateLoadBalancer(instanceGroupName, servicePort, finalhealthCheckPath); err != nil {
							glog.Errorf("HUMAN INTERVENTION REQUIRED: There was an error while propagating network changes for service [%s] port [%s]. %s", serviceName, servicePort, err)
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

func getInstanceGroupName(tagInfo tagparser.TagInfo) string {
	return fmt.Sprintf("proxy-%s-%s-%s", getCdnType(tagInfo.Cdn), tagInfo.Affinity, normalizeHost(tagInfo.Host))
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
