package configservice

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// PutConfigurationRecorder creates or updates the account's configuration
// recorder. Config allows exactly one customer-managed recorder per account, so
// a Put naming a different recorder while one already exists is a
// MaxNumberOfConfigurationRecordersExceededException. A Put naming the existing
// recorder is an idempotent upsert (real Config's semantics).
//
//nolint:gocritic // rec is the driver ConfigurationRecorder input, taken by value to match the driver API.
func (m *Mock) PutConfigurationRecorder(_ context.Context, rec driver.ConfigurationRecorder) error {
	if rec.Name == "" {
		rec.Name = defaultName
	}

	if rec.RoleARN == "" {
		return invalidParameter("RoleARN is required")
	}

	now := m.now()

	// Guard the single-recorder invariant: if a different recorder exists, reject.
	for _, k := range m.recorders.Keys() {
		if k != rec.Name {
			return maxRecordersExceeded()
		}
	}

	if existing, ok := m.recorders.Get(rec.Name); ok {
		existing.mu.Lock()
		existing.rec.RoleARN = rec.RoleARN
		existing.rec.RecordingGroup = rec.RecordingGroup
		existing.rec.Tags = copyTags(rec.Tags)
		existing.mu.Unlock()

		return nil
	}

	rec.Arn = m.arn("config-recorder/" + rec.Name)
	rec.Tags = copyTags(rec.Tags)
	rec.LastStatus = driver.RecorderStatusPending
	rec.LastStatusChangeTime = now
	rec.Recording = false

	// SetIfAbsent guards against a concurrent create racing the same name.
	if !m.recorders.SetIfAbsent(rec.Name, &recorderData{rec: rec}) {
		return maxRecordersExceeded()
	}

	return nil
}

func copyRecorder(r *driver.ConfigurationRecorder) driver.ConfigurationRecorder {
	out := *r
	out.Tags = copyTags(r.Tags)

	if r.RecordingGroup != nil {
		rg := *r.RecordingGroup
		rg.ResourceTypes = copyStrings(r.RecordingGroup.ResourceTypes)
		rg.ExclusionByResources = copyStrings(r.RecordingGroup.ExclusionByResources)
		out.RecordingGroup = &rg
	}

	return out
}

func (m *Mock) allRecorders() []driver.ConfigurationRecorder {
	keys := sortedKeys(m.recorders.Keys())
	out := make([]driver.ConfigurationRecorder, 0, len(keys))

	for _, k := range keys {
		rd, ok := m.recorders.Get(k)
		if !ok {
			continue
		}

		rd.mu.RLock()
		out = append(out, copyRecorder(&rd.rec))
		rd.mu.RUnlock()
	}

	return out
}

// DescribeConfigurationRecorders returns the named recorders (all if names is
// empty). A named-but-absent recorder is a NoSuchConfigurationRecorderException.
func (m *Mock) DescribeConfigurationRecorders(
	_ context.Context, names []string,
) ([]driver.ConfigurationRecorder, error) {
	for _, n := range names {
		if !m.recorders.Has(n) {
			return nil, noSuchRecorder(n)
		}
	}

	all := m.allRecorders()

	return filterByNames(all, func(r driver.ConfigurationRecorder) string { return r.Name }, names), nil
}

// DescribeConfigurationRecorderStatus returns the runtime status of the named
// recorders (all if empty).
func (m *Mock) DescribeConfigurationRecorderStatus(
	_ context.Context, names []string,
) ([]driver.ConfigurationRecorder, error) {
	for _, n := range names {
		if !m.recorders.Has(n) {
			return nil, noSuchRecorder(n)
		}
	}

	all := m.allRecorders()

	return filterByNames(all, func(r driver.ConfigurationRecorder) string { return r.Name }, names), nil
}

// ListConfigurationRecorders paginates recorders.
func (m *Mock) ListConfigurationRecorders(
	_ context.Context, page driver.Page,
) ([]driver.ConfigurationRecorder, string, error) {
	return paginate(m.allRecorders(), page)
}

// DeleteConfigurationRecorder removes a recorder, holding the write lock across
// the existence check and delete.
func (m *Mock) DeleteConfigurationRecorder(_ context.Context, name string) error {
	if !m.recorders.Delete(name) {
		return noSuchRecorder(name)
	}

	return nil
}

// StartConfigurationRecorder starts recording. Real Config requires a delivery
// channel before recording can start; without one it is a
// NoAvailableDeliveryChannelException.
func (m *Mock) StartConfigurationRecorder(_ context.Context, name string) error {
	rd, ok := m.recorders.Get(name)
	if !ok {
		return noSuchRecorder(name)
	}

	if m.channels.Len() == 0 {
		return noAvailableDeliveryChannel()
	}

	now := m.now()

	rd.mu.Lock()
	rd.rec.Recording = true
	rd.rec.LastStatus = driver.RecorderStatusSuccess
	rd.rec.LastStartTime = now
	rd.rec.LastStatusChangeTime = now
	rd.mu.Unlock()

	return nil
}

// StopConfigurationRecorder stops recording.
func (m *Mock) StopConfigurationRecorder(_ context.Context, name string) error {
	rd, ok := m.recorders.Get(name)
	if !ok {
		return noSuchRecorder(name)
	}

	now := m.now()

	rd.mu.Lock()
	rd.rec.Recording = false
	rd.rec.LastStopTime = now
	rd.rec.LastStatusChangeTime = now
	rd.mu.Unlock()

	return nil
}

