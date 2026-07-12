package azureai

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

// Compile-time check that Mock implements the MachineLearning surface.
var _ driver.MachineLearning = (*Mock)(nil)

const mlProvider = "Microsoft.MachineLearningServices"

func (m *Mock) mlID(resourceGroup, resourceType, name string) string {
	return idgen.AzureID(m.opts.AccountID, resourceGroup, mlProvider, resourceType, name)
}

// wsChildID builds an ARM ID for a resource nested under a workspace.
func (m *Mock) wsChildID(resourceGroup, workspace, childPath string) string {
	return m.mlID(resourceGroup, "workspaces", workspace) + "/" + childPath
}

// --- Workspaces ---

func cloneMLWorkspace(w *driver.MLWorkspace) *driver.MLWorkspace {
	out := *w
	out.Tags = copyMap(w.Tags)

	return &out
}

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateMLWorkspace(_ context.Context, cfg driver.MLWorkspaceConfig) (*driver.MLWorkspace, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "workspace name is required")
	case cfg.ResourceGroup == "":
		return nil, errors.New(errors.InvalidArgument, "resource group is required")
	case cfg.Location == "":
		return nil, errors.New(errors.InvalidArgument, "location is required")
	}

	k := key(cfg.ResourceGroup, cfg.Name)

	kind := cfg.Kind
	if kind == "" {
		kind = kindDefault
	}

	if existing, ok := m.mlWorkspaces.Get(k); ok {
		updated := *existing
		updated.FriendlyName = cfg.FriendlyName
		updated.Description = cfg.Description
		updated.Tags = copyMap(cfg.Tags)
		m.mlWorkspaces.Set(k, &updated)

		return cloneMLWorkspace(&updated), nil
	}

	ws := &driver.MLWorkspace{
		ID:                m.mlID(cfg.ResourceGroup, "workspaces", cfg.Name),
		Name:              cfg.Name,
		ResourceGroup:     cfg.ResourceGroup,
		Location:          cfg.Location,
		Kind:              kind,
		FriendlyName:      cfg.FriendlyName,
		Description:       cfg.Description,
		DiscoveryURL:      "https://" + orLower(cfg.Location) + ".api.azureml.ms/discovery",
		ProvisioningState: driver.StateSucceeded,
		Tags:              copyMap(cfg.Tags),
		CreatedAt:         m.now(),
	}
	m.mlWorkspaces.Set(k, ws)
	m.emitMetric("workspace/count", 1, map[string]string{"kind": kind})

	return cloneMLWorkspace(ws), nil
}

func orLower(s string) string {
	if s == "" {
		return "eastus"
	}

	return strings.ToLower(s)
}

func (m *Mock) GetMLWorkspace(_ context.Context, resourceGroup, name string) (*driver.MLWorkspace, error) {
	w, ok := m.mlWorkspaces.Get(key(resourceGroup, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "workspace %q not found", name)
	}

	return cloneMLWorkspace(w), nil
}

func (m *Mock) DeleteMLWorkspace(_ context.Context, resourceGroup, name string) error {
	if !m.mlWorkspaces.Delete(key(resourceGroup, name)) {
		return errors.Newf(errors.NotFound, "workspace %q not found", name)
	}

	return nil
}

func (m *Mock) UpdateMLWorkspaceTags(
	_ context.Context, resourceGroup, name string, tags map[string]string,
) (*driver.MLWorkspace, error) {
	k := key(resourceGroup, name)

	w, ok := m.mlWorkspaces.Get(k)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "workspace %q not found", name)
	}

	updated := *w
	updated.Tags = copyMap(tags)
	m.mlWorkspaces.Set(k, &updated)

	return cloneMLWorkspace(&updated), nil
}

func (m *Mock) ListMLWorkspacesByResourceGroup(_ context.Context, resourceGroup string) ([]driver.MLWorkspace, error) {
	out := make([]driver.MLWorkspace, 0)

	for _, w := range m.mlWorkspaces.All() {
		if w.ResourceGroup == resourceGroup {
			out = append(out, *cloneMLWorkspace(w))
		}
	}

	return out, nil
}

