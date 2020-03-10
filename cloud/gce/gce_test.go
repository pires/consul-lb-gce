package gce

import (
	"testing"
)

func TestMakePathRule(t *testing.T) {
	backend := "backend.service"

	path := "/"
	rule := makePathRule(path, backend)
	if rule != nil {
		t.Fail()
	}

	path = "/test"
	rule = makePathRule(path, backend)
	paths := rule.Paths
	if !contain(paths, path) || !contain(paths, path+"/*") || len(paths) > 2 {
		t.Fail()
	}
	if rule.Service != backend {
		t.Fail()
	}
}
