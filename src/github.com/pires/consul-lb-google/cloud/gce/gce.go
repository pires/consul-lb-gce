package gce

import (
	"errors"
	"fmt"
	"github.com/pires/consul-lb-google/util"
	"google.golang.org/api/dns/v1"
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

// HttpHealthCheck Management

// CreateHttpHealthCheck creates the given HttpHealthCheck.
func (gce *GCEClient) CreateHttpHealthCheck(name string, path string) error {
	hcName := makeHttpHealthCheckName(name)

	if path == "" {
		path = "/"
	}

	args := []string{"gcloud", "beta", "compute", "health-checks", "create", "http", hcName, "--request-path=" + path, "--use-serving-port" /*todo(max): remove this flag, for local usage only*/, "--global"}

	if err := util.ExecCommand(args); err != nil && !util.IsAlreadyExistsError(err) {
		glog.Errorf("Failed creating health check [%s]. %s", hcName, err)
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

	args := []string{"gcloud", "beta", "compute", "backend-services", "create", bsName, "--global", "--health-checks", hcName,
		/*todo(max): remote, for local usage only*/ "--global-health-checks", getCdnOption(cdn), getAffinityOption(affinity)}

	if err := util.ExecCommand(args); err != nil && !util.IsAlreadyExistsError(err) {
		glog.Errorf("Failed creating backend service [%s]. %s", bsName, err)
		return err
	}

	args = []string{"gcloud", "beta", "compute", "backend-services", "add-backend", bsName,
		"--global", "--network-endpoint-group=" + zonifiedGroupName, "--network-endpoint-group-zone=" + zone, "--balancing-mode=RATE", "--max-rate-per-endpoint=5"}

	if err := util.ExecCommand(args); err != nil && !util.IsAlreadyExistsError(err) {
		glog.Errorf("Failed attaching network endpoint group [%s] to backend service [%s]. %s", zonifiedGroupName, bsName, err)
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
