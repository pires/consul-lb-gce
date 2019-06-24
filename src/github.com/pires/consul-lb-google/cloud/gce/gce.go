package gce

import (
	"bytes"
	"fmt"
	"github.com/pires/consul-lb-google/util"
	"google.golang.org/api/dns/v1"
	"net/http"
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
	gceAffinityTypeNone     = "NONE"
	gceAffinityTypeClientIP = "CLIENT_IP"

	operationPollInterval        = 3 * time.Second
	operationPollTimeoutDuration = 30 * time.Minute

	maxResourceNameLength = 63
)

type NetworkEndpoint struct {
	Instance string
	Ip       string
	Port     string
}

// GCEClient is a placeholder for GCE stuff.
type GCEClient struct {
	httpClient *http.Client
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
		httpClient: client,
		service:    svc,
		dnsService: dnsService,
		projectID:  project,
		networkURL: makeNetworkURL(project, network),
	}, nil
}

func (gce *GCEClient) CreateNetworkEndpointGroup(groupName, zone string) error {
	request, err := http.NewRequest("POST", gce.makeCreateNetworkEndpointGroupUrl(zone), gce.makeCreateNetworkEndpointGroupBody(groupName, gce.networkURL))

	if err != nil {
		return err
	}

	request.Header.Add("Content-Type", "application/json")

	_, err = util.SendRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})

	if err != nil {
		return err
	}

	return nil
}

func (gce *GCEClient) AttachNetworkEndpoints(groupName, zone string, endpoints []NetworkEndpoint) error {
	request, err := http.NewRequest("POST", gce.makeAttachNetworkEndpointsUrl(groupName, zone),
		gce.makeAttachOrDetachNetworkEndpointsBody(endpoints, zone))

	if err != nil {
		return err
	}

	request.Header.Add("Content-Type", "application/json")

	_, err = util.SendRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})

	if err != nil {
		return err
	}

	return nil
}

func (gce *GCEClient) DetachNetworkEndpoints(groupName, zone string, endpoints []NetworkEndpoint) error {
	request, err := http.NewRequest("POST", gce.makeDetachNetworkEndpointsUrl(groupName, zone),
		gce.makeAttachOrDetachNetworkEndpointsBody(endpoints, zone))

	if err != nil {
		return err
	}

	request.Header.Add("Content-Type", "application/json")

	_, err = util.SendRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})

	if err != nil {
		return err
	}

	return nil
}

// HttpHealthCheck Management

// CreateHttpHealthCheck creates the given HttpHealthCheck.
func (gce *GCEClient) CreateHttpHealthCheck(name string, path string) error {
	hcName := makeHttpHealthCheckName(name)

	if path == "" {
		path = "/"
	}

	request, err := http.NewRequest("POST", gce.makeCreateHealthCheckUrl(), gce.makeCreateHealthCheckBody(hcName, path))

	if err != nil {
		return err
	}

	request.Header.Add("Content-Type", "application/json")

	_, err = util.SendRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})

	if err != nil {
		return err
	}

	return nil
}

// BackendService Management

// GetBackendService retrieves a backend by name.
func (gce *GCEClient) GetBackendService(name string) (*compute.BackendService, error) {
	bsName := makeBackendServiceName(name)
	return gce.service.BackendServices.Get(gce.projectID, bsName).Do()
}

func (gce *GCEClient) CreateBackendService(groupName, zone, affinity string, cdn bool) error {
	zonifiedGroupName := util.Zonify(zone, groupName)
	bsName := makeBackendServiceName(zonifiedGroupName)
	hcName := makeHttpHealthCheckName(groupName)

	request, err := http.NewRequest("POST", gce.makeCreateBackendServiceUrl(), gce.makeCreateBackendServiceBody(bsName, zonifiedGroupName, hcName, zone, cdn, getAffinityOption(affinity)))

	if err != nil {
		return err
	}

	request.Header.Add("Content-Type", "application/json")

	_, err = util.SendRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})

	if err != nil {
		return err
	}

	return nil
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
	if existingPathMatcher == nil {
		var defaultServiceLink string
		if path == "/" {
			defaultServiceLink = backend.SelfLink
		} else {
			defaultServiceLink = urlMap.DefaultService
		}

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
		if hr.Description == host {
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

func makeNetworkURL(project string, network string) string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/networks/%s", project, network)
}

func (gce *GCEClient) makeNetworkEndpointGroupUrl(negName, zone string) string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/networkEndpointGroups/%s", gce.projectID, zone, negName)
}

