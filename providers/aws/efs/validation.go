package efs

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// maxProvisionedMibps is the highest ProvisionedThroughputInMibps EFS accepts.
const maxProvisionedMibps = 3414

// validateFileSystemModes checks the performance and throughput settings of a
// new file system. maxIO can't be combined with One Zone or Elastic throughput.
func validateFileSystemModes(perf, tput string, mibps float64, zone string) error {
	switch perf {
	case driver.PerformanceGeneralPurpose:
	case driver.PerformanceMaxIO:
		if zone != "" {
			return errors.New(errors.InvalidArgument, "One Zone file systems don't support the maxIO performance mode")
		}
	default:
		return errors.Newf(errors.InvalidArgument, "invalid PerformanceMode %q", perf)
	}

	return validateThroughput(perf, tput, mibps)
}

// validateThroughput checks a throughput mode and its provisioned value.
func validateThroughput(perf, tput string, mibps float64) error {
	switch tput {
	case driver.ThroughputBursting:
	case driver.ThroughputElastic:
		if perf == driver.PerformanceMaxIO {
			return errors.New(errors.InvalidArgument, "elastic throughput isn't supported with the maxIO performance mode")
		}
	case driver.ThroughputProvisioned:
		if mibps <= 0 {
			return errors.New(errors.InvalidArgument,
				"ProvisionedThroughputInMibps is required when ThroughputMode is provisioned")
		}
	default:
		return errors.Newf(errors.InvalidArgument, "invalid ThroughputMode %q", tput)
	}

	if mibps < 0 || mibps > maxProvisionedMibps {
		return errors.Newf(errors.InvalidArgument,
			"ProvisionedThroughputInMibps must be between 1 and %d", maxProvisionedMibps)
	}

	return nil
}

// validateUpdate checks the state an UpdateFileSystem call would leave behind.
// Switching to provisioned needs a new throughput value.
func validateUpdate(fs *driver.FileSystem, in driver.UpdateFileSystemInput) error {
	tput := fs.ThroughputMode
	if in.ThroughputMode != "" {
		tput = in.ThroughputMode
	}

	mibps := in.ProvisionedThroughputInMibps
	if mibps == 0 && tput == driver.ThroughputProvisioned && fs.ThroughputMode == driver.ThroughputProvisioned {
		mibps = fs.ProvisionedThroughputInMibps
	}

	return validateThroughput(fs.PerformanceMode, tput, mibps)
}
