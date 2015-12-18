package gce

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"k8s.io/kubernetes/pkg/util/wait"
)

const (
	gceAffinityTypeNone = "NONE"
	// AffinityTypeClientIP - affinity based on Client IP.
	gceAffinityTypeClientIP = "CLIENT_IP"
	// AffinityTypeClientIPProto - affinity based on Client IP and port.
	gceAffinityTypeClientIPProto = "CLIENT_IP_PROTO"

	operationPollInterval        = 3 * time.Second
	operationPollTimeoutDuration = 30 * time.Minute
)

var (
	ErrInstanceNotFound = errors.New("Instance not found")
)

// GCEClient is a placeholder for GCE stuff.
type GCEClient struct {
	service    *compute.Service
	projectID  string
	networkURL string
}

// CreateGCECloud creates a new instance of GCECloud.
func CreateGCECloud(project string, network string) (*GCEClient, error) {
	// Use oauth2.NoContext if there isn't a good context to pass in.
	ctx := context.TODO()

	client, err := google.DefaultClient(ctx, compute.ComputeScope)
	if err != nil {
		return nil, err
	}
	svc, err := compute.New(client)
	if err != nil {
		return nil, err
	}

	// TODO validate project and network exist

	return &GCEClient{
		service:    svc,
		projectID:  project,
		networkURL: makeNetworkURL(project, network),
	}, nil
}

// ListInstancesInZone returns all instances in a zone
func (gce *GCEClient) ListInstancesInZone(zone string) (*compute.InstanceList, error) {
	return gce.service.Instances.List(gce.projectID, zone).Do()
}

// CreateInstanceGroupForZone creates an instance group with the given instances for the given zone.
func (gce *GCEClient) CreateInstanceGroupForZone(name string, zone string, ports map[string]int64) error {
	// defined NamedPorts
	namedPorts := make([]*compute.NamedPort, len(ports))
	for name, port := range ports {
		namedPorts = append(namedPorts, &compute.NamedPort{
			Name: name,
			Port: port,
		})
	}

	// define InstanceGroup
	ig := &compute.InstanceGroup{
		Name:       name,
		NamedPorts: namedPorts,
		Network:    gce.networkURL}

	op, err := gce.service.InstanceGroups.Insert(gce.projectID, zone, ig).Do()
	if err != nil {
		return err
	}
	if err = gce.waitForZoneOp(op, zone); err != nil {
		return err
	}
	return nil
}

// DeleteInstanceGroupForZone deletes an instance group for the given zone.
func (gce *GCEClient) DeleteInstanceGroupForZone(name string, zone string) error {
	op, err := gce.service.InstanceGroups.Delete(
		gce.projectID, zone, name).Do()
	if err != nil {
		return err
	}
	return gce.waitForZoneOp(op, zone)
}

// ListInstanceGroupsForzone lists all InstanceGroups in the project for the given zone.
func (gce *GCEClient) ListInstanceGroupsForZone(zone string) (*compute.InstanceGroupList, error) {
	return gce.service.InstanceGroups.List(gce.projectID, zone).Do()
}

// ListInstancesInInstanceGroupForZone lists all the instances in a given instance group for the given zone.
func (gce *GCEClient) ListInstancesInInstanceGroupForZone(name string, zone string) (*compute.InstanceGroupsListInstances, error) {
	return gce.service.InstanceGroups.ListInstances(
		gce.projectID, zone, name,
		&compute.InstanceGroupsListInstancesRequest{}).Do()
}

// Return the instances matching the relevant name and zone
func (gce *GCEClient) GetInstanceByNameAndZone(name string, zone string) (*compute.Instance, error) {
	name = canonicalizeInstanceName(name)
	res, err := gce.service.Instances.Get(gce.projectID, zone, name).Do()
	if err != nil {
		glog.Errorf("Failed to retrieve TargetInstance resource for instance: %s", name)
		if apiErr, ok := err.(*googleapi.Error); ok && apiErr.Code == http.StatusNotFound {
			return nil, ErrInstanceNotFound
		}
		return nil, err
	}
	return res, nil
}

// AddInstancesToInstanceGroupForZone adds the given instances to the given instance group for the given zone.
func (gce *GCEClient) AddInstancesToInstanceGroup(name string, instanceNames []string, zone string) error {
	if len(instanceNames) == 0 {
		return nil
	}
	// Adding the same instance twice will result in a 4xx error
	instances := []*compute.InstanceReference{}
	for _, ins := range instanceNames {
		instances = append(instances, &compute.InstanceReference{Instance: makeHostURL(gce.projectID, zone, ins)})
	}
	op, err := gce.service.InstanceGroups.AddInstances(
		gce.projectID, zone, name,
		&compute.InstanceGroupsAddInstancesRequest{
			Instances: instances,
		}).Do()

	if err != nil {
		return err
	}
	return gce.waitForZoneOp(op, zone)
}

// RemoveInstancesFromInstanceGroupForZone removes the given instances from the instance group for the given zone.
func (gce *GCEClient) RemoveInstancesFromInstanceGroup(name string, instanceNames []string, zone string) error {
	if len(instanceNames) == 0 {
		return nil
	}
	instances := []*compute.InstanceReference{}
	for _, ins := range instanceNames {
		instanceLink := makeHostURL(gce.projectID, zone, ins)
		instances = append(instances, &compute.InstanceReference{Instance: instanceLink})
	}
	op, err := gce.service.InstanceGroups.RemoveInstances(
		gce.projectID, zone, name,
		&compute.InstanceGroupsRemoveInstancesRequest{
			Instances: instances,
		}).Do()

	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForZoneOp(op, zone)
}

