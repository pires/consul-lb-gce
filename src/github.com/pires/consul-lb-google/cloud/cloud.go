package cloud

import (
	"errors"
	"strings"

	"github.com/golang/glog"
	"github.com/pires/consul-lb-google/cloud/gce"
)

var (
	ErrCantCreateInstanceGroup     = errors.New("Can't create instance group")
	ErrCantRemoveInstanceGroup     = errors.New("Can't remove instance group")
	ErrUnknownInstanceGroup        = errors.New("Unknown instance group")
	ErrUnknownInstanceInGroup      = errors.New("Unknown instance in instance group")
	ErrCantSetPortForInstanceGroup = errors.New("Can't set port for instance group")
)

type Cloud interface {
	// CreateInstanceGroup creates an instance group
	CreateInstanceGroup(groupName string, managedZone, globalAddressName, host string) error

	// RemoveInstanceGroup removes an instance group
	RemoveInstanceGroup(groupName string) error

	// AddInstancesToInstanceGroup adds a sert of instances to an instance group
	AddInstancesToInstanceGroup(instanceNames []string, groupName string) error

	// RemoveInstancesFromInstanceGroup removes a set of instances from an instance group
	RemoveInstancesFromInstanceGroup(instanceNames []string, groupName string) error

	// SetPortForInstanceGroup sets the port on an instance group
	SetPortForInstanceGroup(port int64, groupName string) error

	// UpdateLoadBalancer updates existing load-balancer related to an instance group
	UpdateLoadBalancer(urlMapName, groupName string, port string, healthCheckPath, host, path string) error

	// RemoveLoadBalancer removes an existing load-balancer related to an instance group
	//RemoveLoadBalancer(groupName string) error
}

