package efs

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// tag is the EFS wire tag shape ({Key, Value}).
type tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func tagsToMap(tags []tag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

func mapToTags(m map[string]string) []tag {
	out := make([]tag, 0, len(m))
	for k, v := range m {
		out = append(out, tag{Key: k, Value: v})
	}

	return out
}

// nanosPerSecond converts nanoseconds to the fractional epoch seconds EFS uses.
const nanosPerSecond = 1e9

// epochSeconds renders a time as the fractional epoch-seconds EFS uses for
// timestamps on the wire.
func epochSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / nanosPerSecond
}

// fileSystemSizeJSON is the SizeInBytes wire shape.
type fileSystemSizeJSON struct {
	Value           int64   `json:"Value"`
	Timestamp       float64 `json:"Timestamp,omitempty"`
	ValueInIA       int64   `json:"ValueInIA"`
	ValueInStandard int64   `json:"ValueInStandard"`
}

// fileSystemProtectionJSON is the FileSystemProtection wire shape.
type fileSystemProtectionJSON struct {
	ReplicationOverwriteProtection string `json:"ReplicationOverwriteProtection,omitempty"`
}

// fileSystemJSON is the EFS FileSystemDescription wire shape.
type fileSystemJSON struct {
	OwnerID                      string                    `json:"OwnerId"`
	CreationToken                string                    `json:"CreationToken"`
	FileSystemID                 string                    `json:"FileSystemId"`
	FileSystemARN                string                    `json:"FileSystemArn"`
	CreationTime                 float64                   `json:"CreationTime"`
	LifeCycleState               string                    `json:"LifeCycleState"`
	Name                         string                    `json:"Name,omitempty"`
	NumberOfMountTargets         int32                     `json:"NumberOfMountTargets"`
	SizeInBytes                  fileSystemSizeJSON        `json:"SizeInBytes"`
	PerformanceMode              string                    `json:"PerformanceMode"`
	Encrypted                    bool                      `json:"Encrypted"`
	KMSKeyID                     string                    `json:"KmsKeyId,omitempty"`
	ThroughputMode               string                    `json:"ThroughputMode"`
	ProvisionedThroughputInMibps float64                   `json:"ProvisionedThroughputInMibps,omitempty"`
	AvailabilityZoneName         string                    `json:"AvailabilityZoneName,omitempty"`
	AvailabilityZoneID           string                    `json:"AvailabilityZoneId,omitempty"`
	Tags                         []tag                     `json:"Tags"`
	FileSystemProtection         *fileSystemProtectionJSON `json:"FileSystemProtection,omitempty"`
}

func fileSystemToWire(fs *driver.FileSystem) fileSystemJSON {
	return fileSystemJSON{
		OwnerID:              fs.OwnerID,
		CreationToken:        fs.CreationToken,
		FileSystemID:         fs.FileSystemID,
		FileSystemARN:        fs.ARN,
		CreationTime:         epochSeconds(fs.CreationTime),
		LifeCycleState:       fs.LifeCycleState,
		Name:                 fs.Name,
		NumberOfMountTargets: fs.NumberOfMountTargets,
		SizeInBytes: fileSystemSizeJSON{
			Value:           fs.SizeInBytes.Value,
			Timestamp:       zeroOrEpoch(fs.SizeInBytes.Timestamp),
			ValueInIA:       fs.SizeInBytes.ValueInIA,
			ValueInStandard: fs.SizeInBytes.ValueInStandard,
		},
		PerformanceMode:              fs.PerformanceMode,
		Encrypted:                    fs.Encrypted,
		KMSKeyID:                     fs.KMSKeyID,
		ThroughputMode:               fs.ThroughputMode,
		ProvisionedThroughputInMibps: fs.ProvisionedThroughputInMibps,
		AvailabilityZoneName:         fs.AvailabilityZoneName,
		AvailabilityZoneID:           fs.AvailabilityZoneID,
		Tags:                         mapToTags(fs.Tags),
		FileSystemProtection: &fileSystemProtectionJSON{
			ReplicationOverwriteProtection: fs.Protection.ReplicationOverwriteProtection,
		},
	}
}

func zeroOrEpoch(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}

	return epochSeconds(t)
}

// epochOrNil renders a time as epoch seconds, or nil when zero so the field is
// omitted from the response.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := epochSeconds(t)

	return &secs
}

// --- request shapes ---

type createFileSystemRequest struct {
	CreationToken                string  `json:"CreationToken"`
	PerformanceMode              string  `json:"PerformanceMode"`
	Encrypted                    bool    `json:"Encrypted"`
	KMSKeyID                     string  `json:"KmsKeyId"`
	ThroughputMode               string  `json:"ThroughputMode"`
	ProvisionedThroughputInMibps float64 `json:"ProvisionedThroughputInMibps"`
	AvailabilityZoneName         string  `json:"AvailabilityZoneName"`
	Backup                       bool    `json:"Backup"`
	Tags                         []tag   `json:"Tags"`
}

type updateFileSystemRequest struct {
	ThroughputMode               string  `json:"ThroughputMode"`
	ProvisionedThroughputInMibps float64 `json:"ProvisionedThroughputInMibps"`
}

type putFileSystemPolicyRequest struct {
	Policy                         string `json:"Policy"`
	BypassPolicyLockoutSafetyCheck bool   `json:"BypassPolicyLockoutSafetyCheck"`
}

type tagResourceRequest struct {
	Tags []tag `json:"Tags"`
}

type untagResourceRequest struct {
	TagKeys []string `json:"TagKeys"`
}

// --- response shapes ---

type describeFileSystemsResponse struct {
	FileSystems []fileSystemJSON `json:"FileSystems"`
	Marker      string           `json:"Marker,omitempty"`
	NextMarker  string           `json:"NextMarker,omitempty"`
}

type fileSystemPolicyResponse struct {
	FileSystemID string `json:"FileSystemId"`
	Policy       string `json:"Policy"`
}

type listTagsForResourceResponse struct {
	Tags      []tag  `json:"Tags"`
	NextToken string `json:"NextToken,omitempty"`
}
