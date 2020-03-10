package main

import "fmt"

type tagParserConfiguration struct {
	TagPrefix string `toml:"tag_prefix"`
}

type consulConfiguration struct {
	URL         string   `toml:"url"`
	TagsToWatch []string `toml:"tags_to_watch"`
	// NOTE: We specify it explicitly in configuration coz Consul doesn't provide this information via API
	HealthChecksPaths map[string]string `toml:"health_checks_paths"`
}

type cloudConfiguration struct {
	Project      string
	Network      string
	AllowedZones []string `toml:"allowed_zones"`
	URLMap       string   `toml:"url_map"`
}

type configuration struct {
	TagParser tagParserConfiguration
	Consul    consulConfiguration
	Cloud     cloudConfiguration
}

func (c *consulConfiguration) GetHealthCheckPath(tag string) (string, error) {
	if v, ok := c.HealthChecksPaths[tag]; ok {
		return v, nil
	}
	return "", fmt.Errorf("Health check path is not provided for tag %s", tag)
}
