package util

import (
	"bytes"
	"errors"
	"os/exec"
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

func ExecCommand(arguments []string) error {
	cmd := exec.Command(arguments[0], arguments[1:]...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return errors.New(stderr.String())
	}

	return nil
}

func IsAlreadyExistsError(err error) bool {
	return strings.Contains(err.Error(), "already exists")
}

// Take a GCE instance 'hostname' and break it down to something that can be fed
// to the GCE API client library.  Basically this means reducing 'kubernetes-
// minion-2.c.my-proj.internal' to 'kubernetes-minion-2' if necessary.
func NormalizeInstanceName(name string) string {
	return strings.Split(name, ".")[0]
}
