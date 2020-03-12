package main

import (
	"fmt"
	"regexp"
	"strings"
)

type tagInfo struct {
	Tag      string
	CDN      bool
	Affinity string
	Host     string
	Path     string
}

var tagRegexp = regexp.MustCompile("^consullbgce-(?P<cdn>cdn|nocdn):(?P<affinity>(no|ip|ipport)affinity):(?P<host>[a-z0-9-\\.]+)(?P<path>/.*)$")

func parseTag(tag string) (*tagInfo, error) {
	matched := tagRegexp.FindStringSubmatch(tag)
	if matched == nil {
		return nil, fmt.Errorf("Malformed tag: %s", tag)
	}

	parsed := make(map[string]string)
	for i, name := range tagRegexp.SubexpNames() {
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

func (info *tagInfo) String() string {
	var cdn string
	if info.CDN {
		cdn = "cdn"
	} else {
		cdn = "nocdn"
	}
	normalizedHost := strings.Replace(info.Host, ".", "-", -1)
	return fmt.Sprintf("%s-%s-%s", cdn, info.Affinity, normalizedHost)
}
