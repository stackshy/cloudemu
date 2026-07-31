// Package keyspaces provides an in-memory mock of Amazon Keyspaces, the managed
// Apache Cassandra–compatible service. Keyspaces is control-plane only here, so
// this mock models keyspaces, tables (schema/capacity/encryption/PITR/TTL),
// user-defined types, tags, and provisioned auto-scaling settings.
package keyspaces

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
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

	opts *config.Options
}

// New creates a new Keyspaces mock with the account-default "system" keyspaces
// that real Amazon Keyspaces provisions.
func New(opts *config.Options) *Mock {
	m := &Mock{
		keyspaces: memstore.New[ksdriver.Keyspace](),
		tables:    memstore.New[ksdriver.Table](),
		udts:      memstore.New[ksdriver.UDT](),
		tags:      make(map[string]map[string]string),
		opts:      opts,
	}

	for _, sys := range []string{"system", "system_schema", "system_multiregion_info"} {
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

func validName(kind, name string) error {
	if name == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "%s name is required", kind)
	}

	if strings.Contains(name, "/") {
		return cerrors.Newf(cerrors.InvalidArgument, "%s name %q must not contain '/'", kind, name)
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

	strategy := cfg.ReplicationStrategy
	if strategy == "" {
		strategy = ksdriver.SingleRegion
	}

	const minMultiRegionRegions = 2

	regions := cfg.ReplicationRegions
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
		ReplicationStrategy: strategy, ReplicationRegions: regions, Tags: copyTags(cfg.Tags),
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

	if m.keyspaceHasTables(name) {
		return cerrors.Newf(cerrors.FailedPrecondition, "keyspace %q still contains tables", name)
	}

	m.keyspaces.Delete(name)
	delete(m.tags, ks.ARN)

	return nil
}

func (m *Mock) keyspaceHasTables(keyspace string) bool {
	prefix := keyspace + "/"
	for _, k := range m.tables.Keys() {
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
