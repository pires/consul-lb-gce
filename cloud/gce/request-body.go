package gce

import (
	"bytes"
	"fmt"
	"strings"
)

func (gce *Client) makeAttachOrDetachNetworkEndpointsBody(endpoints []NetworkEndpoint, zone string) *bytes.Buffer {
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

func (gce *Client) makeCreateNetworkEndpointGroupBody(name, network string) *bytes.Buffer {
	return bytes.NewBuffer([]byte(fmt.Sprintf(`{
		"name": "%s",
		"description": "Managed by consul-lb-gce",
		"defaultPort": 80,
		"networkEndpointType": "GCE_VM_IP_PORT",
		"network": "%s"
	}`, name, network)))
}

func (gce *Client) makeCreateHealthCheckBody(name, path string) *bytes.Buffer {
	return bytes.NewBuffer([]byte(fmt.Sprintf(`{
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
	}`, name, path)))
}
