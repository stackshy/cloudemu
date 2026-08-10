package opensearch

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// copyClusterConfig returns a value copy of a ClusterConfig (all scalar fields,
// no reference members) so callers cannot alias stored state.
func copyClusterConfig(c driver.ClusterConfig) driver.ClusterConfig {
	return c
}

// snapshotStatus returns a deep copy of a stored DomainStatus so a reader
// cannot alias the Tags/AdvancedOptions/RawOptions maps under the lock.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot a copy of stored state.
func snapshotStatus(s driver.DomainStatus) driver.DomainStatus {
	out := s
	out.ClusterConfig = copyClusterConfig(s.ClusterConfig)
	out.AdvancedOptions = copyTags(s.AdvancedOptions)
	out.Endpoints = copyTags(s.Endpoints)
	out.RawOptions = copyRaw(s.RawOptions)

	return out
}

// snapshotConfig returns a deep copy of a stored DomainConfig.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot a copy of stored state.
func snapshotConfig(c driver.DomainConfig) driver.DomainConfig {
	out := c
	out.ClusterConfig = copyClusterConfig(c.ClusterConfig)
	out.AdvancedOptions = copyTags(c.AdvancedOptions)
	out.RawOptions = copyRaw(c.RawOptions)

	return out
}

// CreateDomain provisions a domain that is immediately Active with an endpoint.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.OpenSearch interface (by-value input).
func (m *Mock) CreateDomain(_ context.Context, in driver.CreateDomainInput) (*driver.DomainStatus, error) {
	if err := validateDomainName(in.DomainName); err != nil {
		return nil, err
	}

	if len(in.Tags) > maxTags {
		return nil, limitExceeded("A domain may have at most %d tags", maxTags)
	}

	engine := in.EngineVersion
	if engine == "" {
		engine = defaultEngine
	}

	now := m.now()
	cfg := copyClusterConfig(in.ClusterConfig)

	status := driver.DomainStatus{
		DomainID:               m.opts.AccountID + "/" + in.DomainName,
		DomainName:             in.DomainName,
		ARN:                    m.domainARN(in.DomainName),
		Created:                true,
		Deleted:                false,
		Processing:             false,
		EngineVersion:          engine,
		Endpoint:               m.endpointFor(in.DomainName),
		DomainProcessingStatus: driver.ProcessingActive,
		ClusterConfig:          cfg,
		AccessPolicies:         in.AccessPolicies,
		AdvancedOptions:        copyTags(in.AdvancedOptions),
		IPAddressType:          in.IPAddressType,
		EngineMode:             in.EngineMode,
		RawOptions:             copyRaw(in.RawOptions),
		CreatedAt:              now,
	}

	dd := &domainData{
		status: status,
		config: driver.DomainConfig{
			EngineVersion:   engine,
			ClusterConfig:   cfg,
			AccessPolicies:  in.AccessPolicies,
			AdvancedOptions: copyTags(in.AdvancedOptions),
			IPAddressType:   in.IPAddressType,
			RawOptions:      copyRaw(in.RawOptions),
			UpdatedAt:       now,
		},
		tags:     copyTags(in.Tags),
		dataSrcs: map[string]driver.DataSource{},
	}

	// Claim the name atomically; a losing racer sees the duplicate exception.
	if !m.domains.SetIfAbsent(in.DomainName, dd) {
		return nil, alreadyExists("Domain already exists: %s", in.DomainName)
	}

	out := snapshotStatus(status)

	return &out, nil
}

// DescribeDomain returns a deep copy of the stored domain status.
func (m *Mock) DescribeDomain(_ context.Context, name string) (*driver.DomainStatus, error) {
	dd, err := m.getDomain(name)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	out := snapshotStatus(dd.status)

	return &out, nil
}

// DescribeDomains returns deep copies of the named domains, skipping any that
// do not exist (matching real DescribeDomains, which returns only found ones).
func (m *Mock) DescribeDomains(_ context.Context, names []string) ([]driver.DomainStatus, error) {
	out := make([]driver.DomainStatus, 0, len(names))

	for _, name := range names {
		dd, ok := m.domains.Get(name)
		if !ok {
			continue
		}

		dd.mu.RLock()
		out = append(out, snapshotStatus(dd.status))
		dd.mu.RUnlock()
	}

	return out, nil
}

// DescribeDomainConfig returns a deep copy of the stored domain config.
func (m *Mock) DescribeDomainConfig(_ context.Context, name string) (*driver.DomainConfig, error) {
	dd, err := m.getDomain(name)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	out := snapshotConfig(dd.config)

	return &out, nil
}

