package cloud

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/golang/glog"
	"github.com/pires/consul-lb-google/cloud/gce"
)

var (
	ErrCantCreateInstanceGroup     = errors.New("Can't create instance group")
	ErrCantRemoveInstanceGroup     = errors.New("Can't remove instance group")
	ErrUnknownInstanceGroup        = errors.New("Unknown instance group")
	ErrUnknownInstanceInGroup      = errors.New("Unknown instance in instance group")
	ErrCantSetPortForInstanceGroup = errors.New("Can't set port for instance group")
)

type Cloud interface {
	CreateDnsRecordSet(managedZone, globalAddressName, host string) error

	// CreateNetworkEndpointGroup creates an network endpoint group
	CreateNetworkEndpointGroup(groupName string) error

	AddHealthCheck(groupName, path string) error

	// AddEndpointsToNetworkEndpointGroup adds a set of endpoints to an network endpoint group
	AddEndpointsToNetworkEndpointGroup(endpoints []NetworkEndpoint, groupName string) error

	// RemoveEndpointsFromNetworkEndpointGroup removes a set of endpoints from an network endpoint group
	RemoveEndpointsFromNetworkEndpointGroup(endpoints []NetworkEndpoint, groupName string) error

	CreateBackendServiceWithNetworkEndpointGroup(groupName string) error

	// UpdateLoadBalancer updates existing load-balancer related to an instance group
	UpdateLoadBalancer(urlMapName, groupName, host, path string) error
}

type NetworkEndpoint struct {
	Instance string
	Ip       string
	Port     string
}

type instanceGroup struct {
	// name of the instance group
	name string
	// map of instances in the instance group
	instances map[string]string
}

type gceCloud struct {
	// GCE client
	client *gce.GCEClient

	// zones available to this project
	zones []string

	// one instance group identifier represents n instance groups, one per available zone
	// e.g. groups := instanceGroups["myIG"]["europe-west1-d"]
	instanceGroups map[string]map[string]*instanceGroup
}

func New(projectID string, network string, allowedZones []string) (Cloud, error) {
	// try and provision GCE client
	c, err := gce.CreateGCECloud(projectID, network)
	if err != nil {
		return nil, err
	}

	return &gceCloud{
		client:         c,
		zones:          allowedZones,
		instanceGroups: make(map[string]map[string]*instanceGroup),
	}, nil
}

func (c *gceCloud) AddEndpointsToNetworkEndpointGroup(endpoints []NetworkEndpoint, groupName string) error {
	glog.Infof("Adding %d network endpoints into network endpoint group [%s]", len(endpoints), groupName)

	for _, zone := range c.zones {
		finalGroupName := zonify(zone, groupName)

		for _, endpoint := range endpoints {
			cmd := exec.Command("gcloud", "beta", "compute", "network-endpoint-groups", "update",
				finalGroupName, "--zone="+zone, "--add-endpoint", fmt.Sprintf("instance=%s,ip=%s,port=%s", endpoint.Instance, endpoint.Ip, endpoint.Port))

			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err != nil {
				glog.Errorf("Failed adding endpoint to network endpoint group [%s]. %s", finalGroupName, stderr.String())
			}
		}
	}

	glog.Infof("Added %d network endpoints into network endpoint group [%s]", len(endpoints), groupName)

	return nil
}

func (c *gceCloud) RemoveEndpointsFromNetworkEndpointGroup(endpoints []NetworkEndpoint, groupName string) error {
	return nil
}

func (c *gceCloud) CreateDnsRecordSet(managedZone, globalAddressName, host string) error {
	// Possible error: 'googleapi: Error 403: Request had insufficient authentication scopes., forbidden'
	if err := c.client.AddDnsRecordSet(managedZone, globalAddressName, host); err != nil {
		return err
	}
	glog.Infof("Created DNS record set successfully [%s] ", host)
	return nil
}

func (c *gceCloud) CreateNetworkEndpointGroup(groupName string) error {
	for _, zone := range c.zones {
		finalGroupName := zonify(zone, groupName)
		cmd := exec.Command("gcloud", "beta", "compute", "network-endpoint-groups", "create", finalGroupName, "--zone="+zone, "--network=default", "--subnet=default", "--default-port=80")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			if strings.Contains(stderr.String(), "already exists") {
				continue
			} else {
				glog.Errorf("Failed creating of network endpoint group [%s]. %s", finalGroupName, stderr.String())
				return err
			}
		}
	}

	glog.Infof("Created network endpoint group [%s] successfully", groupName)

	// todo(max): add health check

	return nil
}

func (c *gceCloud) CreateBackendServiceWithNetworkEndpointGroup(groupName string) error {
	for _, zone := range c.zones {
		finalGroupName := zonify(zone, groupName)
		err := c.client.CreateBackendService(finalGroupName, groupName, zone)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *gceCloud) UpdateLoadBalancer(urlMapName, groupName string, host, path string) error {
	glog.Infof("Updating load-balancer for [%s].", groupName)
	for _, zone := range c.zones {
		err := c.client.UpdateLoadBalancer(urlMapName, zonify(zone, groupName), host, path)
		if err != nil {
			return err
		}
	}
	glog.Infof("Load-balancer [%s] updated successfully.", groupName)
	return nil
}

func (c *gceCloud) AddHealthCheck(groupName, path string) error {
	return c.client.CreateHttpHealthCheck(groupName, path)
}

//zonify takes a specified name and prepends a specified zone plus an hyphen
// e.g. zone == "us-east1-d" && name == "myname", returns "us-east1-d-myname"
func zonify(zone string, name string) string {
	return strings.Join([]string{zone, name}, "-")
}

// unzonify takes a specified supposedly zonified name and removes the zone prefix.
// e.g. name == "us-east1-d-myname" && zone == "us-east1-d", returns "myname"
func unzonify(name string, zone string) string {
	return strings.TrimPrefix(name, zone)
}
