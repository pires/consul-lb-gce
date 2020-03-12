package gce

import (
	"fmt"
	"net/http"

	"google.golang.org/api/dns/v1"

	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
)

// NetworkEndpoint represents network endpoint
type NetworkEndpoint struct {
	Instance string
	IP       string
	Port     string
}

// Client is a client to GCE.
type Client struct {
	httpClient *http.Client
	service    *compute.Service
	dnsService *dns.Service
	projectID  string
	networkURL string
}

const (
	googleComputeAPIHost = "https://www.googleapis.com/compute/v1"
)

// New creates new instance of Client.
func New(project string, network string) (*Client, error) {
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

	// TODO(max): Validate project and network exist
	return &Client{
		httpClient: client,
		service:    svc,
		dnsService: dnsService,
		projectID:  project,
		// TODO(max): Consider zoned network
		networkURL: fmt.Sprintf("%s/projects/%s/global/networks/%s", googleComputeAPIHost, project, network),
	}, nil
}
