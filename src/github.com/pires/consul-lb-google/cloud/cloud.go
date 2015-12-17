package cloud

import (
	"errors"
	"strings"

	"github.com/golang/glog"
	"github.com/pires/consul-lb-google/cloud/gce"
)

var (
	ErrCantCreateInstanceGroup = errors.New("Can't create instance group")
	ErrCantRemoveInstanceGroup = errors.New("Can't remove instance group")
	ErrUnknownInstanceGroup    = errors.New("Unknown instance group")
	ErrUnknownInstanceInGroup  = errors.New("Unknown instance in instance group")
)

type Cloud interface {
	// CreateInstanceGroup creates an instance group
	CreateInstanceGroup(groupName string) error

	// RemoveInstanceGroup removes an instance group
	RemoveInstanceGroup(groupName string) error

	// AddInstanceToInstanceGroup adds an instance to an instance group
	//	AddInstanceToInstanceGroup(instanceName string, groupName string) error

	// RemoveInstanceFromInstanceGroup removes an instance from an instance group
	//	RemoveInstanceFromInstanceGroup(instanceName string, groupName string) error
}

type instanceGroup struct {
	// name of the instance group
	name string
	// map of instances in the instance group
	instances map[string]string
	// the name of the firewall allow rule related to this instance group
	fwRule string
}

type gceCloud struct {
	// GCE client
	client *gce.GCEClient

	// zones available to this project
	zones []string

	// one instance group identifier represents n instance groups, one per available zone
	// e.g. groups := instanceGroups["myIG"]["europe-west1-d"]
	instanceGroups map[string]map[string]*instanceGroup
}

func New(projectID string, networkURL string, allowedZones []string) (Cloud, error) {
	// try and provision GCE client
	c, err := gce.CreateGCECloud(projectID, networkURL)
	if err != nil {
		return nil, err
	}

	return &gceCloud{
		client:         c,
		zones:          allowedZones,
		instanceGroups: make(map[string]map[string]*instanceGroup),
	}, nil
}

func (c *gceCloud) CreateInstanceGroup(groupName string) error {
	// create one instance-group per zone
	cleanup := false
	glog.Infof("Creating instance groups for [%s]..", groupName)
	for _, zone := range c.zones {
		finalGroupName := zonifyName(zone, groupName)
		if err := c.client.CreateInstanceGroupForZone(finalGroupName, zone /*TODO define ports*/, make(map[string]int64)); err == nil {
			glog.Infof("Created instance group [%s] in zone [%s].", finalGroupName, zone)
			m := make(map[string]*instanceGroup, 1)
			m[finalGroupName] = &instanceGroup{} // empty
			c.instanceGroups[zone] = m
		} else {
			glog.Errorf("There was an error creating instance group [%s] in zone [%s]. Error: %#v", finalGroupName, zone, err)
			cleanup = true
			break
		}
	}

	// need to clean-up?
	if cleanup {
		glog.Warningf("Rollback instance group creation for [%s]..", groupName)
		// delete created instance groups
		c.RemoveInstanceGroup(groupName)
		return ErrCantCreateInstanceGroup
	}

	glog.Infof("Creating instance groups for [%s] completed successfully", groupName)

	return nil
}

func (c *gceCloud) RemoveInstanceGroup(groupName string) error {
	// remove one instance-group per zone
	cleanup := false
	glog.Infof("Removing instance groups for [%s]..", groupName)
	// delete created instance groups
	for _, zone := range c.zones {
		if _, ok := c.instanceGroups[zone]; ok {
			finalGroupName := zonifyName(zone, groupName)
			if err := c.client.DeleteInstanceGroupForZone(finalGroupName, zone); err == nil {
				glog.Warningf("Removed instance group [%s] from zone [%s].", finalGroupName, zone)
			} else {
				glog.Errorf("HUMAN INTERVERTION REQUIRED: Failed to remove instance group [%s] from zone [%s]. Error: %#v", finalGroupName, zone)
				cleanup = true
			}
		}
	}

	if cleanup {
		return ErrCantRemoveInstanceGroup
	}

	glog.Infof("Removing instance groups for [%s] completed successfully", groupName)

	return nil
}

func zonifyName(zone string, name string) string {
	return strings.Join([]string{zone, name}, "-")
}
