package gce

import (
	"fmt"
	"strings"

	"google.golang.org/api/compute/v1"
)

// UpdateURLMap updates a url map using the given backend service as the default service.
func (gce *Client) UpdateURLMap(urlMapName, bsName, host, path string) error {
	urlMap, err := gce.service.UrlMaps.Get(gce.projectID, urlMapName).Do()
	if err != nil {
		return err
	}
	backend, err := gce.service.BackendServices.Get(gce.projectID, bsName).Do()
	if err != nil {
		return err
	}

	pathMatcherName := strings.Split(host, ".")[0]

	// create path matcher if it doesn't exist
	var existingHostRule *compute.HostRule
	for _, hr := range urlMap.HostRules {
		theSameHost := false
		for _, h := range hr.Hosts {
			if h == host {
				theSameHost = true
				break
			}
		}
		if theSameHost {
			existingHostRule = hr
			break
		}
	}
	if existingHostRule == nil {
		// create path matcher if it doesn't exist
		var existingPathMatcher *compute.PathMatcher
		for _, pm := range urlMap.PathMatchers {
			if pm.Name == pathMatcherName {
				existingPathMatcher = pm
				break
			}
		}
		if existingPathMatcher == nil {
			var defaultServiceLink string
			if path == "/" {
				defaultServiceLink = backend.SelfLink
			} else {
				defaultServiceLink = urlMap.DefaultService
			}

			urlMap.PathMatchers = append(urlMap.PathMatchers, &compute.PathMatcher{
				Name:           pathMatcherName,
				DefaultService: defaultServiceLink,
				PathRules: []*compute.PathRule{
					makePathRule(path, backend.SelfLink),
				},
			})
		}

		urlMap.HostRules = append(urlMap.HostRules, &compute.HostRule{
			Hosts:       []string{host},
			PathMatcher: pathMatcherName,
			Description: host,
		})
	}

	if existingHostRule == nil {
		op, err := gce.service.UrlMaps.Update(gce.projectID, urlMap.Name, urlMap).Do()
		if err != nil {
			return err
		}
		return gce.waitForGlobalOp(op)
	}

	return nil
}

func makePathRule(path string, backend string) *compute.PathRule {
	if path == "/" {
		return nil
	}

	if path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}

	paths := []string{
		path,
		path + "/*",
	}

	return &compute.PathRule{
		Paths:   paths,
		Service: backend,
	}
}

func (gce *Client) makeBackendServiceURL(bs, zone string) string {
	if zone == "global" {
		return fmt.Sprintf("%s/projects/%s/global/backendServices/%s", googleComputeAPIHost, gce.projectID, bs)
	}
	return ""
}
