package gce

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	"github.com/dffrntmedia/consul-lb-gce/util"
	"github.com/golang/glog"
)

// CreateNetworkEndpointGroup creates network endpoint group
func (gce *Client) CreateNetworkEndpointGroup(negName, zone string) error {
	request, err := http.NewRequest(
		"POST",
		fmt.Sprintf("%s/projects/%s/zones/%s/networkEndpointGroups", googleComputeAPIHost, gce.projectID, zone),
		bytes.NewBuffer([]byte(fmt.Sprintf(`{
			"name": "%s",
			"description": "Managed by consul-lb-gce",
			"defaultPort": 80,
			"networkEndpointType": "GCE_VM_IP_PORT",
			"network": "%s"
		}`, negName, gce.networkURL))),
	)
	if err != nil {
		return err
	}
	request.Header.Add("Content-Type", "application/json")
	res, err := util.SendHTTPRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})
	if res.StatusCode == http.StatusConflict {
		glog.Infof("Network endpoint group %s alredy exists", negName)
		return nil
	}
	return gce.waitForOpFromHTTPResponse(res, zone, fmt.Sprintf("network endpoint group %s creation", negName))
}

// AttachNetworkEndpoints adds network endpoints to group
func (gce *Client) AttachNetworkEndpoints(name, zone string, endpoints []NetworkEndpoint) error {
	request, err := http.NewRequest(
		"POST",
		gce.makeAttachNetworkEndpointsURL(name, zone),
		gce.makeAttachOrDetachNetworkEndpointsBody(endpoints),
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
		gce.makeAttachOrDetachNetworkEndpointsBody(endpoints),
	)
	if err != nil {
		return err
	}
	request.Header.Add("Content-Type", "application/json")
	_, err = util.SendHTTPRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})
	return err
}

func (gce *Client) makeAttachNetworkEndpointsURL(neg, zone string) string {
	return fmt.Sprintf("%s/projects/%s/zones/%s/networkEndpointGroups/%s/attachNetworkEndpoints", googleComputeAPIHost, gce.projectID, zone, neg)
}

func (gce *Client) makeDetachNetworkEndpointsURL(neg, zone string) string {
	return fmt.Sprintf("%s/projects/%s/zones/%s/networkEndpointGroups/%s/detachNetworkEndpoints", googleComputeAPIHost, gce.projectID, zone, neg)
}

func (gce *Client) makeAttachOrDetachNetworkEndpointsBody(endpoints []NetworkEndpoint) *bytes.Buffer {
	var endpointsJsons []string
	for _, endpoint := range endpoints {
		endpointsJsons = append(endpointsJsons, fmt.Sprintf(`{
			"instance": "%s",
			"ipAddress": "%s",
			"port": %s
		}`, endpoint.Instance, endpoint.IP, endpoint.Port))
	}
	return bytes.NewBuffer([]byte(fmt.Sprintf("{ \"networkEndpoints\": [%s] }", strings.Join(endpointsJsons, ","))))
}