func (m *Mock) ListMLWorkspaces(_ context.Context) ([]driver.MLWorkspace, error) {
	all := m.mlWorkspaces.All()
	out := make([]driver.MLWorkspace, 0, len(all))

	for _, w := range all {
		out = append(out, *cloneMLWorkspace(w))
	}

	return out, nil
}

// requireWorkspace returns NotFound when the parent workspace is absent.
func (m *Mock) requireWorkspace(resourceGroup, workspace string) error {
	if !m.mlWorkspaces.Has(key(resourceGroup, workspace)) {
		return errors.Newf(errors.NotFound, "workspace %q not found", workspace)
	}

	return nil
}

// --- Computes ---

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateCompute(_ context.Context, cfg driver.ComputeConfig) (*driver.Compute, error) {
	if err := m.requireWorkspace(cfg.ResourceGroup, cfg.Workspace); err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "compute name is required")
	}

	ct := cfg.ComputeType
	if ct == "" {
		ct = "AmlCompute"
	}

	c := &driver.Compute{
		ID:                m.wsChildID(cfg.ResourceGroup, cfg.Workspace, "computes/"+cfg.Name),
		Name:              cfg.Name,
		ComputeType:       ct,
		VMSize:            cfg.VMSize,
		MinNodes:          cfg.MinNodes,
		MaxNodes:          cfg.MaxNodes,
		State:             "Running",
		ProvisioningState: driver.StateSucceeded,
		CreatedAt:         m.now(),
	}
	m.computes.Set(key(cfg.ResourceGroup, cfg.Workspace, "computes", cfg.Name), c)

	out := *c

	return &out, nil
}

func (m *Mock) GetCompute(_ context.Context, resourceGroup, workspace, name string) (*driver.Compute, error) {
	c, ok := m.computes.Get(key(resourceGroup, workspace, "computes", name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "compute %q not found", name)
	}

	out := *c

	return &out, nil
}

func (m *Mock) DeleteCompute(_ context.Context, resourceGroup, workspace, name string) error {
	if !m.computes.Delete(key(resourceGroup, workspace, "computes", name)) {
		return errors.Newf(errors.NotFound, "compute %q not found", name)
	}

	return nil
}

func (m *Mock) ListComputes(_ context.Context, resourceGroup, workspace string) ([]driver.Compute, error) {
	prefix := key(resourceGroup, workspace, "computes") + "/"
	out := make([]driver.Compute, 0)

	for k, c := range m.computes.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *c)
		}
	}

	return out, nil
}

func (m *Mock) StartCompute(_ context.Context, resourceGroup, workspace, name string) error {
	return m.setComputeState(resourceGroup, workspace, name, "Stopped", "Running")
}

func (m *Mock) StopCompute(_ context.Context, resourceGroup, workspace, name string) error {
	return m.setComputeState(resourceGroup, workspace, name, "Running", "Stopped")
}

func (m *Mock) RestartCompute(_ context.Context, resourceGroup, workspace, name string) error {
	k := key(resourceGroup, workspace, "computes", name)

	c, ok := m.computes.Get(k)
	if !ok {
		return errors.Newf(errors.NotFound, "compute %q not found", name)
	}

	updated := *c
	updated.State = "Running"
	m.computes.Set(k, &updated)

	return nil
}

// setComputeState copy-then-Sets a compute to target only from the required
// source state, rejecting illegal transitions like the real API.
func (m *Mock) setComputeState(resourceGroup, workspace, name, from, target string) error {
	k := key(resourceGroup, workspace, "computes", name)

	c, ok := m.computes.Get(k)
	if !ok {
		return errors.Newf(errors.NotFound, "compute %q not found", name)
	}

	if c.State != from {
		return errors.Newf(errors.FailedPrecondition, "compute %q is %s; cannot transition to %s", name, c.State, target)
	}

	updated := *c
	updated.State = target
	m.computes.Set(k, &updated)

	return nil
}
