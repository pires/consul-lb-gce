package main

import (
	"fmt"
	"regexp"
)

type tagInfo struct {
	Tag      string
	CDN      bool
	Affinity string
	Host     string
	Path     string
}

type tagParser struct {
	prefix string
	regexp *regexp.Regexp
}

func (p *tagParser) Parse(tag string) (*tagInfo, error) {
	matched := p.regexp.FindStringSubmatch(tag)
	if matched == nil {
		return nil, fmt.Errorf("Can't parse tag: %s", tag)
	}

	parsed := make(map[string]string)
	for i, name := range p.regexp.SubexpNames() {
		if i != 0 && name != "" {
			parsed[name] = matched[i]
		}
	}

	return &tagInfo{
		Tag:      tag,
		CDN:      parsed["cdn"] == "cdn",
		Affinity: parsed["affinity"],
		Host:     parsed["host"],
		Path:     parsed["path"],
	}, nil
}

func newTagParser(prefix string) *tagParser {
	return &tagParser{
		prefix: prefix,
		regexp: regexp.MustCompile(
			fmt.Sprintf(
				"^%s(?P<cdn>cdn|nocdn):(?P<affinity>(no|ip|ipport)affinity):(?P<host>[a-z0-9-\\.]+)(?P<path>/.*)$",
				prefix,
			),
		),
	}
}
