package gce

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/dffrntmedia/consul-lb-gce/util"
	"github.com/golang/glog"
	"google.golang.org/api/compute/v1"
)

const (
	gceAffinityTypeNone     = "NONE"
	gceAffinityTypeClientIP = "CLIENT_IP"
)

// GetBackendService retrieves a backend by name.
func (gce *Client) GetBackendService(bsName string) (*compute.BackendService, error) {
	return gce.service.BackendServices.Get(gce.projectID, bsName).Do()
}

// CreateBackendService creates backend service based on NEG
func (gce *Client) CreateBackendService(bsName, negName, hcName, zone, affinity string, cdn bool) error {
	req, err := http.NewRequest(
		"POST",
		fmt.Sprintf("%s/projects/%s/global/backendServices", googleComputeAPIHost, gce.projectID),
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
			gce.makeHealthCheckURL(hcName),
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
	sop, err := parseSimpleOperation(res)
	if err != nil {
		return err
	}
	if simpleOperationIsDone(sop) {
		glog.Infof("No wating for backend service %s creation finished", bsName)
		return nil
	}
	op, err := gce.service.GlobalOperations.Get(gce.projectID, sop.ID).Do()
	if err != nil {
		return err
	}
	glog.Infof("Waiting for backend service %s creation finished", bsName)
	return gce.waitForGlobalOp(op)
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
