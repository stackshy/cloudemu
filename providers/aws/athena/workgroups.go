package athena

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

// maxNameLen is Athena's cap on workgroup and named-query names.
const maxNameLen = 128

// validWorkGroupName reports whether s is a non-empty name within Athena's
// length limit.
func validWorkGroupName(s string) bool {
	return s != "" && len(s) <= maxNameLen
}

// CreateWorkGroup creates a workgroup, materializing the effective engine
// version and the default booleans that real Athena reports back.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateWorkGroup(_ context.Context, wg driver.WorkGroup) error {
	if !validWorkGroupName(wg.Name) {
		return invalidRequest("workgroup name %q is invalid", wg.Name)
	}

	if wg.State == "" {
		wg.State = driver.WorkGroupStateEnabled
	}

	if err := materializeConfig(&wg.Configuration); err != nil {
		return err
	}

	wg.CreationTime = m.now()

	if !m.workGroups.SetIfAbsent(wg.Name, copyWorkGroup(wg)) {
		return invalidRequest("WorkGroup %s already exists", wg.Name)
	}

	if len(wg.Tags) > 0 {
		m.storeTags(m.workGroupARN(wg.Name), wg.Tags)
	}

	return nil
}

// materializeConfig fills the real Athena defaults into a workgroup
// configuration so GetWorkGroup echoes concrete values: enforce=true,
// publish=false, requester=false, and a computed EffectiveEngineVersion. An
// explicitly-set false is preserved (the fields are pointers).
func materializeConfig(cfg *driver.WorkGroupConfiguration) error {
	if cfg.EnforceWorkGroupConfiguration == nil {
		cfg.EnforceWorkGroupConfiguration = boolPtr(true)
	}

	if cfg.PublishCloudWatchMetricsEnabled == nil {
		cfg.PublishCloudWatchMetricsEnabled = boolPtr(false)
	}

	if cfg.RequesterPaysEnabled == nil {
		cfg.RequesterPaysEnabled = boolPtr(false)
	}

	if cfg.BytesScannedCutoffPerQuery != nil && *cfg.BytesScannedCutoffPerQuery < minBytesScannedCutoff {
		return invalidRequest("bytesScannedCutoffPerQuery must be at least %d", minBytesScannedCutoff)
	}

	cfg.EngineVersion = resolveEngineVersion(cfg.EngineVersion)

	return nil
}

// GetWorkGroup returns a deep copy of a workgroup.
func (m *Mock) GetWorkGroup(_ context.Context, name string) (*driver.WorkGroup, error) {
	if name == "" {
		name = driver.DefaultWorkGroup
	}

	wg, ok := m.workGroups.Get(name)
	if !ok {
		return nil, notFoundRequest("WorkGroup %s is not found", name)
	}

	out := copyWorkGroup(wg)

	return &out, nil
}

