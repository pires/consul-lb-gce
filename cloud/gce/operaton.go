package gce

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dffrntmedia/consul-lb-gce/util"
	"github.com/golang/glog"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	operationPollInterval        = 3 * time.Second
	operationPollTimeoutDuration = 30 * time.Minute
)

type simpleOperation struct {
	ID     string
	Status string
}

func waitForOp(op *compute.Operation, getOperation func() (*compute.Operation, error)) error {
	if op == nil {
		return fmt.Errorf("operation must not be nil")
	}

	if opIsDone(op) {
		return getErrorFromOp(op)
	}

	return wait.Poll(operationPollInterval, operationPollTimeoutDuration, func() (bool, error) {
		pollOp, err := getOperation()
		return opIsDone(pollOp), err
	})
}

func opIsDone(op *compute.Operation) bool {
	return op != nil && op.Status == "DONE"
}

func getErrorFromOp(op *compute.Operation) error {
	if op != nil && op.Error != nil && len(op.Error.Errors) > 0 {
		return &googleapi.Error{
			Code:    int(op.HttpErrorStatusCode),
			Message: op.Error.Errors[0].Message,
		}
	}

	return nil
}

func (gce *Client) waitForGlobalOp(op *compute.Operation) error {
	return waitForOp(op, func() (*compute.Operation, error) {
		return gce.service.GlobalOperations.Get(gce.projectID, op.Name).Do()
	})
}

// waitForOp waits for operation finished.
// if zone is "global" then operation is considered as global.
func (gce *Client) waitForOp(id string, zone string) error {
	var opProvider func() (*compute.Operation, error)
	if zone == "global" {
		opProvider = func() (*compute.Operation, error) {
			return gce.service.GlobalOperations.Get(gce.projectID, id).Do()
		}
	} else {
		opProvider = func() (*compute.Operation, error) {
			return gce.service.ZoneOperations.Get(gce.projectID, zone, id).Do()
		}
	}
	op, err := opProvider()
	if err != nil {
		return err
	}
	return waitForOp(op, opProvider)
}

func (gce *Client) waitForOpFromHTTPResponse(res *http.Response, zone string, opDesc string) error {
	var op simpleOperation
	if err := util.ParseBody(res.Body, &op); err != nil {
		return err
	}
	if op.Status == "DONE" {
		glog.Infof("Operation '%s' finished during request", opDesc)
		return nil
	}
	glog.Infof("Waiting for '%s' operation finished", opDesc)
	return gce.waitForOp(op.ID, zone)
}
