package sesv2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// sesv2Snapshot is the full serialized state of the AWS SES v2 mock. Five stores
// hold unexported value types whose only stateful field is a driver value behind
// a per-record mutex — identityData, configSetData, and templateData wrap a
// single driver struct, while contactListData and tenantData additionally own a
// nested memstore of fully-exported entries — so all five are promoted to
// exported snapshot forms keyed by resource name (the nested stores round-trip
// through the generic memstore helper). Every other store holds a fully-exported
// *driver pointer type (or driver.SuppressedDestination) and round-trips through
// the generic helper. The mutex-guarded account state, the sent-message log, and
// the dashboard/VDM/auto-warmup flags are captured beside the stores. The wired
// opts and every per-record mutex are intentionally not serialized.
type sesv2Snapshot struct {
	Identities   map[string]*identitySnapshot    `json:"identities,omitempty"`
	ConfigSets   map[string]*configSetSnapshot   `json:"configSets,omitempty"`
	Templates    map[string]*templateSnapshot    `json:"templates,omitempty"`
	ContactLists map[string]*contactListSnapshot `json:"contactLists,omitempty"`
	Tenants      map[string]*tenantSnapshot      `json:"tenants,omitempty"`

	Suppressed   json.RawMessage `json:"suppressed,omitempty"`
	CVTemplates  json.RawMessage `json:"cvTemplates,omitempty"`
	IPPools      json.RawMessage `json:"ipPools,omitempty"`
	DedicatedIps json.RawMessage `json:"dedicatedIps,omitempty"`
	TestReports  json.RawMessage `json:"testReports,omitempty"`
	ImportJobs   json.RawMessage `json:"importJobs,omitempty"`
	ExportJobs   json.RawMessage `json:"exportJobs,omitempty"`
	RepEntities  json.RawMessage `json:"repEntities,omitempty"`
	Endpoints    json.RawMessage `json:"endpoints,omitempty"`

	Sent              []driver.SentMessage `json:"sent,omitempty"`
	Account           driver.Account       `json:"account"`
	DashboardEnabled  bool                 `json:"dashboardEnabled,omitempty"`
	VDMEnabled        bool                 `json:"vdmEnabled,omitempty"`
	AutoWarmupEnabled bool                 `json:"autoWarmupEnabled,omitempty"`
}

// identitySnapshot mirrors identityData (its driver.Identity is behind an
// unexported field). configSetSnapshot and templateSnapshot follow the same shape.
type identitySnapshot struct {
	Identity driver.Identity `json:"identity"`
}

type configSetSnapshot struct {
	ConfigurationSet driver.ConfigurationSet `json:"configurationSet"`
}

type templateSnapshot struct {
	Template driver.Template `json:"template"`
}

// contactListSnapshot mirrors contactListData, promoting its ContactList and its
// nested contacts store (fully-exported *driver.Contact, so it round-trips).
type contactListSnapshot struct {
	ContactList driver.ContactList `json:"contactList"`
	Contacts    json.RawMessage    `json:"contacts,omitempty"`
}

