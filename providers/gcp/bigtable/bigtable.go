// Package bigtable provides an in-memory mock of the Google Cloud Bigtable
// Admin API. It is control-plane only, modeling instances and their clusters,
// tables (with column families), app profiles, and backups, plus per-resource
// IAM policies and long-running operations.
package bigtable

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

const (
	defaultServeNodes  = 3
	defaultStorageType = "SSD"
	maxServeNodes      = 3000
)

var _ btdriver.Admin = (*Mock)(nil)

// Mock is the in-memory Bigtable Admin implementation. All stores are keyed by
// the resource's full GCP name (projects/{p}/instances/{i}/...).
type Mock struct {
	mu sync.RWMutex

	instances   *memstore.Store[btdriver.Instance]
	clusters    *memstore.Store[btdriver.Cluster]
	tables      *memstore.Store[btdriver.Table]
	appProfiles *memstore.Store[btdriver.AppProfile]
	backups     *memstore.Store[btdriver.Backup]
	operations  *memstore.Store[btdriver.Operation]

	policies map[string]btdriver.Policy

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Bigtable Admin mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances:   memstore.New[btdriver.Instance](),
		clusters:    memstore.New[btdriver.Cluster](),
		tables:      memstore.New[btdriver.Table](),
		appProfiles: memstore.New[btdriver.AppProfile](),
		backups:     memstore.New[btdriver.Backup](),
		operations:  memstore.New[btdriver.Operation](),
		policies:    make(map[string]btdriver.Policy),
		opts:        opts,
	}
}

// newOp records a done long-running operation targeting a resource and returns
// it. The caller holds the write lock.
func (m *Mock) newOp(opType, target string) *btdriver.Operation {
	op := btdriver.Operation{
		Name:       fmt.Sprintf("operations/bigtable-%s-%d", opType, m.opSeq.Add(1)),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

func copyLabels(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

// ---- Instances ----

func cloneInstance(in *btdriver.Instance) btdriver.Instance {
	i := *in
	i.Labels = copyLabels(i.Labels)

	return i
}

// CreateInstance creates an instance and its initial clusters.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateInstance(_ context.Context, cfg btdriver.CreateInstanceConfig) (*btdriver.Instance, *btdriver.Operation, error) {
	if len(cfg.Clusters) == 0 {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "an instance requires at least one cluster")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.instances.Has(cfg.Name) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "instance %q already exists", cfg.Name)
	}

	// Validate every initial cluster up front so the create is all-or-nothing:
	// a bad cluster (or a duplicate name that would silently collapse) must not
	// leave a live cluster-less or under-provisioned instance behind.
	seen := make(map[string]bool, len(cfg.Clusters))

	for i := range cfg.Clusters {
		if err := validateServeNodes(cfg.Clusters[i].ServeNodes); err != nil {
			return nil, nil, err
		}

		if seen[cfg.Clusters[i].Name] {
			return nil, nil, cerrors.Newf(cerrors.InvalidArgument, "duplicate cluster %q", cfg.Clusters[i].Name)
		}

		seen[cfg.Clusters[i].Name] = true
	}

	inst := btdriver.Instance{
		Name:        cfg.Name,
		DisplayName: orDefault(cfg.DisplayName, lastSegment(cfg.Name)),
		Type:        orDefault(cfg.Type, "PRODUCTION"),
		State:       btdriver.StateReady,
		Labels:      copyLabels(cfg.Labels),
		CreateTime:  m.opts.Clock.Now().UTC(),
	}
	m.instances.Set(cfg.Name, inst)

	for i := range cfg.Clusters {
		if err := m.putClusterLocked(cfg.Clusters[i]); err != nil {
			return nil, nil, err
		}
	}

	op := m.newOp("create-instance", cfg.Name)
	out := cloneInstance(&inst)

	return &out, op, nil
}

// GetInstance returns an instance by full name.
func (m *Mock) GetInstance(_ context.Context, name string) (*btdriver.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	inst, ok := m.instances.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", name)
	}

	out := cloneInstance(&inst)

	return &out, nil
}

// ListInstances returns all instances in a project.
func (m *Mock) ListInstances(_ context.Context, project string) ([]btdriver.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/instances/"
	all := m.instances.SortedValues()
	out := make([]btdriver.Instance, 0, len(all))

	for i := range all {
		if strings.HasPrefix(all[i].Name, prefix) {
			out = append(out, cloneInstance(&all[i]))
		}
	}

	return out, nil
}

func (m *Mock) updateInstanceLocked(name string, cfg btdriver.UpdateInstanceConfig) (btdriver.Instance, error) {
	inst, ok := m.instances.Get(name)
	if !ok {
		return btdriver.Instance{}, cerrors.Newf(cerrors.NotFound, "instance %q not found", name)
	}

	inst.DisplayName = orKeep(cfg.DisplayName, inst.DisplayName)
	inst.Type = orKeep(cfg.Type, inst.Type)

	if cfg.Labels != nil {
		inst.Labels = copyLabels(cfg.Labels)
	}

	m.instances.Set(name, inst)

	return inst, nil
}