type instanceGroup struct {
	// name of the instance group
	name string
	// map of instances in the instance group
	instances map[string]string
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

func New(projectID string, network string, allowedZones []string) (Cloud, error) {
	// try and provision GCE client
	c, err := gce.CreateGCECloud(projectID, network)
	if err != nil {
		return nil, err
	}

	return &gceCloud{
		client:         c,
		zones:          allowedZones,
		instanceGroups: make(map[string]map[string]*instanceGroup),
	}, nil
}

func (c *gceCloud) CreateInstanceGroup(groupName string, managedZone, globalAddressName, host string) error {
	// create one instance-group per zone
	cleanup := false
	glog.Infof("Creating instance groups for [%s]..", groupName)
	for _, zone := range c.zones {
		finalGroupName := zonify(zone, groupName)
		glog.Infof("Creating instance group [%s] in zone [%s].", finalGroupName, zone)
		if err := c.client.CreateInstanceGroupForZone(finalGroupName, zone /*TODO define ports*/, make(map[string]int64)); err == nil {
			m := make(map[string]*instanceGroup, 1)
			m[finalGroupName] = &instanceGroup{} // empty
			c.instanceGroups[zone] = m
		} else {
			_, err = c.client.GetInstanceGroupForZone(finalGroupName, zone)

			if err != nil {
				glog.Errorf("There was an error creating instance group [%s] in zone [%s]. Error: %s", finalGroupName, zone, err)
				cleanup = true
				break
			}
		}
	}

	// need to clean-up?
	if cleanup {
		glog.Warningf("Rollback instance group creation for [%s]..", groupName)
		// delete created instance groups
		c.RemoveInstanceGroup(groupName)
		return ErrCantCreateInstanceGroup
	}

	// todo(max): uncomment it later
	// it's commented because of error: 'googleapi: Error 403: Request had insufficient authentication scopes., forbidden'
	//if err := c.client.AddDnsRecordSet(managedZone, globalAddressName, host); err != nil {
	//	return err
	//}
	//glog.Infof("Created DNS record set successfully [%s]", host)

	glog.Infof("Created instance groups for [%s] successfully", groupName)

	return nil
}

func (c *gceCloud) RemoveInstanceGroup(groupName string) error {
	// remove one instance-group per zone
	cleanup := false
	glog.Infof("Removing instance groups for [%s]..", groupName)
	// delete created instance groups
	for _, zone := range c.zones {
		if _, ok := c.instanceGroups[zone]; ok {
			finalGroupName := zonify(zone, groupName)
			if err := c.client.DeleteInstanceGroupForZone(finalGroupName, zone); err == nil {
				glog.Warningf("Removed instance group [%s] from zone [%s].", finalGroupName, zone)
			} else {
				glog.Errorf("HUMAN INTERVERTION REQUIRED: Failed to remove instance group [%s] from zone [%s]. Error: %s", finalGroupName, zone, err)
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

func (c *gceCloud) AddInstancesToInstanceGroup(instanceNames []string, groupName string) error {
	glog.Infof("Adding %d instances into instance group [%s]", len(instanceNames), groupName)

	// since instance names are globally unique, we just need to care about matching provided
	// instance names to each zone.
	// let's do it on a per-zone basis.
	// remember instance names were zonified before added to instance group.
	for _, zone := range c.zones {
		// instance group name for this zone
		finalGroupName := zonify(zone, groupName)

		// get all instances in zone
		zoneInstances, err := c.client.ListInstancesInZone(zone)
		if err != nil {
			return err
		}

		// get all instances in group
		groupInstances, err := c.client.ListInstancesInInstanceGroupForZone(finalGroupName, zone)
		if err != nil {
			return err
		}

		var instancesToAddtoZone []string

		// for each instance, un-zonify its name so we can compare against provided instanceNames
		for _, zoneInstance := range zoneInstances.Items {
			// for each instanceName, if equals to unzonified name, add to group
			for _, instanceName := range instanceNames {
				if instanceName == zoneInstance.Name {
					// is instance already added to instance group?
					ignoreOp := false
					for _, groupInstance := range groupInstances.Items {
						// groupInstance.Instance is an instance URL, so replace is needed here
						split := strings.Split(groupInstance.Instance, "/")
						if split[len(split)-1] == zoneInstance.Name {
							ignoreOp = true
						}
					}
					if !ignoreOp {
						// use real instance name, meaning the zonified
						instancesToAddtoZone = append(instancesToAddtoZone, zoneInstance.Name)
					}
				}
			}
		}

		// are there any instances to add for this zone?
		total := len(instancesToAddtoZone)
		if total > 0 {
			glog.Infof("There are %d instances to add to instance group [%s] on zone [%s]. Adding..", total, groupName, zone)
			if err := c.client.AddInstancesToInstanceGroup(finalGroupName, instancesToAddtoZone, zone); err != nil {
				return err
			}
		} else {
			glog.Infof("There are no instances to add to instance group [%s] on zone [%s].", groupName, zone)
		}
	}

	glog.Infof("Added %d instances into instance group [%s]", len(instanceNames), groupName)

	return nil
}

func (c *gceCloud) RemoveInstancesFromInstanceGroup(instanceNames []string, groupName string) error {
	glog.Infof("Removing %d instances from instance group [%s]", len(instanceNames), groupName)

	// since instance names are globally unique, we just need to care about matching provided
	// instance names to each zone.
	// let's do it on a per-zone basis.
	// remember instance names were zonified before added to instance group.
	for _, zone := range c.zones {
		// instance group name for this zone
		finalGroupName := zonify(zone, groupName)

		// get all instances in group
		groupInstances, err := c.client.ListInstancesInInstanceGroupForZone(finalGroupName, zone)
		if err != nil {
			return err
		}

		var instancesToRemoveFromZone []string

		// for each instance in group compare against provided instanceNames
		for _, groupInstance := range groupInstances.Items {
			// compare with provided instanceNames
			for _, instanceName := range instanceNames {
				// groupInstance.Instance is an instance URL, so replace is needed here
				split := strings.Split(groupInstance.Instance, "/")
				if instanceName == split[len(split)-1] {
					// use real instance name, meaning the zonified
					instancesToRemoveFromZone = append(instancesToRemoveFromZone, instanceName)
				}
			}
		}

		// are there any instances to be removed from this zone?
		total := len(instancesToRemoveFromZone)
		if total > 0 {
			glog.Infof("There are %d instances to be removed from instance group [%s] on zone [%s]. Removing..", total, groupName, zone)
			if err := c.client.RemoveInstancesFromInstanceGroup(finalGroupName, instancesToRemoveFromZone, zone); err != nil {
				return err
			}
		} else {
			glog.Infof("There are no instances to be removed from instance group [%s] on zone [%s].", groupName, zone)
		}
	}

	return nil
}

func (c *gceCloud) SetPortForInstanceGroup(port int64, groupName string) error {
	glog.Infof("Setting instance group [%s] port [%d]..", groupName, port)

	success := true
	for _, zone := range c.zones {
		// instance group name for this zone
		finalGroupName := zonify(zone, groupName)

		// get all instances in group
		if err := c.client.SetPortToInstanceGroupForZone(finalGroupName, port, zone); err != nil {
			glog.Errorf("There was an error while setting port [%d] for instance group [%s] in zone [%s]. %s", port, finalGroupName, zone, err)
			success = false
		}
	}

	if !success {
		return ErrCantSetPortForInstanceGroup
	}

	return nil
}

func (c *gceCloud) UpdateLoadBalancer(urlMapName, groupName string, port string, healthCheckPath, host, path string) error {
	glog.Infof("Creating/updating load-balancer for [%s:%s].", groupName, port)
	err := c.client.UpdateLoadBalancer(urlMapName, groupName, port, healthCheckPath, host, path, c.zones)
	glog.Infof("Load-balancer [%s] created successfully.", groupName)
	return err
}

//func (c *gceCloud) RemoveLoadBalancer(groupName string) error {
//	glog.Infof("Removing load-balancer for [%s].", groupName)
//	err := c.client.RemoveLoadBalancer(groupName)
//	glog.Infof("Load-balancer [%s] removed successfully.", groupName)
//
//	return err
//}

//zonify takes a specified name and prepends a specified zone plus an hyphen
// e.g. zone == "us-east1-d" && name == "myname", returns "us-east1-d-myname"
func zonify(zone string, name string) string {
	return strings.Join([]string{zone, name}, "-")
}

// unzonify takes a specified supposedly zonified name and removes the zone prefix.
// e.g. name == "us-east1-d-myname" && zone == "us-east1-d", returns "myname"
func unzonify(name string, zone string) string {
	return strings.TrimPrefix(name, zone)
}
