package gce

import (
	"net/http"

	"github.com/dffrntmedia/consul-lb-gce/util"
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

// CreateBackendService creates backend service in specified zone based on NEG.
func (gce *Client) CreateBackendService(bsName, negName, hcName, zone, affinity string, cdn bool) error {
	request, err := http.NewRequest(
		"POST",
		gce.makeCreateBackendServiceURL(),
		gce.makeCreateBackendServiceBody(
			bsName,
			negName,
			hcName,
			zone,
			cdn,
			getAffinityOption(affinity),
		),
	)
	if err != nil {
		return err
	}
	request.Header.Add("Content-Type", "application/json")
	_, err = util.SendHTTPRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})
	return err
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
