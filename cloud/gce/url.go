package gce

import "fmt"

const (
	googleComputeAPIHost = "https://www.googleapis.com/compute/v1"
)

func makeNetworkURL(project string, network string) string {
	return fmt.Sprintf("%s/projects/%s/global/networks/%s", googleComputeAPIHost, project, network)
}

func (gce *Client) makeNetworkEndpointGroupURL(neg, zone string) string {
	return fmt.Sprintf("%s/projects/%s/zones/%s/networkEndpointGroups/%s", googleComputeAPIHost, gce.projectID, zone, neg)
}

func (gce *Client) makeHealthCheckURL(hc string) string {
	return fmt.Sprintf("%s/projects/%s/global/healthChecks/%s", googleComputeAPIHost, gce.projectID, hc)
}

func (gce *Client) makeAttachNetworkEndpointsURL(neg, zone string) string {
	return fmt.Sprintf("%s/projects/%s/zones/%s/networkEndpointGroups/%s/attachNetworkEndpoints", googleComputeAPIHost, gce.projectID, zone, neg)
}

func (gce *Client) makeDetachNetworkEndpointsURL(neg, zone string) string {
	return fmt.Sprintf("%s/projects/%s/zones/%s/networkEndpointGroups/%s/detachNetworkEndpoints", googleComputeAPIHost, gce.projectID, zone, neg)
}

func (gce *Client) makeCreateNetworkEndpointGroupURL(zone string) string {
	return fmt.Sprintf("%s/projects/%s/zones/%s/networkEndpointGroups", googleComputeAPIHost, gce.projectID, zone)
}

func (gce *Client) makeCreateHealthCheckURL() string {
	return fmt.Sprintf("%s/projects/%s/global/healthChecks", googleComputeAPIHost, gce.projectID)
}