// SetPortToInstanceGroupForZone makes sure there's one single port to the given instance group for the given zone.
func (gce *GCEClient) SetPortToInstanceGroupForZone(name string, port int64, zone string) error {
	var namedPorts []*compute.NamedPort
	namedPorts = append(namedPorts, &compute.NamedPort{Name: "service-port", Port: port})
	op, err := gce.service.InstanceGroups.SetNamedPorts(
		gce.projectID, zone, name,
		&compute.InstanceGroupsSetNamedPortsRequest{
			NamedPorts: namedPorts}).Do()
	if err != nil {
		return err
	}
	if err = gce.waitForZoneOp(op, zone); err != nil {
		return err
	}
	return nil
}

// GetInstanceGroupForZone returns an instance group by name for zone.
func (gce *GCEClient) GetInstanceGroupForZone(name string, zone string) (*compute.InstanceGroup, error) {
	return gce.service.InstanceGroups.Get(gce.projectID, zone, name).Do()
}

// GetAvailableZones returns all available zones for this project
func (gce *GCEClient) GetAvailableZones() (*compute.ZoneList, error) {
	return gce.service.Zones.List(gce.projectID).Do()
}

func createLoadBalancer() {
	/*

		// TODO create unmanaged instance group
		POST https://www.googleapis.com/compute/v1/projects/littlebits-electronics/zones/us-east1-d/instanceGroups
		{
		  "name": "ig-web",
		  "network": "https://www.googleapis.com/compute/v1/projects/littlebits-electronics/global/networks/nomad-network",
		  "namedPorts": [
		    {
		      "name": "http",
		      "port": 11080
		    }
		  ]
		}

		// TODO add instances to created instance group
		POST https://www.googleapis.com/compute/v1/projects/littlebits-electronics/zones/us-east1-d/instanceGroups/ig-web/addInstances
		{
		  "instances": [
		    {
		      "instance": "https://www.googleapis.com/compute/v1/projects/littlebits-electronics/zones/us-east1-d/instances/client-us-01"
		    }
		  ]
		}

		// TODO create firewall rule to allow service traffic to instances
		POST https://www.googleapis.com/compute/v1/projects/littlebits-electronics/global/firewalls
		{
		  "kind": "compute#firewall",
		  "name": "web-http2",
		  "allowed": [
		    {
		      "IPProtocol": "tcp",
		      "ports": [
			"11080"
		      ]
		    }
		  ],
		  "network": "https://www.googleapis.com/compute/v1/projects/littlebits-electronics/global/networks/nomad-network",
		  "sourceRanges": [
		    "0.0.0.0/0"
		  ]
		}

		// TODO create load-balancer

		// TODO create global forwarding rule for HTTP
		POST https://www.googleapis.com/compute/beta/projects/littlebits-electronics/global/forwardingRules
		{
		  "kind": "compute#forwardingRule",
		  "IPProtocol": "TCP",
		  "portRange": "80",
		  "target": "https://www.googleapis.com/compute/beta/projects/littlebits-electronics/global/targetHttpProxies/web-target-1",
		  "name": "xpto"
		}

		// TODO create backend service

	*/

}

// helper methods

// Take a GCE instance 'hostname' and break it down to something that can be fed
// to the GCE API client library.  Basically this means reducing 'kubernetes-
// minion-2.c.my-proj.internal' to 'kubernetes-minion-2' if necessary.
func canonicalizeInstanceName(name string) string {
	ix := strings.Index(name, ".")
	if ix != -1 {
		name = name[:ix]
	}
	return name
}

func makeNetworkURL(project string, network string) string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/networks/%s", project, network)
}

func makeHostURL(projectID, zone, host string) string {
	host = canonicalizeInstanceName(host)
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/instances/%s",
		projectID, zone, host)
}

func isHTTPErrorCode(err error, code int) bool {
	apiErr, ok := err.(*googleapi.Error)
	return ok && apiErr.Code == code
}

func waitForOp(op *compute.Operation, getOperation func(operationName string) (*compute.Operation, error)) error {
	if op == nil {
		return fmt.Errorf("operation must not be nil")
	}

	if opIsDone(op) {
		return getErrorFromOp(op)
	}

	opName := op.Name
	return wait.Poll(operationPollInterval, operationPollTimeoutDuration, func() (bool, error) {
		pollOp, err := getOperation(opName)
		if err != nil {
			glog.Warningf("GCE poll operation failed: %v", err)
		}
		return opIsDone(pollOp), getErrorFromOp(pollOp)
	})
}

func opIsDone(op *compute.Operation) bool {
	return op != nil && op.Status == "DONE"
}

func getErrorFromOp(op *compute.Operation) error {
	if op != nil && op.Error != nil && len(op.Error.Errors) > 0 {
		err := &googleapi.Error{
			Code:    int(op.HttpErrorStatusCode),
			Message: op.Error.Errors[0].Message,
		}
		glog.Errorf("GCE operation failed: %v", err)
		return err
	}

	return nil
}

func (gce *GCEClient) waitForGlobalOp(op *compute.Operation) error {
	return waitForOp(op, func(operationName string) (*compute.Operation, error) {
		return gce.service.GlobalOperations.Get(gce.projectID, operationName).Do()
	})
}

func (gce *GCEClient) waitForRegionOp(op *compute.Operation, region string) error {
	return waitForOp(op, func(operationName string) (*compute.Operation, error) {
		return gce.service.RegionOperations.Get(gce.projectID, region, operationName).Do()
	})
}

func (gce *GCEClient) waitForZoneOp(op *compute.Operation, zone string) error {
	return waitForOp(op, func(operationName string) (*compute.Operation, error) {
		return gce.service.ZoneOperations.Get(gce.projectID, zone, operationName).Do()
	})
}
