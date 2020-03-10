package cloud

import (
	"sync"

	"github.com/dffrntmedia/consul-lb-gce/cloud/gce"
	"github.com/dffrntmedia/consul-lb-gce/util"
	"github.com/golang/glog"
)

type Cloud interface {
	// CreateNetworkEndpointGroup creates an network endpoint group
	CreateNetworkEndpointGroup(groupName string) error

	AddHealthCheck(groupName, path string) error

	// Adds a set of endpoints to a network endpoint group
	AddEndpointsToNetworkEndpointGroup(endpoints []gce.NetworkEndpoint, groupName string) error

	// Removes a set of endpoints from a network endpoint group
	RemoveEndpointsFromNetworkEndpointGroup(endpoints []gce.NetworkEndpoint, groupName string) error

	// Creates backend service with network endpoint group as backend with affinity and cdn properties
	CreateBackendService(groupName, affinity string, cdn bool) error

	// Updates existing load-balancer related to a group
	UpdateUrlMap(urlMapName, groupName, host, path string) error
}

type gceCloud struct {
	client *gce.GCEClient

	lock *sync.RWMutex

	// zones available to this project
	zones []string
}

func New(projectID string, network string, allowedZones []string) (Cloud, error) {
	c, err := gce.CreateGCECloud(projectID, network)
	if err != nil {
		return nil, err
	}

	return &gceCloud{
		client: c,
		lock:   &sync.RWMutex{},
		zones:  allowedZones,
	}, nil
}

func (c *gceCloud) CreateNetworkEndpointGroup(groupName string) error {
	for _, zone := range c.zones {
		finalGroupName := util.Zonify(zone, groupName)

		if err := c.client.CreateNetworkEndpointGroup(finalGroupName, zone); err != nil {
			glog.Errorf("Failed creating network endpoint group [%s]. %s", finalGroupName, err)
		}
	}

	glog.Infof("Created network endpoint group [%s]", groupName)

	return nil
}

func (c *gceCloud) AddHealthCheck(groupName, path string) error {
	if err := c.client.CreateHttpHealthCheck(groupName, path); err != nil {
		glog.Errorf("Failed creating health check. %s", err)
		return err
	}
	return nil
}

func (c *gceCloud) AddEndpointsToNetworkEndpointGroup(endpoints []gce.NetworkEndpoint, groupName string) error {
	glog.Infof("Adding %d network endpoints into network endpoint group [%s]", len(endpoints), groupName)

	for _, zone := range c.zones {
		finalGroupName := util.Zonify(zone, groupName)

		err := c.client.AttachNetworkEndpoints(finalGroupName, zone, endpoints)

		if err != nil {
			glog.Errorf("Failed adding endpoints to network endpoint group [%s]. %s", finalGroupName, err)
			return err
		}
	}

	return nil
}

func (c *gceCloud) RemoveEndpointsFromNetworkEndpointGroup(endpoints []gce.NetworkEndpoint, groupName string) error {
	glog.Infof("Removing %d network endpoints from network endpoint group [%s]", len(endpoints), groupName)

	for _, zone := range c.zones {
		finalGroupName := util.Zonify(zone, groupName)

		err := c.client.DetachNetworkEndpoints(finalGroupName, zone, endpoints)

		if err != nil {
			glog.Errorf("Failed removing endpoints from network endpoint group [%s]. %s", finalGroupName, err)
			return err
		}
	}

	return nil
}

func (c *gceCloud) CreateBackendService(groupName string, affinity string, cdn bool) error {
	for _, zone := range c.zones {
		err := c.client.CreateBackendService(groupName, zone, affinity, cdn)
		if err != nil {
			glog.Errorf("Failed creating backend service. %s", err)
			return err
		}
	}
	return nil
}

func (c *gceCloud) UpdateUrlMap(urlMapName, groupName string, host, path string) error {
	defer c.lock.Unlock()
	c.lock.Lock()

	glog.Infof("Updating url map [%s].", urlMapName)

	for _, zone := range c.zones {
		err := c.client.UpdateUrlMap(urlMapName, util.Zonify(zone, groupName), host, path)
		if err != nil {
			glog.Errorf("Failed updating url map [%s]. %s", urlMapName, err)
			return err
		}
	}

	glog.Infof("Updated url map [%s]", urlMapName)

	return nil
}
