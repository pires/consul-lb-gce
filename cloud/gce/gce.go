package gce

import (
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

	// TODO validate project and network exist

	return &Client{
		httpClient: client,
		service:    svc,
		dnsService: dnsService,
		projectID:  project,
		networkURL: makeNetworkURL(project, network),
	}, nil
}