// UpdateInstance replaces an instance's mutable fields (synchronous Update RPC).
func (m *Mock) UpdateInstance(_ context.Context, name string, cfg btdriver.UpdateInstanceConfig) (*btdriver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, err := m.updateInstanceLocked(name, cfg)
	if err != nil {
		return nil, err
	}

	out := cloneInstance(&inst)

	return &out, nil
}

// PartialUpdateInstance is the LRO variant of UpdateInstance.
func (m *Mock) PartialUpdateInstance(
	_ context.Context, name string, cfg btdriver.UpdateInstanceConfig,
) (*btdriver.Instance, *btdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, err := m.updateInstanceLocked(name, cfg)
	if err != nil {
		return nil, nil, err
	}

	op := m.newOp("update-instance", name)
	out := cloneInstance(&inst)

	return &out, op, nil
}

// DeleteInstance removes an instance and cascade-deletes its clusters, tables,
// app profiles, and backups.
func (m *Mock) DeleteInstance(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.instances.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", name)
	}

	prefix := name + "/"

	deletePrefixed(m.clusters, prefix)
	deletePrefixed(m.tables, prefix)
	deletePrefixed(m.appProfiles, prefix)
	deletePrefixed(m.backups, prefix)
	m.instances.Delete(name)

	for k := range m.policies {
		if k == name || strings.HasPrefix(k, prefix) {
			delete(m.policies, k)
		}
	}

	return nil
}

func deletePrefixed[T any](store *memstore.Store[T], prefix string) {
	for _, k := range store.Keys() {
		if strings.HasPrefix(k, prefix) {
			store.Delete(k)
		}
	}
}

// ---- Operations ----

// GetOperation returns a (done) long-running operation by name.
func (m *Mock) GetOperation(_ context.Context, name string) (*btdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		// Unknown operations are reported as done — the mock completes
		// synchronously, so any op id the SDK polls has already finished.
		return &btdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// ---- IAM ----

// GetIamPolicy returns the IAM policy on a resource (empty if unset).
func (m *Mock) GetIamPolicy(_ context.Context, resource string) (*btdriver.Policy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.policies[resource]
	if !ok {
		return &btdriver.Policy{Etag: "ACAB"}, nil
	}

	out := clonePolicy(&p)

	return &out, nil
}

// SetIamPolicy replaces the IAM policy on a resource.
func (m *Mock) SetIamPolicy(_ context.Context, resource string, policy btdriver.Policy) (*btdriver.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stored := clonePolicy(&policy)
	if stored.Etag == "" {
		stored.Etag = "ACAB"
	}

	m.policies[resource] = stored

	out := clonePolicy(&stored)

	return &out, nil
}

// rolePermissions maps a Bigtable IAM role to the permissions it confers. A nil
// set means the role grants every permission (admin).
//
//nolint:gochecknoglobals // static IAM role -> permission lookup table
var rolePermissions = map[string]map[string]bool{
	"roles/bigtable.admin": nil,
	"roles/bigtable.user": {
		"bigtable.tables.readRows": true, "bigtable.tables.mutateRows": true,
		"bigtable.tables.sampleRowKeys": true, "bigtable.tables.get": true,
		"bigtable.tables.list": true, "bigtable.instances.get": true,
	},
	"roles/bigtable.reader": {
		"bigtable.tables.readRows": true, "bigtable.tables.sampleRowKeys": true,
		"bigtable.tables.get": true, "bigtable.tables.list": true,
	},
	"roles/bigtable.viewer": {
		"bigtable.tables.get": true, "bigtable.tables.list": true, "bigtable.instances.get": true,
	},
}

// roleGrants reports whether a role confers a permission.
func roleGrants(role, perm string) bool {
	set, known := rolePermissions[role]
	if !known {
		return false
	}

	return set == nil || set[perm] // nil set = admin (all permissions)
}

// TestIamPermissions returns the subset of the requested permissions that the
// resource's stored policy actually confers (empty when no policy is set).
func (m *Mock) TestIamPermissions(_ context.Context, resource string, permissions []string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	policy := m.policies[resource]

	granted := make([]string, 0, len(permissions))

	for _, perm := range permissions {
		for i := range policy.Bindings {
			if roleGrants(policy.Bindings[i].Role, perm) {
				granted = append(granted, perm)
				break
			}
		}
	}

	return granted, nil
}

func clonePolicy(in *btdriver.Policy) btdriver.Policy {
	p := *in
	p.Bindings = make([]btdriver.Binding, len(in.Bindings))

	for i := range in.Bindings {
		b := in.Bindings[i]
		b.Members = append([]string(nil), in.Bindings[i].Members...)
		p.Bindings[i] = b
	}

	return p
}

// ---- shared helpers ----

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

func orKeep(v, cur string) string {
	if v == "" {
		return cur
	}

	return v
}

func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

const minResourceSegments = 2

// parentName strips the trailing "/{collection}/{id}" from a full resource name.
func parentName(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) < minResourceSegments {
		return ""
	}

	return strings.Join(parts[:len(parts)-2], "/")
}
