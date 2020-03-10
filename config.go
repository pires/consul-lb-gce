package main

type tagParserConfiguration struct {
	TagPrefix string `toml:"tag_prefix"`
}

type consulConfiguration struct {
	URL         string   `toml:"url"`
	TagsToWatch []string `toml:"tags_to_watch"`
	// NOTE: Since we can't retrieve health checks definitions from Consul we must explicitly define them in configuration
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