// tenantSnapshot mirrors tenantData, promoting its Tenant and its nested
// resources store (fully-exported driver.TenantResource).
type tenantSnapshot struct {
	Tenant    driver.Tenant   `json:"tenant"`
	Resources json.RawMessage `json:"resources,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// SES v2 retains message metadata, not object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := sesv2Snapshot{
		Identities: m.snapshotIdentities(),
		ConfigSets: m.snapshotConfigSets(),
		Templates:  m.snapshotTemplates(),
	}

	if err := m.snapshotNested(&snap); err != nil {
		return nil, err
	}

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.snapshotScalarState(&snap)

	return json.Marshal(snap)
}

func (m *Mock) snapshotIdentities() map[string]*identitySnapshot {
	if m.identities.Len() == 0 {
		return nil
	}

	out := make(map[string]*identitySnapshot, m.identities.Len())

	for name, d := range m.identities.All() {
		d.mu.RLock()
		out[name] = &identitySnapshot{Identity: d.id}
		d.mu.RUnlock()
	}

	return out
}

func (m *Mock) snapshotConfigSets() map[string]*configSetSnapshot {
	if m.configSets.Len() == 0 {
		return nil
	}

	out := make(map[string]*configSetSnapshot, m.configSets.Len())

	for name, d := range m.configSets.All() {
		d.mu.RLock()
		out[name] = &configSetSnapshot{ConfigurationSet: d.cs}
		d.mu.RUnlock()
	}

	return out
}

func (m *Mock) snapshotTemplates() map[string]*templateSnapshot {
	if m.templates.Len() == 0 {
		return nil
	}

	out := make(map[string]*templateSnapshot, m.templates.Len())

	for name, d := range m.templates.All() {
		d.mu.RLock()
		out[name] = &templateSnapshot{Template: d.tpl}
		d.mu.RUnlock()
	}

	return out
}

func (m *Mock) snapshotNested(snap *sesv2Snapshot) error {
	if m.contactLists.Len() > 0 {
		snap.ContactLists = make(map[string]*contactListSnapshot, m.contactLists.Len())

		for name, d := range m.contactLists.All() {
			d.mu.RLock()
			contacts, err := d.contacts.Snapshot()
			cl := d.cl
			d.mu.RUnlock()

			if err != nil {
				return fmt.Errorf("sesv2: snapshot contacts: %w", err)
			}

			snap.ContactLists[name] = &contactListSnapshot{ContactList: cl, Contacts: contacts}
		}
	}

	if m.tenants.Len() > 0 {
		snap.Tenants = make(map[string]*tenantSnapshot, m.tenants.Len())

		for name, d := range m.tenants.All() {
			d.mu.RLock()
			resources, err := d.resources.Snapshot()
			tn := d.t
			d.mu.RUnlock()

			if err != nil {
				return fmt.Errorf("sesv2: snapshot tenant resources: %w", err)
			}

			snap.Tenants[name] = &tenantSnapshot{Tenant: tn, Resources: resources}
		}
	}

	return nil
}

func (m *Mock) snapshotStores(snap *sesv2Snapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Suppressed, m.suppressed.Snapshot},
		{&snap.CVTemplates, m.cvTemplates.Snapshot},
		{&snap.IPPools, m.ipPools.Snapshot},
		{&snap.DedicatedIps, m.dedicatedIps.Snapshot},
		{&snap.TestReports, m.testReports.Snapshot},
		{&snap.ImportJobs, m.importJobs.Snapshot},
		{&snap.ExportJobs, m.exportJobs.Snapshot},
		{&snap.RepEntities, m.repEntities.Snapshot},
		{&snap.Endpoints, m.endpoints.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("sesv2: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// snapshotScalarState captures the mutex-guarded account, sent log, and dashboard
// flags.
func (m *Mock) snapshotScalarState(snap *sesv2Snapshot) {
	m.sentMu.RLock()
	if len(m.sent) > 0 {
		snap.Sent = append([]driver.SentMessage(nil), m.sent...)
	}
	m.sentMu.RUnlock()

	m.acctMu.RLock()
	snap.Account = m.account
	m.acctMu.RUnlock()

	m.dashMu.RLock()
	snap.DashboardEnabled = m.dashboardEnabled
	snap.VDMEnabled = m.vdmEnabled
	snap.AutoWarmupEnabled = m.autoWarmupEnabled
	m.dashMu.RUnlock()
}

// Restore rebuilds the mock's state under the original identities: every identity
// name, config-set name, template name, contact-list/tenant name (and the nested
// contacts/resources under them) is preserved, so a restore is transparent to
// clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap sesv2Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("sesv2: parse snapshot: %w", err)
	}

	m.restorePromoted(&snap)

	if err := m.restoreNested(&snap); err != nil {
		return err
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.restoreScalarState(&snap)

	return nil
}

func (m *Mock) restorePromoted(snap *sesv2Snapshot) {
	for name, s := range snap.Identities {
		m.identities.Set(name, &identityData{id: s.Identity})
	}

	for name, s := range snap.ConfigSets {
		m.configSets.Set(name, &configSetData{cs: s.ConfigurationSet})
	}

	for name, s := range snap.Templates {
		m.templates.Set(name, &templateData{tpl: s.Template})
	}
}

func (m *Mock) restoreNested(snap *sesv2Snapshot) error {
	for name, s := range snap.ContactLists {
		cld := &contactListData{cl: s.ContactList, contacts: memstore.New[*driver.Contact]()}
		if len(s.Contacts) > 0 {
			if err := cld.contacts.LoadSnapshot(s.Contacts); err != nil {
				return fmt.Errorf("sesv2: restore contacts: %w", err)
			}
		}

		m.contactLists.Set(name, cld)
	}

	for name, s := range snap.Tenants {
		td := &tenantData{t: s.Tenant, resources: memstore.New[driver.TenantResource]()}
		if len(s.Resources) > 0 {
			if err := td.resources.LoadSnapshot(s.Resources); err != nil {
				return fmt.Errorf("sesv2: restore tenant resources: %w", err)
			}
		}

		m.tenants.Set(name, td)
	}

	return nil
}

func (m *Mock) restoreStores(snap *sesv2Snapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Suppressed, m.suppressed.LoadSnapshot},
		{snap.CVTemplates, m.cvTemplates.LoadSnapshot},
		{snap.IPPools, m.ipPools.LoadSnapshot},
		{snap.DedicatedIps, m.dedicatedIps.LoadSnapshot},
		{snap.TestReports, m.testReports.LoadSnapshot},
		{snap.ImportJobs, m.importJobs.LoadSnapshot},
		{snap.ExportJobs, m.exportJobs.LoadSnapshot},
		{snap.RepEntities, m.repEntities.LoadSnapshot},
		{snap.Endpoints, m.endpoints.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("sesv2: restore store: %w", err)
		}
	}

	return nil
}

func (m *Mock) restoreScalarState(snap *sesv2Snapshot) {
	m.sentMu.Lock()
	if snap.Sent != nil {
		m.sent = snap.Sent
	}
	m.sentMu.Unlock()

	m.acctMu.Lock()
	m.account = snap.Account
	m.acctMu.Unlock()

	m.dashMu.Lock()
	m.dashboardEnabled = snap.DashboardEnabled
	m.vdmEnabled = snap.VDMEnabled
	m.autoWarmupEnabled = snap.AutoWarmupEnabled
	m.dashMu.Unlock()
}
