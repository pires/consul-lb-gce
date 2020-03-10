package main

import (
	"fmt"
	"regexp"
)

type TagInfo struct {
	Tag      string
	CDN      bool
	Affinity string
	Host     string
	Path     string
}

type TagParser struct {
	tagPrefix string
	tagRegExp *regexp.Regexp
}

func (parser *TagParser) Parse(tag string) (*TagInfo, error) {
	matched := parser.tagRegExp.FindStringSubmatch(tag)
	if matched == nil {
		return nil, fmt.Errorf("Can't parse tag: %s", tag)
	}

	parsed := make(map[string]string)
	for i, name := range parser.tagRegExp.SubexpNames() {
		if i != 0 && name != "" {
			parsed[name] = matched[i]
		}
	}

	return &TagInfo{
		Tag:      tag,
		CDN:      parsed["cdn"] == "cdn",
		Affinity: parsed["affinity"],
		Host:     parsed["host"],
		Path:     parsed["path"],
	}, nil
}

func newTagParser(tagPrefix string) *TagParser {
	return &TagParser{
		tagPrefix: tagPrefix,
		tagRegExp: regexp.MustCompile(fmt.Sprintf("^%s(?P<cdn>cdn|nocdn):(?P<affinity>(no|ip|ipport)affinity):(?P<host>[a-z0-9-\\.]+)(?P<path>/.*)$", tagPrefix)),
	}
}
