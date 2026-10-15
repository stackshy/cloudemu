// Package keyspaces provides an in-memory mock of Amazon Keyspaces, the managed
// Apache Cassandra-compatible service. Keyspaces is control-plane only here, so
// this mock models keyspaces, tables (schema/capacity/encryption/PITR/TTL),
// user-defined types, tags, and provisioned auto-scaling settings.
package keyspaces

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

var (
	_ ksdriver.Keyspaces   = (*Mock)(nil)
	_ ksdriver.AutoScaling = (*Mock)(nil)
)

// Mock is the in-memory Amazon Keyspaces implementation.
type Mock struct {
	mu sync.RWMutex

	keyspaces *memstore.Store[ksdriver.Keyspace]
	tables    *memstore.Store[ksdriver.Table] // keyed by "keyspace/table"
	udts      *memstore.Store[ksdriver.UDT]   // keyed by "keyspace/typename"

	// tags holds tag maps keyed by resource ARN.
	tags map[string]map[string]string

	// tableSettle overlays a transient CREATING/UPDATING/RESTORING window (keyed by
	// "keyspace/table") over a table's stored "ACTIVE" status so
	// create/update/restore report the real Keyspaces intermediate state before
	// settling. It is a no-op unless config.Options.AsyncSettle is set
	// (SettleDuration returns 0 → inactive window → historical synchronous
	// behavior). The Set has its own lock. Keyspaces themselves expose no status in
	// the API, so only tables are overlaid.
	tableSettle *settle.Set

	opts *config.Options
}

// New creates a new Keyspaces mock with the account-default "system" keyspaces
// that real Amazon Keyspaces provisions.
func New(opts *config.Options) *Mock {
	m := &Mock{
		keyspaces:   memstore.New[ksdriver.Keyspace](),
		tables:      memstore.New[ksdriver.Table](),
		udts:        memstore.New[ksdriver.UDT](),
		tags:        make(map[string]map[string]string),
		tableSettle: settle.NewSet(),
		opts:        opts,
	}

	for _, sys := range systemKeyspaceNames() {
		m.keyspaces.Set(sys, ksdriver.Keyspace{
			Name: sys, ARN: m.keyspaceARN(sys), ReplicationStrategy: ksdriver.SingleRegion,
			ReplicationRegions: []string{opts.Region},
		})
	}

	return m
}

func (m *Mock) keyspaceARN(name string) string {
	return fmt.Sprintf("arn:aws:cassandra:%s:%s:/keyspace/%s/", m.opts.Region, m.opts.AccountID, name)
}

func (m *Mock) tableARN(keyspace, table string) string {
	return fmt.Sprintf("arn:aws:cassandra:%s:%s:/keyspace/%s/table/%s/", m.opts.Region, m.opts.AccountID, keyspace, table)
}

func tableKey(keyspace, table string) string { return keyspace + "/" + table }

func typeKey(keyspace, name string) string { return keyspace + "/" + name }

// maxNameLen is the documented maximum length of keyspace, table, and
// user-defined type names (API reference KeyspaceName/TableName/TypeName:
// "Maximum length of 48"; devguide quotas: "Max UDT name length 48").
const maxNameLen = 48

