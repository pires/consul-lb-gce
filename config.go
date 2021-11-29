package main

import "fmt"

type healthCheckConfiguration struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type tagConfiguration struct {
	// todo(max): Add names of resources to reuse
	ReuseResources           bool                     `json:"reuseResources"`
	NetworkEndpointGroupName string                   `json:"networkEndpointGroupName"`
	HealthCheck              healthCheckConfiguration `json:"healthCheck"`
}

type consulConfiguration struct {
	URL string `json:"url"`
}

type cloudConfiguration struct {
	Project string `json:"project"`
	Network string `json:"network"`
	Zone    string `json:"zone"`
	URLMap  string `json:"urlMap"`
}

type configuration struct {
	Tags   map[string]tagConfiguration `json:"tags"`
	Consul consulConfiguration         `json:"consul"`
	Cloud  cloudConfiguration          `json:"cloud"`
}

func (c *configuration) GetTagConfiguration(tag string) (tagConfiguration, error) {
	if v, ok := c.Tags[tag]; ok {
		return v, nil
	}
	return tagConfiguration{}, fmt.Errorf("Tag %s is not found", tag)
}

func (c *configuration) GetTagNames() []string {
	names := []string{}
	for k := range c.Tags {
		names = append(names, k)
	}
	return names
}
