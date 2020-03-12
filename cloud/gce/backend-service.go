package gce

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/dffrntmedia/consul-lb-gce/util"
	"github.com/golang/glog"
)

const (
	gceAffinityTypeNone     = "NONE"
	gceAffinityTypeClientIP = "CLIENT_IP"
)

// CreateBackendService creates backend service based on NEG
func (gce *Client) CreateBackendService(bsName, negName, hcName, zone, affinity string, cdn bool) error {
	req, err := http.NewRequest(
		"POST",
		gce.makeCreateBackendServiceURL(zone),
		bytes.NewBuffer([]byte(fmt.Sprintf(`{
				"name": "%s",
				"description": "Managed by consul-lb-gce",
				"backends": [
					{
						"group": "%s",
						"balancingMode": "RATE",
						"maxRatePerEndpoint": 10000
					}
				],
				"healthChecks": [
					"%s"
				],
				"enableCDN": %t,
				"sessionAffinity": "%s"
			}`,
			bsName,
			gce.makeNetworkEndpointGroupURL(negName, zone),
			fmt.Sprintf("%s/projects/%s/global/healthChecks/%s", googleComputeAPIHost, gce.projectID, hcName),
			cdn,
			getAffinityOption(affinity),
		))),
	)
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")
	res, err := util.SendHTTPRequest(gce.httpClient, req, []int{http.StatusOK, http.StatusConflict})
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusConflict {
		glog.Infof("Backend service %s alredy exists", bsName)
		return nil
	}
	return gce.waitForOpFromHTTPResponse(res, "global", fmt.Sprintf("backend service %s creation", bsName))
}

func getAffinityOption(affinity string) string {
	switch affinity {
	case "ipaffinity":
		return gceAffinityTypeClientIP
	case "noaffinity":
		fallthrough
	default:
		return gceAffinityTypeNone
	}
}

func (gce *Client) makeNetworkEndpointGroupURL(neg, zone string) string {
	if zone == "global" {
		return fmt.Sprintf("%s/projects/%s/global/networkEndpointGroups/%s", googleComputeAPIHost, gce.projectID, neg)
	}
	return fmt.Sprintf("%s/projects/%s/zones/%s/networkEndpointGroups/%s", googleComputeAPIHost, gce.projectID, zone, neg)
}

func (gce *Client) makeCreateBackendServiceURL(zone string) string {
	return fmt.Sprintf("%s/projects/%s/global/backendServices", googleComputeAPIHost, gce.projectID)

	// TODO(max): Handle creating regional backend service
	// https://cloud.google.com/compute/docs/reference/rest/v1/regionBackendServices

	// if zone == "global" {
	// 	return fmt.Sprintf("%s/projects/%s/global/backendServices", googleComputeAPIHost, gce.projectID)
	// }
	// return fmt.Sprintf("%s/projects/%s/zones/%s/backendServices", googleComputeAPIHost, gce.projectID, zone)
}
