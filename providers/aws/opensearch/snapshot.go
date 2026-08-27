package opensearch

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// opensearchSnapshot is the full serialized state of the AWS OpenSearch mock. The
// domains store holds an unexported *domainData (whose fields — status, config,
// tags, dataSrcs — are all unexported and invisible to json.Marshal), so it is
// promoted to an exported domainSnapshot keyed by domain name. Every other store
// holds a fully-exported *driver pointer type (or a plain string for the
// name-claim stores pkgNames/appNames), so each round-trips through the generic
// memstore helper under its exact key — preserving the composite keys used by
// pkgAssoc ("packageID|domainName") so cross-references survive. The mu-guarded
// defaultAppSet (application id -> raw default config) is captured beside the
// stores. The per-domain mutex and the wired opts are intentionally not
// serialized.
type opensearchSnapshot struct {
	Domains  map[string]*domainSnapshot `json:"domains,omitempty"`
	Packages json.RawMessage            `json:"packages,omitempty"`
	VpcEnds  json.RawMessage            `json:"vpcEnds,omitempty"`
	Inbound  json.RawMessage            `json:"inbound,omitempty"`
	Outbound json.RawMessage            `json:"outbound,omitempty"`
	Apps     json.RawMessage            `json:"apps,omitempty"`
	DQData   json.RawMessage            `json:"dqDataSrcs,omitempty"`
	Reserved json.RawMessage            `json:"reserved,omitempty"`
	PkgAssoc json.RawMessage            `json:"pkgAssoc,omitempty"`
	PkgNames json.RawMessage            `json:"pkgNames,omitempty"`
	AppNames json.RawMessage            `json:"appNames,omitempty"`

	DefaultAppSet map[string]json.RawMessage `json:"defaultAppSet,omitempty"`
}

// domainSnapshot is the exported form of domainData (all fields unexported). The
// per-domain mutex is transient and not carried.
type domainSnapshot struct {
	Status   driver.DomainStatus          `json:"status"`
	Config   driver.DomainConfig          `json:"config"`
	Tags     map[string]string            `json:"tags,omitempty"`
	DataSrcs map[string]driver.DataSource `json:"dataSrcs,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// OpenSearch is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := opensearchSnapshot{Domains: m.snapshotDomains()}
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.defaultAppMu.RLock()
	snap.DefaultAppSet = m.defaultAppSet
	m.defaultAppMu.RUnlock()

	return json.Marshal(snap)
}

func (m *Mock) snapshotDomains() map[string]*domainSnapshot {
	if m.domains.Len() == 0 {
		return nil
	}

	out := make(map[string]*domainSnapshot, m.domains.Len())

	for name, dd := range m.domains.All() {
		dd.mu.RLock()
		out[name] = &domainSnapshot{
			Status: dd.status, Config: dd.config, Tags: dd.tags, DataSrcs: dd.dataSrcs,
		}
		dd.mu.RUnlock()
	}

	return out
}

func (m *Mock) snapshotStores(snap *opensearchSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Packages, m.packages.Snapshot},
		{&snap.VpcEnds, m.vpcEnds.Snapshot},
		{&snap.Inbound, m.inbound.Snapshot},
		{&snap.Outbound, m.outbound.Snapshot},
		{&snap.Apps, m.apps.Snapshot},
		{&snap.DQData, m.dqDataSrcs.Snapshot},
		{&snap.Reserved, m.reserved.Snapshot},
		{&snap.PkgAssoc, m.pkgAssoc.Snapshot},
		{&snap.PkgNames, m.pkgNames.Snapshot},
		{&snap.AppNames, m.appNames.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("opensearch: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every domain
// name, package/application id, and connection id (and the id cross-references
// records hold) is preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap opensearchSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("opensearch: parse snapshot: %w", err)
	}

	m.restoreDomains(snap.Domains)

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.defaultAppMu.Lock()
	if snap.DefaultAppSet != nil {
		m.defaultAppSet = snap.DefaultAppSet
	}
	m.defaultAppMu.Unlock()

	return nil
}

func (m *Mock) restoreDomains(domains map[string]*domainSnapshot) {
	for name, ds := range domains {
		dd := &domainData{status: ds.Status, config: ds.Config, tags: ds.Tags, dataSrcs: ds.DataSrcs}
		if dd.dataSrcs == nil {
			dd.dataSrcs = map[string]driver.DataSource{}
		}

		m.domains.Set(name, dd)
	}
}

func (m *Mock) restoreStores(snap *opensearchSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Packages, m.packages.LoadSnapshot},
		{snap.VpcEnds, m.vpcEnds.LoadSnapshot},
		{snap.Inbound, m.inbound.LoadSnapshot},
		{snap.Outbound, m.outbound.LoadSnapshot},
		{snap.Apps, m.apps.LoadSnapshot},
		{snap.DQData, m.dqDataSrcs.LoadSnapshot},
		{snap.Reserved, m.reserved.LoadSnapshot},
		{snap.PkgAssoc, m.pkgAssoc.LoadSnapshot},
		{snap.PkgNames, m.pkgNames.LoadSnapshot},
		{snap.AppNames, m.appNames.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("opensearch: restore store: %w", err)
		}
	}

	return nil
}
