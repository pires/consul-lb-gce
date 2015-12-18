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

// CreateFirewall creates a global firewall rule
func (gce *GCEClient) CreateFirewall(name string, allowedPorts []string) error {
	fwName := makeFirewallName(name)
	firewall, err := gce.makeFirewallObject(fwName, allowedPorts)
	if err != nil {
		return err
	}
	op, err := gce.service.Firewalls.Insert(gce.projectID, firewall).Do()
	if err != nil && !isHTTPErrorCode(err, http.StatusConflict) {
		return err
	}
	if op != nil {
		err = gce.waitForGlobalOp(op)
		if err != nil && !isHTTPErrorCode(err, http.StatusConflict) {
			return err
		}
	}
	return nil
}

// UpdateFirewall updates a global firewall rule
func (gce *GCEClient) UpdateFirewall(name string, allowedPorts []string) error {
	fwName := makeFirewallName(name)
	firewall, err := gce.makeFirewallObject(fwName, allowedPorts)
	if err != nil {
		return err
	}
	op, err := gce.service.Firewalls.Update(gce.projectID, fwName, firewall).Do()
	if err != nil && !isHTTPErrorCode(err, http.StatusConflict) {
		return err
	}
	if op != nil {
		err = gce.waitForGlobalOp(op)
		if err != nil {
			return err
		}
	}
	return nil
}

// RemoveFirewall removes a global firewall rule
func (gce *GCEClient) RemoveFirewall(name string) error {
	fwName := makeFirewallName(name)
	op, err := gce.service.Firewalls.Delete(gce.projectID, fwName).Do()
	if err != nil && isHTTPErrorCode(err, http.StatusNotFound) {
		glog.Infof("Firewall %s already deleted. Continuing to delete other resources.", fwName)
	} else if err != nil {
		glog.Warningf("Failed to delete firewall %s, got error %v", fwName, err)
		return err
	} else {
		if err := gce.waitForGlobalOp(op); err != nil {
			glog.Warningf("Failed waiting for Firewall %s to be deleted.  Got error: %v", fwName, err)
			return err
		}
	}
	return nil
}

// CreateHttpHealthCheck creates the given HttpHealthCheck.
func (gce *GCEClient) CreateHttpHealthCheck(name string) error {
	hcName := makeHttpHealthCheckName(name)
	// TODO add port, etc
	hc := &compute.HttpHealthCheck{Name: hcName}
	op, err := gce.service.HttpHealthChecks.Insert(gce.projectID, hc).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// UpdateHttpHealthCheck applies the given HttpHealthCheck as an update.
func (gce *GCEClient) UpdateHttpHealthCheck(name string) error {
	hcName := makeHttpHealthCheckName(name)
	// TODO add port, etc
	hc := &compute.HttpHealthCheck{Name: hcName}
	op, err := gce.service.HttpHealthChecks.Update(gce.projectID, hcName, hc).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// RemoveHttpHealthCheck deletes the given HttpHealthCheck by name.
func (gce *GCEClient) RemoveHttpHealthCheck(name string) error {
	hcName := makeHttpHealthCheckName(name)
	op, err := gce.service.HttpHealthChecks.Delete(gce.projectID, hcName).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForGlobalOp(op)
}

// CreateTargetHttpProxy creates and returns a TargetHttpProxy with the given UrlMap.
func (gce *GCEClient) CreateTargetHttpProxy(urlMap *compute.UrlMap, name string) error {
	proxy := &compute.TargetHttpProxy{
		Name:   name,
		UrlMap: urlMap.SelfLink,
	}
	op, err := gce.service.TargetHttpProxies.Insert(gce.projectID, proxy).Do()
	if err != nil {
		return err
	}
	if err = gce.waitForGlobalOp(op); err != nil {
		return err
	}

	return nil
}

// RemoveTargetHttpProxy removes the TargetHttpProxy by name.
func (gce *GCEClient) RemoveTargetHttpProxy(name string) error {
	op, err := gce.service.TargetHttpProxies.Delete(gce.projectID, name).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}

	return gce.waitForGlobalOp(op)
}

func (gce *GCEClient) CreateOrUpdateLoadBalancer(name string, port string) error {
	// create or update firewall rule
	// try to update first
	if err := gce.UpdateFirewall(name, []string{port}); err != nil {
		// couldn't update most probably because firewall didn't exist
		if err := gce.CreateFirewall(name, []string{port}); err != nil {
			// couldn't update or create
			return err
		}
	}
	glog.Infof("Created/updated firewall rule with success.")

	// create or update HTTP health-check
	// try to update first
	if err := gce.UpdateHttpHealthCheck(name); err != nil {
		// couldn't update most probably because health-check didn't exist
		if err := gce.CreateHttpHealthCheck(name); err != nil {
			// couldn't update or create
			return err
		}
	}
	glog.Infof("Created/updated HTTP health-check with success.")

	// TODO create backend services with healthcheck
	// TODO create urlmap with backend services
	// TODO create or update target proxy with urlmap
	// TODO create global fwd rule with target proxy

	return nil

}

func (gce *GCEClient) RemoveLoadBalancer(groupName string) error {

	// remove firewall rule
	if err := gce.RemoveFirewall(groupName); err != nil {
		return err
	}
	glog.Infof("Removed firewall rule with success")

	// remove HTTP health-check
	if err := gce.RemoveHttpHealthCheck(groupName); err != nil {
		return err
	}
	glog.Infof("Removed HTTP health-check with success")

	return nil
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

func makeName(prefix string, name string) string {
	return strings.Join([]string{prefix, name}, "-")
}

func makeFirewallName(name string) string {
	return makeName("fw", name)
}

func makeHttpHealthCheckName(name string) string {
	return makeName("http-hc", name)
}

func makeHttpsHealthCheckName(name string) string {
	return makeName("https-hc", name)
}

// makeFirewallObject returns a pre-populated instance of *computeFirewall
func (gce *GCEClient) makeFirewallObject(name string, allowedPorts []string) (*compute.Firewall, error) {
	firewall := &compute.Firewall{
		Name:         name,
		Description:  "Generated by consul-lb-gce",
		Network:      gce.networkURL,
		SourceRanges: []string{"130.211.0.0/22"}, // allow load-balancers alone
		Allowed: []*compute.FirewallAllowed{
			{
				IPProtocol: "tcp",
				Ports:      allowedPorts,
			},
		},
	}
	return firewall, nil
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