// recorderByArn resolves a recorder by its ARN for resource-type association.
func (m *Mock) recorderByArn(arn string) (*recorderData, error) {
	for _, k := range m.recorders.Keys() {
		rd, ok := m.recorders.Get(k)
		if !ok {
			continue
		}

		rd.mu.RLock()
		match := rd.rec.Arn == arn
		rd.mu.RUnlock()

		if match {
			return rd, nil
		}
	}

	return nil, validation("no configuration recorder with ARN %q", arn)
}

// AssociateResourceTypes adds resource types to a recorder's recording group,
// switching it to INCLUSION_BY_RESOURCE_TYPES. Validation happens before any
// mutation so a bad ARN never partially updates the group.
//
//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (m *Mock) AssociateResourceTypes(
	_ context.Context, recorderArn string, resourceTypes []string,
) (driver.ConfigurationRecorder, error) {
	if len(resourceTypes) == 0 {
		return driver.ConfigurationRecorder{}, invalidParameter("ResourceTypes must not be empty")
	}

	rd, err := m.recorderByArn(recorderArn)
	if err != nil {
		return driver.ConfigurationRecorder{}, err
	}

	rd.mu.Lock()
	defer rd.mu.Unlock()

	rd.rec.RecordingGroup = mergeResourceTypes(rd.rec.RecordingGroup, resourceTypes, true)

	return copyRecorder(&rd.rec), nil
}

// DisassociateResourceTypes removes resource types from a recorder's group.
//
//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (m *Mock) DisassociateResourceTypes(
	_ context.Context, recorderArn string, resourceTypes []string,
) (driver.ConfigurationRecorder, error) {
	if len(resourceTypes) == 0 {
		return driver.ConfigurationRecorder{}, invalidParameter("ResourceTypes must not be empty")
	}

	rd, err := m.recorderByArn(recorderArn)
	if err != nil {
		return driver.ConfigurationRecorder{}, err
	}

	rd.mu.Lock()
	defer rd.mu.Unlock()

	rd.rec.RecordingGroup = mergeResourceTypes(rd.rec.RecordingGroup, resourceTypes, false)

	return copyRecorder(&rd.rec), nil
}

// mergeResourceTypes adds or removes types from a recording group, returning a
// new group set to INCLUSION_BY_RESOURCE_TYPES.
func mergeResourceTypes(rg *driver.RecordingGroup, types []string, add bool) *driver.RecordingGroup {
	set := map[string]bool{}

	if rg != nil {
		for _, t := range rg.ResourceTypes {
			set[t] = true
		}
	}

	for _, t := range types {
		if add {
			set[t] = true
		} else {
			delete(set, t)
		}
	}

	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}

	sortedKeys(out)

	return &driver.RecordingGroup{
		AllSupported:      false,
		ResourceTypes:     out,
		RecordingStrategy: "INCLUSION_BY_RESOURCE_TYPES",
	}
}

// PutServiceLinkedConfigurationRecorder creates a service-linked recorder,
// subject to the same single-recorder invariant.
func (m *Mock) PutServiceLinkedConfigurationRecorder(
	_ context.Context, principal string, tags map[string]string,
) (arn, name string, err error) {
	if principal == "" {
		return "", "", invalidParameter("ServicePrincipal is required")
	}

	name = "AWSConfigurationRecorderFor" + serviceShortName(principal)

	if m.recorders.Len() > 0 && !m.recorders.Has(name) {
		return "", "", maxRecordersExceeded()
	}

	now := m.now()
	rec := driver.ConfigurationRecorder{
		Arn:                  m.arn("config-recorder/" + name),
		Name:                 name,
		Tags:                 copyTags(tags),
		LastStatus:           driver.RecorderStatusPending,
		LastStatusChangeTime: now,
	}

	if !m.recorders.SetIfAbsent(name, &recorderData{rec: rec}) {
		return rec.Arn, name, nil
	}

	return rec.Arn, name, nil
}

// PutThirdPartyServiceLinkedConfigurationRecorder is the third-party variant of
// the service-linked recorder create.
func (m *Mock) PutThirdPartyServiceLinkedConfigurationRecorder(
	ctx context.Context, principal string, tags map[string]string,
) (arn, name string, err error) {
	return m.PutServiceLinkedConfigurationRecorder(ctx, principal, tags)
}

// DeleteServiceLinkedConfigurationRecorder deletes a service-linked recorder,
// returning its ARN and name.
func (m *Mock) DeleteServiceLinkedConfigurationRecorder(
	_ context.Context, name string,
) (arn, delName string, err error) {
	rd, ok := m.recorders.Get(name)
	if !ok {
		return "", "", noSuchRecorder(name)
	}

	rd.mu.RLock()
	arn = rd.rec.Arn
	rd.mu.RUnlock()

	m.recorders.Delete(name)

	return arn, name, nil
}

func serviceShortName(principal string) string {
	// e.g. "securityhub.amazonaws.com" -> "SecurityHub".
	base := principal
	if i := strings.IndexByte(principal, '.'); i > 0 {
		base = principal[:i]
	}

	return strings.ToUpper(base[:1]) + base[1:]
}
