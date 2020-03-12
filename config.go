package main

import "fmt"

type tagParserConfiguration struct {
	Prefix string `json:"prefix"`
}

type healthCheckConfiguration struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type tagConfiguration struct {
	Name                    string                   `json:"name"`
	HealthCheck             healthCheckConfiguration `json:"healthCheck"`
	SecondaryBackendService string                   `json:"secondaryBackendService,omitempty"`
}

type consulConfiguration struct {
	URL  string             `json:"url"`
	Tags []tagConfiguration `json:"tags"`
}

type cloudConfiguration struct {
	Project string `json:"project"`
	Network string `json:"network"`
	Zone    string `json:"zone"`
	URLMap  string `json:"urlMap"`
}

type configuration struct {
	TagParser tagParserConfiguration `json:"tagParser"`
	Consul    consulConfiguration    `json:"consul"`
	Cloud     cloudConfiguration     `json:"cloud"`
}

func (c *consulConfiguration) GetHealthCheck(tag string) (healthCheckConfiguration, error) {
	for _, t := range c.Tags {
		if t.Name == tag {
			return t.HealthCheck, nil
		}
	}
	return healthCheckConfiguration{}, fmt.Errorf("Health check path is not provided for tag %s", tag)
}

func (c *consulConfiguration) GetTagNames() []string {
	names := []string{}
	for _, t := range c.Tags {
		names = append(names, t.Name)
	}
	return names
}
