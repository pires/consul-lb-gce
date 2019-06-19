package tagparser

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/golang/glog"
)

var (
	ErrCantParseTag = errors.New("Can't parse tag")
)

type TagInfo struct {
	Tag      string
	Cdn      bool
	Affinity string
	Host     string
	Path     string
}

type TagParser interface {
	Parse(tag string) (TagInfo, error)
}

type TagParserImpl struct {
	tagPrefix string
	tagRegExp *regexp.Regexp
}

func (parser *TagParserImpl) Parse(tag string) (TagInfo, error) {
	glog.Infof("Parse agrs: %s, %s", tag, parser.tagRegExp)

	matched := parser.tagRegExp.FindStringSubmatch(tag)

	if matched == nil {
		return TagInfo{}, ErrCantParseTag
	}

	parsed := make(map[string]string)

	for i, name := range parser.tagRegExp.SubexpNames() {
		if i != 0 && name != "" {
			parsed[name] = matched[i]
		}
	}

	return TagInfo{
		Tag:      tag,
		Cdn:      parsed["cdn"] == "cdn",
		Affinity: parsed["affinity"],
		Host:     parsed["host"],
		Path:     parsed["path"],
	}, nil
}

func New(tagPrefix string) TagParser {
	return &TagParserImpl{
		tagPrefix: tagPrefix,
		tagRegExp: regexp.MustCompile(fmt.Sprintf("^%s(?P<cdn>cdn|nocdn):(?P<affinity>(no|ip|ipport)affinity):(?P<host>[a-z0-9-\\.]+)(?P<path>/.*)$", tagPrefix)),
	}
}
