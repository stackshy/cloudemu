// Package databricks provides an in-memory mock implementation of Azure
// Databricks workspace management (Microsoft.Databricks/workspaces).
package databricks

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

// Compile-time checks that Mock implements both Databricks interfaces.
var (
	_ driver.Databricks = (*Mock)(nil)
	_ driver.DataPlane  = (*Mock)(nil)
)

const (
	providerNamespace = "Microsoft.Databricks"
	resourceType      = "workspaces"
	defaultSKU        = "standard"

	// urlShardModulo bounds the synthetic regional shard in a workspace URL
	// (adb-{id}.{shard}.azuredatabricks.net), matching Azure's 1–2 digit shard.
	urlShardModulo = 100
)

// Mock is an in-memory mock implementation of the Azure Databricks service,
// covering both ARM workspace management and the workspace data plane.
type Mock struct {
	workspaces  *memstore.Store[*driver.Workspace]
	pools       *memstore.Store[*driver.InstancePool]
	clusters    *memstore.Store[*driver.Cluster]
	jobs        *memstore.Store[*driver.Job]
	runs        *memstore.Store[*driver.Run]
	policies    *memstore.Store[*driver.ClusterPolicy]
	libraries   *memstore.Store[[]driver.LibraryStatus]
	permissions *memstore.Store[*driver.ObjectPermissions]
	opts        *config.Options

	jobSeq atomic.Int64
	runSeq atomic.Int64
}

// New creates a new Databricks mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		workspaces:  memstore.New[*driver.Workspace](),
		pools:       memstore.New[*driver.InstancePool](),
		clusters:    memstore.New[*driver.Cluster](),
		jobs:        memstore.New[*driver.Job](),
		runs:        memstore.New[*driver.Run](),
		policies:    memstore.New[*driver.ClusterPolicy](),
		libraries:   memstore.New[[]driver.LibraryStatus](),
		permissions: memstore.New[*driver.ObjectPermissions](),
		opts:        opts,
	}
}

// key uniquely identifies a workspace within the subscription: workspace names
// are unique per resource group.
func key(resourceGroup, name string) string {
	return resourceGroup + "/" + name
}

// CreateWorkspace creates a workspace, completing provisioning synchronously.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateWorkspace(_ context.Context, cfg driver.WorkspaceConfig) (*driver.Workspace, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "workspace name is required")
	case cfg.ResourceGroup == "":
		return nil, errors.New(errors.InvalidArgument, "resource group is required")
	case cfg.Location == "":
		return nil, errors.New(errors.InvalidArgument, "location is required")
	case cfg.ManagedResourceGroupID == "":
		return nil, errors.New(errors.InvalidArgument, "managedResourceGroupId is required")
	}

	k := key(cfg.ResourceGroup, cfg.Name)

	if existing, ok := m.workspaces.Get(k); ok {
		// ARM PUT is create-or-update: apply the mutable fields (tags, SKU) to
		// a copy and swap it in, preserving the identity fields (ID, workspace
		// ID/URL, created time). Location and managed RG are immutable in real
		// Azure, so they are left untouched.
		updated := *existing
		updated.Tags = copyMap(cfg.Tags)
		updated.SKUName = skuOrDefault(cfg.SKUName)
		updated.SKUTier = cfg.SKUTier
		m.workspaces.Set(k, &updated)

		return cloneWorkspace(&updated), nil
	}

	wsID := workspaceID(k)
	ws := &driver.Workspace{
		ID:                     idgen.AzureID(m.opts.AccountID, cfg.ResourceGroup, providerNamespace, resourceType, cfg.Name),
		Name:                   cfg.Name,
		ResourceGroup:          cfg.ResourceGroup,
		Location:               cfg.Location,
		SKUName:                skuOrDefault(cfg.SKUName),
		SKUTier:                cfg.SKUTier,
		ManagedResourceGroupID: cfg.ManagedResourceGroupID,
		WorkspaceID:            wsID,
		WorkspaceURL:           fmt.Sprintf("adb-%s.%d.azuredatabricks.net", wsID, hash(k)%urlShardModulo),
		ProvisioningState:      driver.StateSucceeded,
		Tags:                   copyMap(cfg.Tags),
		CreatedAt:              m.opts.Clock.Now().UTC().Format(time.RFC3339),
	}

	m.workspaces.Set(k, ws)

	return cloneWorkspace(ws), nil
}

// GetWorkspace returns a workspace by resource group and name.
func (m *Mock) GetWorkspace(_ context.Context, resourceGroup, name string) (*driver.Workspace, error) {
	ws, ok := m.workspaces.Get(key(resourceGroup, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "workspace %q not found", name)
	}

	return cloneWorkspace(ws), nil
}

// DeleteWorkspace deletes a workspace by resource group and name.
func (m *Mock) DeleteWorkspace(_ context.Context, resourceGroup, name string) error {
	if !m.workspaces.Delete(key(resourceGroup, name)) {
		return errors.Newf(errors.NotFound, "workspace %q not found", name)
	}

	return nil
}

// UpdateWorkspaceTags replaces a workspace's tags.
func (m *Mock) UpdateWorkspaceTags(_ context.Context, resourceGroup, name string, tags map[string]string) (*driver.Workspace, error) {
	k := key(resourceGroup, name)

	ws, ok := m.workspaces.Get(k)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "workspace %q not found", name)
	}

	// Mutate a copy and swap it in rather than writing the shared struct in
	// place, so concurrent readers never observe a torn update.
	updated := *ws
	updated.Tags = copyMap(tags)
	m.workspaces.Set(k, &updated)

	return cloneWorkspace(&updated), nil
}

// ListWorkspacesByResourceGroup lists workspaces in a resource group.
func (m *Mock) ListWorkspacesByResourceGroup(_ context.Context, resourceGroup string) ([]driver.Workspace, error) {
	out := make([]driver.Workspace, 0)

	for _, ws := range m.workspaces.All() {
		if ws.ResourceGroup == resourceGroup {
			out = append(out, *cloneWorkspace(ws))
		}
	}

	return out, nil
}

// ListWorkspaces lists all workspaces in the subscription.
func (m *Mock) ListWorkspaces(_ context.Context) ([]driver.Workspace, error) {
	all := m.workspaces.All()
	out := make([]driver.Workspace, 0, len(all))

	for _, ws := range all {
		out = append(out, *cloneWorkspace(ws))
	}

	return out, nil
}

func skuOrDefault(name string) string {
	if name == "" {
		return defaultSKU
	}

	return name
}

func cloneWorkspace(ws *driver.Workspace) *driver.Workspace {
	clone := *ws
	clone.Tags = copyMap(ws.Tags)

	return &clone
}

func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// hash returns a deterministic 32-bit FNV hash of s.
func hash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))

	return h.Sum32()
}

// workspaceID derives a deterministic numeric workspace ID from the key.
func workspaceID(k string) string {
	return fmt.Sprintf("%d", uint64(hash(k))*uint64(hash(k+".")))
}
