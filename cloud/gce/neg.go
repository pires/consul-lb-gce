package gce

import (
	"net/http"

	"github.com/dffrntmedia/consul-lb-gce/util"
)

// CreateNetworkEndpointGroup create network endpoint group
func (gce *Client) CreateNetworkEndpointGroup(name, zone string) error {
	request, err := http.NewRequest(
		"POST",
		gce.makeCreateNetworkEndpointGroupURL(zone),
		gce.makeCreateNetworkEndpointGroupBody(name, gce.networkURL),
	)
	if err != nil {
		return err
	}
	request.Header.Add("Content-Type", "application/json")
	_, err = util.SendHTTPRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})
	return err
}

// AttachNetworkEndpoints adds network endpoints to group
func (gce *Client) AttachNetworkEndpoints(name, zone string, endpoints []NetworkEndpoint) error {
	request, err := http.NewRequest(
		"POST",
		gce.makeAttachNetworkEndpointsURL(name, zone),
		gce.makeAttachOrDetachNetworkEndpointsBody(endpoints, zone),
	)
	if err != nil {
		return err
	}
	request.Header.Add("Content-Type", "application/json")
	_, err = util.SendHTTPRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})
	return err
}

// DetachNetworkEndpoints removes network endpoint from group
func (gce *Client) DetachNetworkEndpoints(groupName, zone string, endpoints []NetworkEndpoint) error {
	request, err := http.NewRequest(
		"POST",
		gce.makeDetachNetworkEndpointsURL(groupName, zone),
		gce.makeAttachOrDetachNetworkEndpointsBody(endpoints, zone),
	)
	if err != nil {
		return err
	}
	request.Header.Add("Content-Type", "application/json")
	_, err = util.SendHTTPRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})
	return err
}
