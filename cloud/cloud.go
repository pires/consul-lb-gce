package cloud

import (
	"sync"

	"github.com/dffrntmedia/consul-lb-gce/cloud/gce"
)

type Cloud interface {
	CreateNetworkEndpointGroup(negName string) error
	CreateHealthCheck(hcName, path string) error
	AttachEndpointsToGroup(endpoints []gce.NetworkEndpoint, negName string) error
	DetachEndpointsFromGroup(endpoints []gce.NetworkEndpoint, negName string) error
	CreateBackendService(bsName string, negName string, hcName string, affinity string, cdn bool) error
	UpdateURLMap(urlMapName, negName, host, path string) error
}

type gceCloud struct {
	client *gce.Client
	lock   *sync.RWMutex
	zone   string
}

// New creates new instacne of Cloud
func New(projectID string, network string, zone string) (Cloud, error) {
	c, err := gce.New(projectID, network)
	if err != nil {
		return nil, err
	}

	return &gceCloud{
		client: c,
		lock:   &sync.RWMutex{},
		zone:   zone,
	}, nil
}

func (c *gceCloud) CreateNetworkEndpointGroup(negName string) error {
	return c.client.CreateNetworkEndpointGroup(negName, c.zone)
}

func (c *gceCloud) CreateHealthCheck(hcName, path string) error {
	return c.client.CreateHTTPHealthCheck(hcName, path)
}

func (c *gceCloud) AttachEndpointsToGroup(endpoints []gce.NetworkEndpoint, negName string) error {
	return c.client.AttachNetworkEndpoints(negName, c.zone, endpoints)
}

func (c *gceCloud) DetachEndpointsFromGroup(endpoints []gce.NetworkEndpoint, negName string) error {
	return c.client.DetachNetworkEndpoints(negName, c.zone, endpoints)
}

func (c *gceCloud) CreateBackendService(bsName string, negName string, hcName string, affinity string, cdn bool) error {
	return c.client.CreateBackendService(bsName, negName, hcName, c.zone, affinity, cdn)
}

func (c *gceCloud) UpdateURLMap(urlMapName string, bsName string, host string, path string) error {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.UpdateURLMap(urlMapName, bsName, host, path)
}
