package gce

// code ripped and adapted from Kubernetes source

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"k8s.io/apimachinery/pkg/util/wait"
	"strconv"
)

const (
	gceAffinityTypeNone = "NONE"
	// AffinityTypeClientIP - affinity based on Client IP.
	gceAffinityTypeClientIP = "CLIENT_IP"
	// AffinityTypeClientIPProto - affinity based on Client IP and port.
	gceAffinityTypeClientIPProto = "CLIENT_IP_PROTO"

	operationPollInterval        = 3 * time.Second
	operationPollTimeoutDuration = 30 * time.Minute

	servicePort = "service-port"
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
	namedPorts = append(namedPorts, &compute.NamedPort{Name: servicePort, Port: port})
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

// Firewall rules management

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

// HttpHealthCheck Management

// GetHttpHealthCheck returns the given HttpHealthCheck by name.
func (gce *GCEClient) GetHttpHealthCheck(name string) (*compute.HttpHealthCheck, error) {
	hcName := makeHttpHealthCheckName(name)
	return gce.service.HttpHealthChecks.Get(gce.projectID, hcName).Do()
}

// CreateHttpHealthCheck creates the given HttpHealthCheck.
func (gce *GCEClient) CreateHttpHealthCheck(name string, port string, path string) error {
	hcName := makeHttpHealthCheckName(name)
	hcPort, err := strconv.ParseInt(port, 10, 64)
	if err != nil {
		hcPort = 80
	}
	if path == "" {
		path = "/"
	}
	hc := &compute.HttpHealthCheck{
		Name:        hcName,
		Port:        hcPort,
		RequestPath: path,
	}
	op, err := gce.service.HttpHealthChecks.Insert(gce.projectID, hc).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// UpdateHttpHealthCheck applies the given HttpHealthCheck as an update.
func (gce *GCEClient) UpdateHttpHealthCheck(name string, port, path string) error {
	hcName := makeHttpHealthCheckName(name)
	hcPort, err := strconv.ParseInt(port, 10, 64)
	if err != nil {
		hcPort = 80
	}
	if path == "" {
		path = "/"
	}
	hc := &compute.HttpHealthCheck{
		Name:        hcName,
		Port:        hcPort,
		RequestPath: path,
	}
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

// BackendService Management

// GetBackendService retrieves a backend by name.
func (gce *GCEClient) GetBackendService(name string) (*compute.BackendService, error) {
	bsName := makeBackendServiceName(name)
	return gce.service.BackendServices.Get(gce.projectID, bsName).Do()
}

//zonify takes a specified name and prepends a specified zone plus an hyphen
// e.g. zone == "us-east1-d" && name == "myname", returns "us-east1-d-myname"
func zonify(zone string, name string) string {
	return strings.Join([]string{zone, name}, "-")
}

// CreateBackendService creates the given BackendService.
func (gce *GCEClient) CreateBackendService(name string, zones []string) error {
	bsName := makeBackendServiceName(name)

	// prepare backends
	var backends []*compute.Backend
	// one backend (instance group) per zone
	for _, zone := range zones {
		// instance groups have been previously zonified
		ig, _ := gce.GetInstanceGroupForZone(zonify(zone, name), zone)
		backends = append(backends, &compute.Backend{
			Description: zone,
			Group:       ig.SelfLink,
		})
	}

	hc, _ := gce.GetHttpHealthCheck(name)

	// prepare backend service
	bs := &compute.BackendService{
		Backends:     backends,
		HealthChecks: []string{hc.SelfLink},
		Name:         bsName,
		PortName:     servicePort,
		Protocol:     "HTTP",
		TimeoutSec:   10, // TODO make configurable
	}
	op, err := gce.service.BackendServices.Insert(gce.projectID, bs).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// UpdateBackendService applies the given BackendService as an update to an existing service.
func (gce *GCEClient) UpdateBackendService(name string, zones []string) error {
	bsName := makeBackendServiceName(name)

	// prepare backends
	var backends []*compute.Backend
	// one backend (instance group) per zone
	for _, zone := range zones {
		// instance groups have been previously zonified
		ig, _ := gce.GetInstanceGroupForZone(zonify(zone, name), zone)
		backends = append(backends, &compute.Backend{
			Description: zone,
			Group:       ig.SelfLink,
		})
	}

	hc, _ := gce.GetHttpHealthCheck(name)

	// prepare backend service
	bs := &compute.BackendService{
		Backends:     backends,
		HealthChecks: []string{hc.SelfLink},
		Name:         bsName,
		PortName:     servicePort,
		Protocol:     "HTTP",
	}

	op, err := gce.service.BackendServices.Update(gce.projectID, bsName, bs).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// RemoveBackendService deletes the given BackendService by name.
func (gce *GCEClient) RemoveBackendService(name string) error {
	bsName := makeBackendServiceName(name)
	op, err := gce.service.BackendServices.Delete(gce.projectID, bsName).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForGlobalOp(op)
}

// UrlMap management

// GetUrlMap returns the UrlMap by name.
func (gce *GCEClient) GetUrlMap(name string) (*compute.UrlMap, error) {
	return gce.service.UrlMaps.Get(gce.projectID, name).Do()
}

// UpdateUrlMap updates an url map, using the given backend service as the default service.
func (gce *GCEClient) UpdateUrlMap(urlMap *compute.UrlMap, name, host, path string) error {
	backend, _ := gce.GetBackendService(name)

	pathMatcherName := strings.Split(host, ".")[0]

	// create path matcher if it doesn't exist
	var existingPathMatcher *compute.PathMatcher
	for _, pm := range (urlMap.PathMatchers) {
		if pm.Name == pathMatcherName {
			existingPathMatcher = pm
			break
		}
	}
	if existingPathMatcher == nil {
		// todo(max): handle paths like '/v1'
		urlMap.PathMatchers = append(urlMap.PathMatchers, &compute.PathMatcher{
			Name:           pathMatcherName,
			DefaultService: backend.SelfLink,
		})
	}

	// create path matcher if it doesn't exist
	var existingHostRule *compute.HostRule
	for _, hr := range (urlMap.HostRules) {
		if hr.Description == pathMatcherName {
			existingHostRule = hr
			break
		}
	}
	if existingHostRule == nil {
		urlMap.HostRules = append(urlMap.HostRules, &compute.HostRule{
			Hosts:       []string{host},
			PathMatcher: pathMatcherName,
			Description: host,
		})
	}

	if existingPathMatcher == nil || existingHostRule == nil {
		op, err := gce.service.UrlMaps.Update(gce.projectID, urlMap.Name, urlMap).Do()

		if err != nil {
			return err
		}

		if err = gce.waitForGlobalOp(op); err != nil {
			return err
		}
	}

	return nil
}

// RemoveUrlMap deletes a url map by name.
//func (gce *GCEClient) RemoveUrlMap(name string) error {
//	op, err := gce.service.UrlMaps.Delete(gce.projectID, name).Do()
//	if err != nil {
//		if isHTTPErrorCode(err, http.StatusNotFound) {
//			return nil
//		}
//		return err
//	}
//	return gce.waitForGlobalOp(op)
//}

// TargetHttpProxy management

// GetTargetHttpProxy returns the UrlMap by name.
func (gce *GCEClient) GetTargetHttpProxy(name string) (*compute.TargetHttpProxy, error) {
	thpName := makeHttpProxyName(name)
	return gce.service.TargetHttpProxies.Get(gce.projectID, thpName).Do()
}

// CreateTargetHttpProxy creates and returns a TargetHttpProxy with the given UrlMap.
func (gce *GCEClient) CreateTargetHttpProxy(name string) error {
	urlMap, _ := gce.GetUrlMap(name)
	thpName := makeHttpProxyName(name)
	proxy := &compute.TargetHttpProxy{
		Name:   thpName,
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
	thpName := makeHttpProxyName(name)
	op, err := gce.service.TargetHttpProxies.Delete(gce.projectID, thpName).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}

	return gce.waitForGlobalOp(op)
}

// GlobalForwardingRule management

// CreateGlobalForwardingRule creates and returns a GlobalForwardingRule that points to the given TargetHttpProxy.
func (gce *GCEClient) CreateGlobalForwardingRule(name string, portRange string) error {
	thp, _ := gce.GetTargetHttpProxy(name)
	fwdName := makeForwardingRuleName(name)
	rule := &compute.ForwardingRule{
		Name:       fwdName,
		IPProtocol: "TCP",
		PortRange:  "80", // TODO enable portRange
		Target:     thp.SelfLink,
	}
	op, err := gce.service.GlobalForwardingRules.Insert(gce.projectID, rule).Do()
	if err != nil {
		return err
	}
	if err = gce.waitForGlobalOp(op); err != nil {
		return err
	}
	return nil
}

// RemoveGlobalForwardingRule deletes the GlobalForwardingRule by name.
func (gce *GCEClient) RemoveGlobalForwardingRule(name string) error {
	fwdName := makeForwardingRuleName(name)
	op, err := gce.service.GlobalForwardingRules.Delete(gce.projectID, fwdName).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForGlobalOp(op)
}

func (gce *GCEClient) UpdateLoadBalancer(urlMapName, name string, port string, healthCheckPath string, host, path string, zones []string) error {
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
	if err := gce.UpdateHttpHealthCheck(name, port, healthCheckPath); err != nil {
		// couldn't update most probably because health-check didn't exist
		if err := gce.CreateHttpHealthCheck(name, port, healthCheckPath); err != nil {
			// couldn't update or create
			return err
		}
	}
	glog.Infof("Created/updated HTTP health-check with success.")

	// create or update backend service, only for allowed zones
	// try to update first
	if err := gce.UpdateBackendService(name, zones); err != nil {
		// couldn't update most probably because backend service didn't exist
		if err := gce.CreateBackendService(name, zones); err != nil {
			// couldn't update or create
			return err
		}
	}
	glog.Infof("Created/updated backend service with success.")

	urlMap, err := gce.GetUrlMap(urlMapName)

	if err != nil {
		glog.Errorf("Can't get url map %s", err)
		return err
	}

	// update url map
	if err := gce.UpdateUrlMap(urlMap, name, host, path); err != nil {
		return err
	}
	glog.Infof("Updated URL map with success.")

	return nil
}

//func (gce *GCEClient) RemoveLoadBalancer(name string) error {
//	// TODO make the following optional according to creation
//	// remove global forwarding rule
//	if err := gce.RemoveGlobalForwardingRule(name); err != nil {
//		return err
//	}
//	glog.Infof("Removed global forwarding rule with success.")
//
//	// remove target http proxy
//	if err := gce.RemoveTargetHttpProxy(name); err != nil {
//		return err
//	}
//	glog.Infof("Removed target HTTP proxy with success.")
//
//	// remove url map
//	if err := gce.RemoveUrlMap(name); err != nil {
//		return err
//	}
//	glog.Infof("Removed URL map with success.")
//
//	// remove backend service
//	if err := gce.RemoveBackendService(name); err != nil {
//		return err
//	}
//	glog.Infof("Removed backend service with success.")
//
//	// remove HTTP health-check
//	if err := gce.RemoveHttpHealthCheck(name); err != nil {
//		return err
//	}
//	glog.Infof("Removed HTTP health-check with success.")
//
//	// remove firewall rule
//	if err := gce.RemoveFirewall(name); err != nil {
//		return err
//	}
//	glog.Infof("Removed firewall rule with success.")
//
//	return nil
//}

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

func makeBackendServiceName(name string) string {
	return makeName("backend", name)
}

func makeHttpsHealthCheckName(name string) string {
	return makeName("https-hc", name)
}

func makeHttpProxyName(name string) string {
	return makeName("http-proxy", name)
}

func makeForwardingRuleName(name string) string {
	return makeName("fwd-rule", name)
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
