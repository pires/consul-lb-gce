package util

import "testing"

func TestZonify(t *testing.T) {
	if Zonify("zone", "name") != "zone-name" {
		t.Fail()
	}
}

func TestUnzonify(t *testing.T) {
	if Unzonify("zone-name", "zone") != "name" {
		t.Fail()
	}
}