// UpdateWorkGroup applies the top-level Description/State plus the
// ConfigurationUpdates delta. It never replaces the whole configuration.
func (m *Mock) UpdateWorkGroup(_ context.Context, name string, upd driver.WorkGroupUpdate) error {
	if name == "" {
		name = driver.DefaultWorkGroup
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	wg, ok := m.workGroups.Get(name)
	if !ok {
		return notFoundRequest("WorkGroup %s is not found", name)
	}

	wg = copyWorkGroup(wg)

	if upd.Description != nil {
		wg.Description = *upd.Description
	}

	if upd.State != nil {
		if *upd.State != driver.WorkGroupStateEnabled && *upd.State != driver.WorkGroupStateDisabled {
			return invalidRequest("invalid workgroup state %q", *upd.State)
		}

		wg.State = *upd.State
	}

	if err := applyConfigurationUpdates(&wg.Configuration, upd.ConfigurationUpdates); err != nil {
		return err
	}

	m.workGroups.Set(name, wg)

	return nil
}

// applyConfigurationUpdates applies a configuration delta in place. A nil setter
// with a false Remove flag leaves the attribute unchanged; Remove flags clear a
// value.
func applyConfigurationUpdates(cfg *driver.WorkGroupConfiguration, upd *driver.WorkGroupConfigurationUpdates) error {
	if upd == nil {
		return nil
	}

	if upd.EnforceWorkGroupConfiguration != nil {
		cfg.EnforceWorkGroupConfiguration = copyBoolPtr(upd.EnforceWorkGroupConfiguration)
	}

	if upd.PublishCloudWatchMetricsEnabled != nil {
		cfg.PublishCloudWatchMetricsEnabled = copyBoolPtr(upd.PublishCloudWatchMetricsEnabled)
	}

	if upd.RequesterPaysEnabled != nil {
		cfg.RequesterPaysEnabled = copyBoolPtr(upd.RequesterPaysEnabled)
	}

	if err := applyBytesScannedUpdate(cfg, upd); err != nil {
		return err
	}

	if upd.EngineVersion != nil {
		cfg.EngineVersion = resolveEngineVersion(*upd.EngineVersion)
	}

	applyResultConfigurationUpdates(cfg, upd.ResultConfigurationUpdates)

	return nil
}

// applyBytesScannedUpdate handles the cutoff setter and its Remove flag.
func applyBytesScannedUpdate(cfg *driver.WorkGroupConfiguration, upd *driver.WorkGroupConfigurationUpdates) error {
	if upd.RemoveBytesScannedCutoffPerQuery {
		cfg.BytesScannedCutoffPerQuery = nil

		return nil
	}

	if upd.BytesScannedCutoffPerQuery != nil {
		if *upd.BytesScannedCutoffPerQuery < minBytesScannedCutoff {
			return invalidRequest("bytesScannedCutoffPerQuery must be at least %d", minBytesScannedCutoff)
		}

		cfg.BytesScannedCutoffPerQuery = copyInt64Ptr(upd.BytesScannedCutoffPerQuery)
	}

	return nil
}

// applyResultConfigurationUpdates applies the ResultConfiguration delta,
// allocating the nested struct on first use.
func applyResultConfigurationUpdates(cfg *driver.WorkGroupConfiguration, upd *driver.ResultConfigurationUpdates) {
	if upd == nil {
		return
	}

	if cfg.ResultConfiguration == nil {
		cfg.ResultConfiguration = &driver.ResultConfiguration{}
	}

	rc := cfg.ResultConfiguration

	applyRCLocationAndEncryption(rc, upd)
	applyRCOwnerAndACL(rc, upd)
}

// applyRCLocationAndEncryption applies the output-location and encryption deltas.
func applyRCLocationAndEncryption(rc *driver.ResultConfiguration, upd *driver.ResultConfigurationUpdates) {
	switch {
	case upd.RemoveOutputLocation:
		rc.OutputLocation = ""
	case upd.OutputLocation != "":
		rc.OutputLocation = upd.OutputLocation
	}

	switch {
	case upd.RemoveEncryptionConfiguration:
		rc.EncryptionConfiguration = nil
	case upd.EncryptionConfiguration != nil:
		rc.EncryptionConfiguration = copyEncryptionConfiguration(upd.EncryptionConfiguration)
	}
}

// applyRCOwnerAndACL applies the expected-bucket-owner and ACL deltas.
func applyRCOwnerAndACL(rc *driver.ResultConfiguration, upd *driver.ResultConfigurationUpdates) {
	switch {
	case upd.RemoveExpectedBucketOwner:
		rc.ExpectedBucketOwner = ""
	case upd.ExpectedBucketOwner != "":
		rc.ExpectedBucketOwner = upd.ExpectedBucketOwner
	}

	switch {
	case upd.RemoveACLConfiguration:
		rc.ACLConfiguration = nil
	case upd.ACLConfiguration != nil:
		rc.ACLConfiguration = copyACLConfiguration(upd.ACLConfiguration)
	}
}

// DeleteWorkGroup removes a workgroup. The primary workgroup cannot be deleted.
// Without recursive, a workgroup that still owns named queries or query
// executions is rejected.
func (m *Mock) DeleteWorkGroup(_ context.Context, name string, recursive bool) error {
	if name == "" {
		name = driver.DefaultWorkGroup
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.workGroups.Get(name); !ok {
		return notFoundRequest("WorkGroup %s is not found", name)
	}

	if name == driver.DefaultWorkGroup {
		return invalidRequest("the primary workgroup cannot be deleted")
	}

	nqKeys := m.namedQueryKeysForWorkGroup(name)
	qeKeys := m.queryExecutionKeysForWorkGroup(name)

	if !recursive && (len(nqKeys) > 0 || len(qeKeys) > 0) {
		return invalidRequest("WorkGroup %s is not empty", name)
	}

	for _, id := range nqKeys {
		m.namedQueries.Delete(id)
	}

	for _, id := range qeKeys {
		m.queryExecutions.Delete(id)
	}

	m.workGroups.Delete(name)
	m.deleteTags(m.workGroupARN(name))

	return nil
}

func (m *Mock) namedQueryKeysForWorkGroup(workGroup string) []string {
	var out []string

	for id, nq := range m.namedQueries.All() {
		if nq.WorkGroup == workGroup {
			out = append(out, id)
		}
	}

	return out
}

func (m *Mock) queryExecutionKeysForWorkGroup(workGroup string) []string {
	var out []string

	for _, id := range m.queryExecutions.Keys() {
		qe, ok := m.queryExecutions.Get(id)
		if ok && qe.WorkGroup == workGroup {
			out = append(out, id)
		}
	}

	return out
}

// ListWorkGroups returns workgroup summaries sorted by name.
func (m *Mock) ListWorkGroups(_ context.Context, page driver.Pagination) ([]driver.WorkGroupSummary, string, error) {
	keys := sortedKeys(m.workGroups.Keys())
	all := make([]driver.WorkGroupSummary, 0, len(keys))

	for _, name := range keys {
		wg, ok := m.workGroups.Get(name)
		if !ok {
			continue
		}

		all = append(all, driver.WorkGroupSummary{
			Name:          wg.Name,
			State:         wg.State,
			Description:   wg.Description,
			CreationTime:  wg.CreationTime,
			EngineVersion: wg.Configuration.EngineVersion,
		})
	}

	return paginate(all, page)
}
