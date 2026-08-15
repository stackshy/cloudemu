package guardduty

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// isoFormat is the timestamp layout GuardDuty uses for the detector's string
// createdAt/updatedAt fields.
const isoFormat = "2006-01-02T15:04:05.000Z"

// withNext adds a nextToken field to a response map when next is non-empty.
func withNext(m map[string]any, next string) map[string]any {
	if next != "" {
		m["nextToken"] = next
	}

	return m
}

// --- Detector wire shapes ---

// createDetectorRequest is the CreateDetector request body. Features and
// DataSources are carried verbatim as raw JSON so a round-tripped detector
// reflects everything the caller sent.
type createDetectorRequest struct {
	Enable                     *bool             `json:"enable"`
	ClientToken                string            `json:"clientToken"`
	FindingPublishingFrequency string            `json:"findingPublishingFrequency"`
	Features                   []json.RawMessage `json:"features"`
	DataSources                json.RawMessage   `json:"dataSources"`
	Tags                       map[string]string `json:"tags"`
}

// updateDetectorRequest is the UpdateDetector request body.
type updateDetectorRequest struct {
	Enable                     *bool             `json:"enable"`
	FindingPublishingFrequency *string           `json:"findingPublishingFrequency"`
	Features                   []json.RawMessage `json:"features"`
	DataSources                json.RawMessage   `json:"dataSources"`
}

// detectorToWire renders a driver detector as its GetDetector wire shape.
func detectorToWire(d *driver.Detector) map[string]any {
	out := map[string]any{
		"serviceRole":                d.ServiceRole,
		"status":                     d.Status,
		"findingPublishingFrequency": d.FindingPublishingFrequency,
		"createdAt":                  d.CreatedAt.Format(isoFormat),
		"updatedAt":                  d.UpdatedAt.Format(isoFormat),
	}

	if len(d.Tags) > 0 {
		out["tags"] = d.Tags
	}

	if len(d.Features) > 0 {
		out["features"] = d.Features
	}

	if d.DataSources != nil {
		out["dataSources"] = d.DataSources
	}

	return out
}

// --- Set (IPSet / ThreatIntelSet / ThreatEntitySet / TrustedEntitySet) wire shapes ---

// createSetRequest is the shared request body for the four set-create ops. Its
// fields are identical across IPSet, ThreatIntelSet, ThreatEntitySet, and
// TrustedEntitySet.
type createSetRequest struct {
	Name                string            `json:"name"`
	Format              string            `json:"format"`
	Location            string            `json:"location"`
	Activate            bool              `json:"activate"`
	ClientToken         string            `json:"clientToken"`
	ExpectedBucketOwner string            `json:"expectedBucketOwner"`
	Tags                map[string]string `json:"tags"`
}

// updateSetRequest is the shared request body for the four set-update ops.
type updateSetRequest struct {
	Name                *string `json:"name"`
	Location            *string `json:"location"`
	Activate            *bool   `json:"activate"`
	ExpectedBucketOwner *string `json:"expectedBucketOwner"`
}

// setToWire renders the common fields shared by every set's Get response.
func setToWire(name, format, location, status, bucketOwner string, tags map[string]string) map[string]any {
	out := map[string]any{
		"name":     name,
		"format":   format,
		"location": location,
		"status":   status,
	}

	if bucketOwner != "" {
		out["expectedBucketOwner"] = bucketOwner
	}

	if len(tags) > 0 {
		out["tags"] = tags
	}

	return out
}

// entitySetTimestamps adds the createdAt/updatedAt/errorDetails fields the
// newer entity sets carry as epoch-seconds on the wire.
func entitySetTimestamps(m map[string]any, created, updated time.Time, errorDetails string) map[string]any {
	m["createdAt"] = created.Unix()
	m["updatedAt"] = updated.Unix()

	if errorDetails != "" {
		m["errorDetails"] = errorDetails
	}

	return m
}

// --- Filter wire shapes ---

// createFilterRequest is the CreateFilter request body.
type createFilterRequest struct {
	Name            string            `json:"name"`
	Action          string            `json:"action"`
	Description     string            `json:"description"`
	Rank            int32             `json:"rank"`
	FindingCriteria json.RawMessage   `json:"findingCriteria"`
	ClientToken     string            `json:"clientToken"`
	Tags            map[string]string `json:"tags"`
}

// updateFilterRequest is the UpdateFilter request body.
type updateFilterRequest struct {
	Action          *string         `json:"action"`
	Description     *string         `json:"description"`
	Rank            *int32          `json:"rank"`
	FindingCriteria json.RawMessage `json:"findingCriteria"`
}

// filterToWire renders a driver filter as its GetFilter wire shape.
func filterToWire(f *driver.Filter) map[string]any {
	out := map[string]any{
		"name":   f.Name,
		"action": f.Action,
		"rank":   f.Rank,
	}

	if f.Description != "" {
		out["description"] = f.Description
	}

	if f.FindingCriteria != nil {
		out["findingCriteria"] = f.FindingCriteria
	}

	if len(f.Tags) > 0 {
		out["tags"] = f.Tags
	}

	return out
}
