package gce

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/dffrntmedia/consul-lb-gce/util"
	"github.com/golang/glog"
)

// CreateHTTPHealthCheck creates http health check
func (gce *Client) CreateHTTPHealthCheck(hcName string, path string) error {
	if path == "" {
		path = "/"
	}

	request, err := http.NewRequest(
		"POST",
		fmt.Sprintf("%s/projects/%s/global/healthChecks", googleComputeAPIHost, gce.projectID),
		bytes.NewBuffer([]byte(fmt.Sprintf(`{
			"name": "%s",
			"description": "Managed by consul-lb-gce",
			"kind": "compute#healthCheck",
			"type": "HTTP",
			"httpHealthCheck": {
				"portSpecification": "USE_SERVING_PORT",
				"requestPath": "%s"
			},
			"timeoutSec": 5,
			"checkIntervalSec": 10,
			"healthyThreshold": 2,
			"unhealthyThreshold": 3
		}`, hcName, path))),
	)
	if err != nil {
		return err
	}
	request.Header.Add("Content-Type", "application/json")

	res, err := util.SendHTTPRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusConflict {
		glog.Infof("Health check %s alredy exists", hcName)
		return nil
	}
	return gce.waitForOpFromHTTPResponse(res, "global", fmt.Sprintf("health check %s creation", hcName))
}
