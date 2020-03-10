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
		"description": "Managed by consul-lb-google",
		"defaultPort": 80,
		"networkEndpointType": "GCE_VM_IP_PORT",
		"network": "%s"
	}`, name, network)))
}

func (gce *Client) makeCreateHealthCheckBody(name, path string) *bytes.Buffer {
	return bytes.NewBuffer([]byte(fmt.Sprintf(`{
		"name": "%s",
		"description": "Managed by consul-lb-google",
		"kind": "compute#healthCheck",
		"type": "HTTP",
		"httpHealthCheck": {
    		"portSpecification": "USE_SERVING_PORT",
    		"requestPath": "%s"
		},
		"timeoutSec": 2,
		"checkIntervalSec": 2,
  		"healthyThreshold": 2,
		"unhealthyThreshold": 2
	}`, name, path)))
}

func (gce *Client) makeCreateBackendServiceBody(name, groupName, healthCheckName, zone string, cdn bool, affinity string) *bytes.Buffer {
	return bytes.NewBuffer([]byte(fmt.Sprintf(`{
  		"name": "%s",
  		"description": "Managed by consul-lb-google",
  		"backends": [
			{
      			"group": "%s",
      			"balancingMode": "RATE",
      			"maxRatePerEndpoint": 5
    		}
		],
  		"healthChecks": [
			"%s"
		],
  		"enableCDN": %t,
  		"sessionAffinity": "%s"
	}`, name, gce.makeNetworkEndpointGroupURL(groupName, zone), gce.makeHealthCheckURL(healthCheckName), cdn, affinity)))
}
