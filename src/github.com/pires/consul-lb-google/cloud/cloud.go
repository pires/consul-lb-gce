package cloud

import (
	"fmt"
	"github.com/golang/glog"
	"github.com/pires/consul-lb-google/cloud/gce"
	"github.com/pires/consul-lb-google/util"
)

type Cloud interface {
	// CreateNetworkEndpointGroup creates an network endpoint group
	CreateNetworkEndpointGroup(groupName string) error

	AddHealthCheck(groupName, path string) error

	// Adds a set of endpoints to a network endpoint group
	AddEndpointsToNetworkEndpointGroup(endpoints []NetworkEndpoint, groupName string) error

	// Removes a set of endpoints from a network endpoint group
	RemoveEndpointsFromNetworkEndpointGroup(endpoints []NetworkEndpoint, groupName string) error

	// Creates backend service with network endpoint group as backend with affinity and cdn properties
	CreateBackendService(groupName, affinity string, cdn bool) error

	// Updates existing load-balancer related to a group
	UpdateUrlMap(urlMapName, groupName, host, path string) error
}

type NetworkEndpoint struct {
	Instance string
	Ip       string
	Port     string
}

type gceCloud struct {
	client *gce.GCEClient

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
		zones:  allowedZones,
	}, nil
}

func (c *gceCloud) CreateNetworkEndpointGroup(groupName string) error {
	for _, zone := range c.zones {
		finalGroupName := util.Zonify(zone, groupName)

		args := []string{"gcloud", "beta", "compute", "network-endpoint-groups", "create", finalGroupName, "--zone=" + zone, "--network=default", "--subnet=default", "--default-port=80"}

		if err := util.ExecCommand(args); err != nil && !util.IsAlreadyExistsError(err) {
			glog.Errorf("Failed creating network endpoint group [%s]. %s", finalGroupName, err)
			return err
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

func (c *gceCloud) AddEndpointsToNetworkEndpointGroup(endpoints []NetworkEndpoint, groupName string) error {
	glog.Infof("Adding %d network endpoints into network endpoint group [%s]", len(endpoints), groupName)

	for _, zone := range c.zones {
		finalGroupName := util.Zonify(zone, groupName)

		for _, endpoint := range endpoints {
			endpointSignature := fmt.Sprintf("instance=%s,ip=%s,port=%s", endpoint.Instance, endpoint.Ip, endpoint.Port)

			args := []string{"gcloud", "beta", "compute", "network-endpoint-groups", "update",
				finalGroupName, "--zone=" + zone, "--add-endpoint", endpointSignature}

			if err := util.ExecCommand(args); err != nil {
				glog.Errorf("Failed adding endpoint to network endpoint group [%s]. %s", finalGroupName, err)
			} else {
				glog.Infof("Added network endpoint [%s] into network endpoint group [%s]", endpointSignature, finalGroupName)
			}
		}
	}

	return nil
}

func (c *gceCloud) RemoveEndpointsFromNetworkEndpointGroup(endpoints []NetworkEndpoint, groupName string) error {
	return nil
}

func (c *gceCloud) CreateBackendService(groupName string, affinity string, cdn bool) error {
	for _, zone := range c.zones {
		err := c.client.CreateBackendService(groupName, zone, affinity, cdn)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *gceCloud) UpdateUrlMap(urlMapName, groupName string, host, path string) error {
	glog.Infof("Updating url map [%s].", urlMapName)

	for _, zone := range c.zones {
		err := c.client.UpdateUrlMap(urlMapName, util.Zonify(zone, groupName), host, path)
		if err != nil {
			return err
		}
	}

	glog.Infof("Updated url map [%s]", urlMapName)

	return nil
}
