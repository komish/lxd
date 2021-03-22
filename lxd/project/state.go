package project

import (
	"fmt"
	"strconv"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared/units"
	"github.com/pkg/errors"
)

// GetCurrentAllocations returns the current resource utilization for a given
// project.
func GetCurrentAllocations(tx *db.ClusterTx, projectName string) (map[string]string, error) {
	result := map[string]string{
		"disk":             "0",
		"memory":           "0",
		"containers":       "0",
		"virtual-machines": "0",
		"cpu":              "0",
		"processes":        "0",
		"networks":         "0",
	}

	info, err := fetchProject(tx, projectName, false)
	if err != nil {
		return nil, err
	}

	// if the info is empty, we assume no allocations
	if info == nil {
		return result, nil
	}

	info.Instances = expandInstancesConfigAndDevices(info.Instances, info.Profiles)
	totals, err := getTotalsAcrossProjectEntities(info,
		[]string{"limits.memory", "limits.cpu", "limits.processes"},
	)
	if err != nil {
		return result, err
	}

	// calculate volume storage
	var totalVolumeSize int64
	for _, volume := range info.Volumes {
		sizeString, ok := volume.Config["size"]
		if !ok {
			return result, fmt.Errorf("unable to determine volume state on volume with no size config key")
		}

		size, err := units.ParseByteSizeString(sizeString)
		if err != nil {
			return result, err
		}

		totalVolumeSize += size
	}

	// calculate instance root disk storage
	rootDisks := make([]string, len(info.Instances))
	for i, instance := range info.Instances {
		root := instance.Devices["root"]
		rootSize, ok := root["size"]
		if !ok {
			return result, fmt.Errorf("Failed to get root disk size for instance %q in project :q", instance, projectName)
		}

		rootDisks[i] = rootSize
	}

	var calculatedRootDiskSize int64
	for _, rootDisk := range rootDisks {
		// convert values and sum
		size, err := units.ParseByteSizeString(rootDisk)
		if err != nil {
			return result, errors.Wrapf(err, "Failed to determine root disk usage %q", projectName)
		}
		calculatedRootDiskSize += size
	}

	// calculate image storage size
	filter := db.ImageFilter{
		Project: projectName,
	}

	images, err := tx.GetImages(filter)
	if err != nil {
		return result, err
	}

	var imageSize int64

	for _, image := range images {
		imageSize += image.Size
	}

	// count the instance types
	var containerCount int64
	var vmCount int64
	for _, inst := range info.Instances {
		switch inst.Type {
		case instancetype.Container:
			containerCount++
		case instancetype.VM:
			vmCount++
		default:
			// theoretically we should never hit this case
			return result, fmt.Errorf("Unexpected instance type %q", inst.Type.String())
		}
	}

	// use printers for response
	cpuPrinter := aggregateLimitConfigValuePrinters["limits.cpu"]
	processesPrinter := aggregateLimitConfigValuePrinters["limits.processes"]
	memoryPrinter := aggregateLimitConfigValuePrinters["limits.memory"]
	diskPrinter := aggregateLimitConfigValuePrinters["limits.disk"]

	// set values just before returning
	result["cpu"] = cpuPrinter(totals["limits.cpu"])
	result["memory"] = memoryPrinter(totals["limits.memory"])
	result["processes"] = processesPrinter(totals["limits.processes"])
	result["containers"] = strconv.FormatInt(containerCount, 10)
	result["virtual-machines"] = strconv.FormatInt(vmCount, 10)
	result["disk"] += diskPrinter(totalVolumeSize + calculatedRootDiskSize + imageSize)

	return result, nil
}