// UpdateDomainConfig patches the modeled config fields and reflects them in the
// domain status. A dry run validates without persisting. The returned bool
// reports whether the change was persisted (false for dry runs).
func (m *Mock) UpdateDomainConfig(_ context.Context, in driver.UpdateDomainConfigInput) (*driver.DomainConfig, bool, error) {
	dd, err := m.getDomain(in.DomainName)
	if err != nil {
		return nil, false, err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	next := snapshotConfig(dd.config)
	applyConfigPatch(&next, in)
	next.UpdatedAt = m.now()

	if in.DryRun {
		return &next, false, nil
	}

	dd.config = snapshotConfig(next)
	reflectConfigOnStatus(&dd.status, &dd.config)

	out := snapshotConfig(dd.config)

	return &out, true, nil
}

// applyConfigPatch applies only the non-nil fields of an update to cfg.
func applyConfigPatch(cfg *driver.DomainConfig, in driver.UpdateDomainConfigInput) {
	if in.ClusterConfig != nil {
		cfg.ClusterConfig = copyClusterConfig(*in.ClusterConfig)
	}

	if in.AccessPolicies != nil {
		cfg.AccessPolicies = *in.AccessPolicies
	}

	if in.IPAddressType != nil {
		cfg.IPAddressType = *in.IPAddressType
	}

	if in.AdvancedOptions != nil {
		cfg.AdvancedOptions = copyTags(in.AdvancedOptions)
	}

	for k, v := range in.RawOptions {
		if cfg.RawOptions == nil {
			cfg.RawOptions = map[string]json.RawMessage{}
		}

		cfg.RawOptions[k] = append(json.RawMessage(nil), v...)
	}
}

// reflectConfigOnStatus copies the modeled config fields onto the domain status.
func reflectConfigOnStatus(s *driver.DomainStatus, cfg *driver.DomainConfig) {
	s.ClusterConfig = copyClusterConfig(cfg.ClusterConfig)
	s.AccessPolicies = cfg.AccessPolicies
	s.IPAddressType = cfg.IPAddressType
	s.AdvancedOptions = copyTags(cfg.AdvancedOptions)
	s.RawOptions = copyRaw(cfg.RawOptions)
}

// DeleteDomain removes a domain under the write lock and returns its final
// (Deleted) status. Package associations referencing it are released.
func (m *Mock) DeleteDomain(_ context.Context, name string) (*driver.DomainStatus, error) {
	dd, err := m.getDomain(name)
	if err != nil {
		return nil, err
	}

	dd.mu.Lock()
	dd.status.Deleted = true
	dd.status.Processing = true
	dd.status.DomainProcessingStatus = driver.ProcessingDeleting
	final := snapshotStatus(dd.status)
	dd.mu.Unlock()

	m.domains.Delete(name)
	m.releaseDomainAssociations(name)
	m.cascadeDeleteDomainRefs(name)

	return &final, nil
}

// releaseDomainAssociations drops every package association for a deleted domain.
func (m *Mock) releaseDomainAssociations(domainName string) {
	for _, key := range m.pkgAssoc.Keys() {
		if a, ok := m.pkgAssoc.Get(key); ok && a.DomainName == domainName {
			m.pkgAssoc.Delete(key)
		}
	}
}

// cascadeDeleteDomainRefs removes the resources orphaned by a deleted domain so
// they stop listing: its VPC endpoints (by domain ARN) and any cross-cluster
// inbound/outbound connections in which the domain is either the local or the
// remote endpoint (a connection references a domain on both sides).
func (m *Mock) cascadeDeleteDomainRefs(domainName string) {
	arn := m.domainARN(domainName)

	for _, id := range m.vpcEnds.Keys() {
		if ep, ok := m.vpcEnds.Get(id); ok && ep.DomainARN == arn {
			m.vpcEnds.Delete(id)
		}
	}

	for _, id := range m.outbound.Keys() {
		if c, ok := m.outbound.Get(id); ok && referencesDomain(c.LocalDomainName, c.RemoteDomainName, domainName) {
			m.outbound.Delete(id)
		}
	}

	for _, id := range m.inbound.Keys() {
		if c, ok := m.inbound.Get(id); ok && referencesDomain(c.LocalDomainName, c.RemoteDomainName, domainName) {
			m.inbound.Delete(id)
		}
	}
}

// referencesDomain reports whether a connection's local or remote endpoint is
// the named domain.
func referencesDomain(local, remote, domainName string) bool {
	return local == domainName || remote == domainName
}

// ListDomainNames lists all domain names, optionally filtered by engine type
// (OpenSearch vs Elasticsearch), sorted for deterministic output.
func (m *Mock) ListDomainNames(_ context.Context, engineType string) ([]driver.DomainInfo, error) {
	names := m.domains.Keys()
	sort.Strings(names)

	out := make([]driver.DomainInfo, 0, len(names))

	for _, name := range names {
		dd, ok := m.domains.Get(name)
		if !ok {
			continue
		}

		dd.mu.RLock()
		et := engineTypeOf(dd.status.EngineVersion)
		dd.mu.RUnlock()

		if engineType != "" && engineType != et {
			continue
		}

		out = append(out, driver.DomainInfo{DomainName: name, EngineType: et})
	}

	return out, nil
}

// Engine type names.
const (
	engineElasticsearch = "Elasticsearch"
	engineOpenSearch    = "OpenSearch"
)

// engineTypeOf derives the engine type from a version string.
func engineTypeOf(engineVersion string) string {
	if strings.HasPrefix(engineVersion, engineElasticsearch) {
		return engineElasticsearch
	}

	return engineOpenSearch
}

// DescribeDomainChangeProgress returns a synthesized completed change record.
func (m *Mock) DescribeDomainChangeProgress(_ context.Context, name, changeID string) (map[string]json.RawMessage, error) {
	if _, err := m.getDomain(name); err != nil {
		return nil, err
	}

	id := changeID
	if id == "" {
		id = idgen.GenerateID("change-")
	}

	return map[string]json.RawMessage{
		"ChangeId":             rawString(id),
		"StartTime":            rawFloat(float64(m.now().Unix())),
		"Status":               rawString("COMPLETED"),
		"TotalNumberOfStages":  rawInt(1),
		"ChangeProgressStages": json.RawMessage("[]"),
		"CompletedProperties":  json.RawMessage("[]"),
		"PendingProperties":    json.RawMessage("[]"),
	}, nil
}

// DescribeDomainHealth returns a synthesized healthy status for the domain.
func (m *Mock) DescribeDomainHealth(_ context.Context, name string) (map[string]json.RawMessage, error) {
	if _, err := m.getDomain(name); err != nil {
		return nil, err
	}

	return map[string]json.RawMessage{
		"DomainState":                  rawString("Active"),
		"ClusterHealth":                rawString("Green"),
		"DataNodeCount":                rawString("1"),
		"DedicatedMaster":              json.RawMessage("false"),
		"MasterEligibleNodeCount":      rawString("0"),
		"WarmNodeCount":                rawString("0"),
		"MasterNode":                   rawString("Available"),
		"AvailabilityZoneCount":        rawString("1"),
		"ActiveAvailabilityZoneCount":  rawString("1"),
		"StandByAvailabilityZoneCount": rawString("0"),
		"TotalShards":                  rawString("0"),
		"TotalUnAssignedShards":        rawString("0"),
	}, nil
}

// DescribeDomainNodes returns a synthesized single-node topology.
func (m *Mock) DescribeDomainNodes(_ context.Context, name string) ([]map[string]json.RawMessage, error) {
	dd, err := m.getDomain(name)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	instanceType := dd.status.ClusterConfig.InstanceType
	dd.mu.RUnlock()

	if instanceType == "" {
		instanceType = "t3.small.search"
	}

	return []map[string]json.RawMessage{{
		"NodeId":           rawString(idgen.GenerateID("node-")),
		"NodeType":         rawString("Data"),
		"AvailabilityZone": rawString(m.opts.Region + "a"),
		"InstanceType":     rawString(instanceType),
		"NodeStatus":       rawString("Active"),
		"StorageType":      rawString("EBS"),
	}}, nil
}

// DescribeDomainAutoTunes returns an empty synthesized auto-tune list.
func (m *Mock) DescribeDomainAutoTunes(
	_ context.Context, name string, _ driver.Page,
) (autoTunes []map[string]json.RawMessage, next string, err error) {
	if _, err := m.getDomain(name); err != nil {
		return nil, "", err
	}

	return []map[string]json.RawMessage{}, "", nil
}

// DescribeDryRunProgress returns a synthesized succeeded dry-run.
func (m *Mock) DescribeDryRunProgress(_ context.Context, name string) (map[string]json.RawMessage, error) {
	if _, err := m.getDomain(name); err != nil {
		return nil, err
	}

	ts := m.now().Format("2006-01-02T15:04:05Z")

	status, err := json.Marshal(map[string]any{
		"DryRunId":     idgen.GenerateID("dryrun-"),
		"DryRunStatus": "succeeded",
		"CreationDate": ts,
		"UpdateDate":   ts,
	})
	if err != nil {
		return nil, err
	}

	return map[string]json.RawMessage{
		"DryRunProgressStatus": json.RawMessage(status),
	}, nil
}

// CancelDomainConfigChange returns an empty synthesized canceled-changes list.
func (m *Mock) CancelDomainConfigChange(_ context.Context, name string, _ bool) ([]map[string]json.RawMessage, error) {
	if _, err := m.getDomain(name); err != nil {
		return nil, err
	}

	return []map[string]json.RawMessage{}, nil
}