// keyspaceTableNamePattern is the documented KeyspaceName/TableName pattern
// ([a-zA-Z0-9][a-zA-Z0-9_]{0,47}), anchored: alphanumerics and underscores
// only, not starting with an underscore. A leading digit is allowed.
var keyspaceTableNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_]{0,47}$`)

// validName validates a keyspace or table name against the documented
// KeyspaceName/TableName constraints.
func validName(kind, name string) error {
	if name == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "%s name is required", kind)
	}

	if len(name) > maxNameLen {
		return cerrors.Newf(cerrors.InvalidArgument,
			"%s name %q must be at most %d characters", kind, name, maxNameLen)
	}

	if !keyspaceTableNamePattern.MatchString(name) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"%s name %q must match pattern [a-zA-Z0-9][a-zA-Z0-9_]{0,47}", kind, name)
	}

	return nil
}

// validTypeName validates a user-defined type name. The API model gives
// TypeName only a 1-48 length constraint (no pattern), so only emptiness,
// length, and the '/' used in internal store keys are checked.
func validTypeName(name string) error {
	if name == "" {
		return cerrors.New(cerrors.InvalidArgument, "type name is required")
	}

	if len(name) > maxNameLen {
		return cerrors.Newf(cerrors.InvalidArgument,
			"type name %q must be at most %d characters", name, maxNameLen)
	}

	if strings.Contains(name, "/") {
		return cerrors.Newf(cerrors.InvalidArgument, "type name %q must not contain '/'", name)
	}

	return nil
}

func copyTags(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

func cloneKeyspace(in *ksdriver.Keyspace) ksdriver.Keyspace {
	k := *in
	k.ReplicationRegions = append([]string(nil), k.ReplicationRegions...)
	k.Tags = copyTags(k.Tags)

	return k
}

// ---- Keyspaces ----

// CreateKeyspace creates a keyspace with single- or multi-region replication.
func (m *Mock) CreateKeyspace(_ context.Context, cfg ksdriver.CreateKeyspaceConfig) (*ksdriver.Keyspace, error) {
	if err := validName("keyspace", cfg.Name); err != nil {
		return nil, err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	strategy := cfg.ReplicationStrategy
	if strategy == "" {
		strategy = ksdriver.SingleRegion
	}

	if strategy != ksdriver.SingleRegion && strategy != ksdriver.MultiRegion {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"replicationStrategy %q must be SINGLE_REGION or MULTI_REGION", strategy)
	}

	const minMultiRegionRegions = 2

	// Clone the caller's slice so a later mutation can't reach into the store.
	regions := append([]string(nil), cfg.ReplicationRegions...)
	if strategy == ksdriver.SingleRegion {
		regions = []string{m.opts.Region}
	} else if len(regions) < minMultiRegionRegions {
		return nil, cerrors.New(cerrors.InvalidArgument, "MULTI_REGION replication requires at least two regions")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.keyspaces.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "keyspace %q already exists", cfg.Name)
	}

	ks := ksdriver.Keyspace{
		Name: cfg.Name, ARN: m.keyspaceARN(cfg.Name),
		ReplicationStrategy: strategy, ReplicationRegions: regions,
	}
	m.keyspaces.Set(cfg.Name, ks)
	m.setTags(ks.ARN, cfg.Tags)

	out := cloneKeyspace(&ks)

	return &out, nil
}

// GetKeyspace returns a keyspace by name.
func (m *Mock) GetKeyspace(_ context.Context, name string) (*ksdriver.Keyspace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ks, ok := m.keyspaces.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "keyspace %q not found", name)
	}

	out := cloneKeyspace(&ks)

	return &out, nil
}

// ListKeyspaces returns all keyspaces in deterministic order.
func (m *Mock) ListKeyspaces(_ context.Context) ([]ksdriver.Keyspace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.keyspaces.SortedValues()
	out := make([]ksdriver.Keyspace, 0, len(all))

	for i := range all {
		out = append(out, cloneKeyspace(&all[i]))
	}

	return out, nil
}

// UpdateKeyspace adds regions to a keyspace's replication (single→multi).
func (m *Mock) UpdateKeyspace(_ context.Context, name string, addRegions []string) (*ksdriver.Keyspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ks, ok := m.keyspaces.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "keyspace %q not found", name)
	}

	for _, r := range addRegions {
		if !containsStr(ks.ReplicationRegions, r) {
			ks.ReplicationRegions = append(ks.ReplicationRegions, r)
		}
	}

	if len(ks.ReplicationRegions) > 1 {
		ks.ReplicationStrategy = ksdriver.MultiRegion
	}

	m.keyspaces.Set(name, ks)

	out := cloneKeyspace(&ks)

	return &out, nil
}

// DeleteKeyspace removes a keyspace; it must contain no tables.
func (m *Mock) DeleteKeyspace(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ks, ok := m.keyspaces.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "keyspace %q not found", name)
	}

	if isSystemKeyspace(name) {
		return cerrors.Newf(cerrors.InvalidArgument, "system keyspace %q cannot be deleted", name)
	}

	if m.keyspaceHasTables(name) {
		return cerrors.Newf(cerrors.FailedPrecondition, "keyspace %q still contains tables", name)
	}

	if m.keyspaceHasTypes(name) {
		return cerrors.Newf(cerrors.FailedPrecondition, "keyspace %q still contains user-defined types", name)
	}

	m.keyspaces.Delete(name)
	delete(m.tags, ks.ARN)

	return nil
}

// systemKeyspaceNames lists the four read-only system keyspaces every account
// has ("Amazon Keyspaces uses four system keyspaces", devguide
// working-with-keyspaces.html).
func systemKeyspaceNames() []string {
	return []string{"system", "system_schema", "system_schema_mcs", "system_multiregion_info"}
}

func isSystemKeyspace(name string) bool {
	return containsStr(systemKeyspaceNames(), name)
}

func (m *Mock) keyspaceHasTables(keyspace string) bool {
	return hasPrefixKey(m.tables.Keys(), keyspace+"/")
}

func (m *Mock) keyspaceHasTypes(keyspace string) bool {
	return hasPrefixKey(m.udts.Keys(), keyspace+"/")
}

func hasPrefixKey(keys []string, prefix string) bool {
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}

	return false
}

func containsStr(items []string, s string) bool {
	for _, v := range items {
		if v == s {
			return true
		}
	}

	return false
}
