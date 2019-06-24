package gce

import (
	"testing"
)

func TestGetPathRule(t *testing.T) {
	path := "/"
	backendServiceLink := "backend.service"

	pathRule := GetPathRule(path, backendServiceLink)

	if pathRule != nil {
		t.Fail()
	}

	path = "/test"

	pathRule = GetPathRule(path, backendServiceLink)

	paths := pathRule.Paths

	if !contain(paths, path) || !contain(paths, path+"/*") || len(paths) > 2 {
		t.Fail()
	}

	if pathRule.Service != backendServiceLink {
		t.Fail()
	}
}

func contain(arrayOfStrings []string, target string) bool {
	for _, s := range arrayOfStrings {
		if s == target {
			return true
		}
	}
	return false
}
