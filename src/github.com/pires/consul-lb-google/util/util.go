package util

import (
	"strings"
)

// Zonify takes a specified name and prepends a specified zone plus an hyphen
// e.g. zone == "us-east1-d" && name == "myname", returns "us-east1-d-myname"
func Zonify(zone, name string) string {
	return strings.Join([]string{zone, name}, "-")
}

// Unzonify takes a zonified name and removes the zone prefix.
// e.g. name == "us-east1-d-myname" && zone == "us-east1-d", returns "myname"
func Unzonify(name string, zone string) string {
	return strings.TrimPrefix(name, zone+"-")
}
