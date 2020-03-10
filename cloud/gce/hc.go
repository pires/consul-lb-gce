package gce

import (
	"net/http"

	"github.com/dffrntmedia/consul-lb-gce/util"
)

// CreateHTTPHealthCheck creates http health check.
func (gce *Client) CreateHTTPHealthCheck(hcName string, path string) error {
	if path == "" {
		path = "/"
	}

	request, err := http.NewRequest("POST", gce.makeCreateHealthCheckURL(), gce.makeCreateHealthCheckBody(hcName, path))

	if err != nil {
		return err
	}

	request.Header.Add("Content-Type", "application/json")

	_, err = util.SendHTTPRequest(gce.httpClient, request, []int{http.StatusOK, http.StatusConflict})

	if err != nil {
		return err
	}

	return nil
}
