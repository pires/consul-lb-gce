package gce

import (
	"bytes"
	"errors"
	"fmt"
	"google.golang.org/api/dns/v1"
	"os/exec"
	"strings"
	"time"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	gceAffinityTypeNone = "NONE"
	// AffinityTypeClientIP - affinity based on Client IP.
	gceAffinityTypeClientIP = "CLIENT_IP"

	operationPollInterval        = 3 * time.Second
	operationPollTimeoutDuration = 30 * time.Minute

	servicePort = "service-port"

	maxResourceNameLength = 63
)

var (
	ErrInstanceNotFound = errors.New("Instance not found")
)

// GCEClient is a placeholder for GCE stuff.
type GCEClient struct {
	service    *compute.Service
	dnsService *dns.Service
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

	dnsService, err := dns.New(client)
	if err != nil {
		return nil, err
	}

	// TODO validate project and network exist

	return &GCEClient{
		service:    svc,
		dnsService: dnsService,
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

// GetInstanceGroupForZone returns an instance group by name for zone.
func (gce *GCEClient) GetInstanceGroupForZone(name string, zone string) (*compute.InstanceGroup, error) {
	return gce.service.InstanceGroups.Get(gce.projectID, zone, name).Do()
}

// GetAvailableZones returns all available zones for this project
func (gce *GCEClient) GetAvailableZones() (*compute.ZoneList, error) {
	return gce.service.Zones.List(gce.projectID).Do()
}

// HttpHealthCheck Management

// CreateHttpHealthCheck creates the given HttpHealthCheck.
func (gce *GCEClient) CreateHttpHealthCheck(name string, path string) error {
	hcName := makeHttpHealthCheckName(name)
	if path == "" {
		path = "/"
	}
	cmd := exec.Command("gcloud", "beta", "compute", "health-checks", "create", "http", hcName, "--request-path="+path, "--use-serving-port")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		glog.Errorf("Failed creating health check [%s]. %s", hcName, stderr.String())
	}
	return nil
}

// BackendService Management

// GetBackendService retrieves a backend by name.
func (gce *GCEClient) GetBackendService(name string) (*compute.BackendService, error) {
	bsName := makeBackendServiceName(name)
	return gce.service.BackendServices.Get(gce.projectID, bsName).Do()
}

// UrlMap management

// GetUrlMap returns the UrlMap by name.
func (gce *GCEClient) GetUrlMap(name string) (*compute.UrlMap, error) {
	return gce.service.UrlMaps.Get(gce.projectID, name).Do()
}

// UpdateUrlMap updates an url map, using the given backend service as the default service.
func (gce *GCEClient) UpdateUrlMap(urlMapName, name, host, path string) error {

	urlMap, err := gce.GetUrlMap(urlMapName)

	if err != nil {
		glog.Errorf("Can't get url map %s", err)
		return err
	}

	backend, err := gce.GetBackendService(name)

	if err != nil {
		glog.Errorf("Can't get backend service %s", err)
		return err
	}

	pathMatcherName := strings.Split(host, ".")[0]

	// create path matcher if it doesn't exist
	var existingPathMatcher *compute.PathMatcher
	for _, pm := range urlMap.PathMatchers {
		if pm.Name == pathMatcherName {
			existingPathMatcher = pm
			break
		}
	}
	var defaultServiceLink string
	if path == "/" {
		defaultServiceLink = backend.SelfLink
	} else {
		defaultServiceLink = urlMap.DefaultService
	}
	if existingPathMatcher == nil {
		urlMap.PathMatchers = append(urlMap.PathMatchers, &compute.PathMatcher{
			Name:           pathMatcherName,
			DefaultService: defaultServiceLink,
			PathRules: []*compute.PathRule{
				GetPathRule(path, backend.SelfLink),
			},
		})
	}

	// create path matcher if it doesn't exist
	var existingHostRule *compute.HostRule
	for _, hr := range urlMap.HostRules {
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

// NOTE: maximal length of resource name is 63 and name must start and end with letter or digit
func makeName(prefix string, name string) string {
	n := strings.Join([]string{prefix, name}, "-")
	if len(n) > maxResourceNameLength {
		n = n[:maxResourceNameLength]
		if n[len(n)-1] == '-' {
			return n[:len(n)-2]
		}
	}
	return n
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

func (gce *GCEClient) AddDnsRecordSet(managedZone, globalAddressName, host string) error {
	addresses, err := gce.service.GlobalAddresses.List(gce.projectID).Do()

	if err != nil {
		return err
	}

	var address string

	for _, addr := range addresses.Items {
		if addr.Name == globalAddressName {
			address = addr.Address
		}
	}

	_, err = gce.dnsService.Changes.Create(gce.projectID, managedZone, &dns.Change{
		Additions: []*dns.ResourceRecordSet{
			{
				Name:    host + ".",
				Ttl:     300,
				Type:    "A",
				Rrdatas: []string{address},
				Kind:    "dns#resourceRecordSet",
			},
		},
	}).Do()

	if isHTTPErrorCode(err, 409) {
		return err
	}

	return nil
}

func GetPathRule(path string, backendServiceLink string) *compute.PathRule {
	if path == "/" {
		return nil
	}

	if path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}

	paths := []string{
		path,
		path + "/*",
	}

	return &compute.PathRule{
		Paths:   paths,
		Service: backendServiceLink,
	}
}

func (gce *GCEClient) CreateBackendService(zonifiedGroupName, groupName, zone, affinity string, cdn bool) error {
	bsName := makeBackendServiceName(zonifiedGroupName)
	hcName := makeHttpHealthCheckName(groupName)

	cmd := exec.Command("gcloud", "beta", "compute", "backend-services", "create", bsName, "--global", "--health-checks", hcName, getCdnOption(cdn), getAffinityOption(affinity))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		glog.Errorf("Failed creating backend service [%s]. %s", bsName, stderr.String())
		if !strings.Contains(stderr.String(), "already exists") {
			return err
		}
	}

	cmd = exec.Command("gcloud", "beta", "compute", "backend-services", "add-backend", bsName,
		"--global", "--network-endpoint-group="+zonifiedGroupName, "--network-endpoint-group-zone="+zone, "--balancing-mode=RATE", "--max-rate-per-endpoint=5")
	stderr = bytes.Buffer{}
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		glog.Errorf("Failed attaching network endpoint group [%s] to backend service [%s]. %s", groupName, bsName, stderr.String())
		if !strings.Contains(stderr.String(), "already exists") {
			return err
		}
	}
	return nil
}

func getAffinityOption(affinity string) string {
	switch affinity {
	case "ipaffinity":
		return "--session-affinity=" + gceAffinityTypeClientIP
	case "noaffinity":
		return "--session-affinity=" + gceAffinityTypeNone
	default:
		return ""
	}
}

func getCdnOption(cdn bool) string {
	if cdn {
		return "--enable-cdn"
	} else {
		return ""
	}
}