func (gce *GCEClient) makeHealthCheckUrl(healthCheckName string) string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/healthChecks/%s", gce.projectID, healthCheckName)
}

func (gce *GCEClient) makeInstanceUrl(instance, zone string) string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/instances/%s", gce.projectID, zone, instance)
}

func (gce *GCEClient) makeAttachNetworkEndpointsUrl(groupName, zone string) string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/networkEndpointGroups/%s/attachNetworkEndpoints", gce.projectID, zone, groupName)
}

func (gce *GCEClient) makeDetachNetworkEndpointsUrl(groupName, zone string) string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/networkEndpointGroups/%s/detachNetworkEndpoints", gce.projectID, zone, groupName)
}

func (gce *GCEClient) makeAttachOrDetachNetworkEndpointsBody(endpoints []NetworkEndpoint, zone string) *bytes.Buffer {
	var endpointsJsons []string
	for _, endpoint := range endpoints {
		endpointsJsons = append(endpointsJsons, fmt.Sprintf(`{
			"instance": "%s",
			"ipAddress": "%s",
			"port": %s
		}`, gce.makeInstanceUrl(endpoint.Instance, zone), endpoint.Ip, endpoint.Port))
	}
	return bytes.NewBuffer([]byte(fmt.Sprintf("{ \"networkEndpoints\": [%s] }", strings.Join(endpointsJsons, ","))))
}


func (gce *GCEClient) makeCreateNetworkEndpointGroupUrl(zone string) string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/networkEndpointGroups", gce.projectID, zone)
}

func (gce *GCEClient) makeCreateNetworkEndpointGroupBody(name, network string) *bytes.Buffer {
	return bytes.NewBuffer([]byte(fmt.Sprintf(`{
		"name": "%s",
		"description": "Managed by consul-lb-google",
		"defaultPort": 80,
		"networkEndpointType": "GCE_VM_IP_PORT",
		"network": "%s"
	}`, name, network)))
}

func (gce *GCEClient) makeCreateHealthCheckUrl() string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/healthChecks", gce.projectID)
}

func (gce *GCEClient) makeCreateHealthCheckBody(name, path string) *bytes.Buffer {
	return bytes.NewBuffer([]byte(fmt.Sprintf(`{
		"name": "%s",
		"description": "Managed by consul-lb-google",
		"kind": "compute#healthCheck",
		"type": "HTTP",
		"httpHealthCheck": {
    		"portSpecification": "USE_SERVING_PORT",
    		"requestPath": "%s"
		},
		"timeoutSec": 2,
		"checkIntervalSec": 2,
  		"healthyThreshold": 2,
		"unhealthyThreshold": 2
	}`, name, path)))
}

func (gce *GCEClient) makeCreateBackendServiceUrl() string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/backendServices", gce.projectID)
}

func (gce *GCEClient) makeCreateBackendServiceBody(name, groupName, healthCheckName, zone string, cdn bool, affinity string) *bytes.Buffer {
	return bytes.NewBuffer([]byte(fmt.Sprintf(`{
  		"name": "%s",
  		"description": "Managed by consul-lb-google",
  		"backends": [
			{
      			"group": "%s",
      			"balancingMode": "RATE",
      			"maxRatePerEndpoint": 5
    		}
		],
  		"healthChecks": [
			"%s"
		],
  		"enableCDN": %t,
  		"sessionAffinity": "%s"
	}`, name, gce.makeNetworkEndpointGroupUrl(groupName, zone), gce.makeHealthCheckUrl(healthCheckName), cdn, affinity)))
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

func makeHttpHealthCheckName(name string) string {
	return makeName("http-hc", name)
}

func makeBackendServiceName(name string) string {
	return makeName("backend", name)
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

// NOTE: It's not used for now
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

func getAffinityOption(affinity string) string {
	switch affinity {
	case "ipaffinity":
		return gceAffinityTypeClientIP
	case "noaffinity":
		return gceAffinityTypeNone
	default:
		return gceAffinityTypeNone
	}
}
