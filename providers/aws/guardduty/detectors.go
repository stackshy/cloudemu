package guardduty

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// defaultDetectorFeatures returns the feature-configuration results a freshly
// created detector reports when the caller doesn't specify features, matching
// real GuardDuty's GetDetector which always lists its enabled features.
func defaultDetectorFeatures() []json.RawMessage {
	names := syntheticFeatures()
	out := make([]json.RawMessage, 0, len(names))

	for _, name := range names {
		b, _ := json.Marshal(map[string]any{
			"name":   name,
			"status": "ENABLED",
		})
		out = append(out, b)
	}

	return out
}

// defaultDetectorDataSources returns the deprecated DataSourceConfigurationsResult
// block a detector reports when the caller doesn't specify data sources.
func defaultDetectorDataSources() json.RawMessage {
	enabled := map[string]any{"status": "ENABLED"}
	b, _ := json.Marshal(map[string]any{
		"cloudTrail": enabled,
		"dnsLogs":    enabled,
		"flowLogs":   enabled,
		"s3Logs":     enabled,
		"kubernetes": map[string]any{"auditLogs": enabled},
		"malwareProtection": map[string]any{
			"scanEc2InstanceWithFindings": map[string]any{"ebsVolumes": enabled},
		},
	})

	return b
}

// copyDetector returns a deep copy of a stored detector so a reader cannot alias
// the Tags map, the Features slice, or the DataSources raw block.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot a copy of stored state.
func copyDetector(d driver.Detector) driver.Detector {
	out := d
	out.Tags = copyTags(d.Tags)
	out.Features = copyRawSlice(d.Features)
	out.DataSources = copyRaw(d.DataSources)

	return out
}

// validFindingFrequency reports whether f is one of GuardDuty's modeled
// FindingPublishingFrequency enum values.
func validFindingFrequency(f string) bool {
	switch f {
	case driver.FindingFrequencyFifteenMinutes, driver.FindingFrequencyOneHour,
		driver.FindingFrequencySixHours:
		return true
	default:
		return false
	}
}

// CreateDetector provisions the account's single detector, immediately ENABLED
// (or DISABLED when Enable is false). A second create is rejected because
// GuardDuty allows only one detector per account per region.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) CreateDetector(_ context.Context, in driver.CreateDetectorInput) (*driver.Detector, error) {
	now := m.now()

	status := driver.DetectorStatusEnabled
	if !in.Enable {
		status = driver.DetectorStatusDisabled
	}

	freq := in.FindingPublishingFrequency
	if freq == "" {
		freq = driver.FindingFrequencySixHours
	}

	if !validFindingFrequency(freq) {
		return nil, badRequest("findingPublishingFrequency %q is invalid", freq)
	}

	// One detector per account per region: serialize the count check with the
	// insert so two concurrent creates can't both pass the cap.
	m.createMu.Lock()
	defer m.createMu.Unlock()

	if m.detectors.Len() > 0 {
		return nil, badRequest("a detector already exists for the current account")
	}

	features := copyRawSlice(in.Features)
	if len(features) == 0 {
		features = defaultDetectorFeatures()
	}

	dataSources := copyRaw(in.DataSources)
	if dataSources == nil {
		dataSources = defaultDetectorDataSources()
	}

	det := driver.Detector{
		ID:                         m.newDetectorID(),
		ServiceRole:                m.serviceRoleARN(),
		Status:                     status,
		FindingPublishingFrequency: freq,
		Features:                   features,
		DataSources:                dataSources,
		Tags:                       copyTags(in.Tags),
		CreatedAt:                  now,
		UpdatedAt:                  now,
	}

	dd := &detectorData{
		detector:     det,
		ipSets:       map[string]driver.IPSet{},
		threatIS:     map[string]driver.ThreatIntelSet{},
		threatES:     map[string]driver.ThreatEntitySet{},
		trustES:      map[string]driver.TrustedEntitySet{},
		filters:      map[string]driver.Filter{},
		members:      map[string]memberData{},
		invites:      map[string]invitationData{},
		publishDests: map[string]destData{},
		findings:     map[string]findingData{},
	}

	// Detector IDs are server-minted and unique, so SetIfAbsent never loses here;
	// it is used for consistency with the atomic-create convention.
	if !m.detectors.SetIfAbsent(det.ID, dd) {
		return nil, badRequest("detector %s already exists", det.ID)
	}

	out := copyDetector(det)

	return &out, nil
}

// GetDetector returns a deep copy of the stored detector.
func (m *Mock) GetDetector(_ context.Context, detectorID string) (*driver.Detector, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	out := copyDetector(dd.detector)

	return &out, nil
}

// UpdateDetector patches the modeled detector fields. Only non-nil fields are
// applied so a caller can patch a single attribute.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) UpdateDetector(_ context.Context, in driver.UpdateDetectorInput) error {
	if in.FindingPublishingFrequency != nil && !validFindingFrequency(*in.FindingPublishingFrequency) {
		return badRequest("findingPublishingFrequency %q is invalid", *in.FindingPublishingFrequency)
	}

	dd, err := m.getDetector(in.DetectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	if in.Enable != nil {
		if *in.Enable {
			dd.detector.Status = driver.DetectorStatusEnabled
		} else {
			dd.detector.Status = driver.DetectorStatusDisabled
		}
	}

	if in.FindingPublishingFrequency != nil {
		dd.detector.FindingPublishingFrequency = *in.FindingPublishingFrequency
	}

	if in.Features != nil {
		dd.detector.Features = copyRawSlice(in.Features)
	}

	if in.DataSources != nil {
		dd.detector.DataSources = copyRaw(in.DataSources)
	}

	dd.detector.UpdatedAt = m.now()

	return nil
}

// DeleteDetector removes a detector and every child resource it owns. The
// detector's own lock is held across the check so a concurrent child create
// cannot orphan a resource under a detector being deleted; the child maps are
// dropped with the detector, so no orphan can survive.
func (m *Mock) DeleteDetector(_ context.Context, detectorID string) error {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	// Drop all child resources under the lock, then remove the detector itself.
	// Because the maps live on detectorData and are cleared before the store
	// delete, a racing child-create either ran before (its entry is cleared) or
	// after (its getDetector fails), never leaving an orphan.
	dd.ipSets = map[string]driver.IPSet{}
	dd.threatIS = map[string]driver.ThreatIntelSet{}
	dd.threatES = map[string]driver.ThreatEntitySet{}
	dd.trustES = map[string]driver.TrustedEntitySet{}
	dd.filters = map[string]driver.Filter{}
	dd.members = map[string]memberData{}
	dd.invites = map[string]invitationData{}
	dd.publishDests = map[string]destData{}
	dd.findings = map[string]findingData{}
	dd.mu.Unlock()

	m.detectors.Delete(detectorID)

	return nil
}

// ListDetectors lists all detector IDs, sorted for deterministic output.
func (m *Mock) ListDetectors(_ context.Context, page driver.Page) (ids []string, next string, err error) {
	all := m.detectors.Keys()
	sort.Strings(all)

	return paginateIDs(all, page)
}
