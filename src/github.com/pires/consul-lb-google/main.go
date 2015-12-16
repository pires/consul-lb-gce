package main

import (
	"flag"
	"os"
	"os/signal"

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
	glog.Infof("Connection to Consul at %s..", *consulAddr)
	r, err := consul.NewRegistry(&registry.Config{
		Addresses: []string{*consulAddr},
	})
	if err != nil {
		panic(err)
	}

	glog.Info("Initiating registry..")
	updates := make(chan *registry.ServiceUpdate, 16)
	done := make(chan struct{})
	go r.Run(updates, done)

	glog.Info("Waiting for service updates..")
	go func(updates <-chan *registry.ServiceUpdate, done chan struct{}) {
		for {
			select {
			case update := <-updates:
				glog.Infof("UPDATE: %s", update.UpdateType)
			case <-done:
				return
			}
		}
	}(updates, done)

	// wait for Ctrl-c to stop server
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	<-c
	close(done)

	glog.Info("Terminated")
}
