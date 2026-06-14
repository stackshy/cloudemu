package azureai

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/azureai/driver"
	"github.com/stackshy/cloudemu/errors"
)

// --- Endpoints (online + batch) ---

func endpointCollection(kind string) string {
	if strings.EqualFold(kind, "batch") {
		return "batchEndpoints"
	}

	return "onlineEndpoints"
}

func cloneEndpoint(e *driver.Endpoint) *driver.Endpoint {
	out := *e

	if e.Traffic != nil {
		out.Traffic = make(map[string]int, len(e.Traffic))
		for k, v := range e.Traffic {
			out.Traffic[k] = v
		}
	}

	return &out
}

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateEndpoint(_ context.Context, cfg driver.EndpointConfig) (*driver.Endpoint, error) {
	if err := m.requireWorkspace(cfg.ResourceGroup, cfg.Workspace); err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "endpoint name is required")
	}

	coll := endpointCollection(cfg.Kind)

	auth := cfg.AuthMode
	if auth == "" {
		auth = "Key"
	}

	e := &driver.Endpoint{
		ID:                m.wsChildID(cfg.ResourceGroup, cfg.Workspace, coll+"/"+cfg.Name),
		Name:              cfg.Name,
		Kind:              cfg.Kind,
		AuthMode:          auth,
		Description:       cfg.Description,
		ScoringURI:        "https://" + cfg.Name + ".inference.ml.azure.com/score",
		ProvisioningState: driver.StateSucceeded,
		Traffic:           map[string]int{},
		CreatedAt:         m.now(),
	}
	m.mlEndpoints.Set(key(cfg.ResourceGroup, cfg.Workspace, coll, cfg.Name), e)

	return cloneEndpoint(e), nil
}

func (m *Mock) GetEndpoint(_ context.Context, resourceGroup, workspace, kind, name string) (*driver.Endpoint, error) {
	e, ok := m.mlEndpoints.Get(key(resourceGroup, workspace, endpointCollection(kind), name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", name)
	}

	return cloneEndpoint(e), nil
}

func (m *Mock) DeleteEndpoint(_ context.Context, resourceGroup, workspace, kind, name string) error {
	if !m.mlEndpoints.Delete(key(resourceGroup, workspace, endpointCollection(kind), name)) {
		return errors.Newf(errors.NotFound, "endpoint %q not found", name)
	}

	return nil
}

func (m *Mock) ListEndpoints(_ context.Context, resourceGroup, workspace, kind string) ([]driver.Endpoint, error) {
	prefix := key(resourceGroup, workspace, endpointCollection(kind)) + "/"
	out := make([]driver.Endpoint, 0)

	for k, e := range m.mlEndpoints.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *cloneEndpoint(e))
		}
	}

	return out, nil
}

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateEndpointDeployment(_ context.Context, cfg driver.EndpointDeploymentConfig) (*driver.EndpointDeployment, error) {
	coll := endpointCollection(cfg.EndpointKind)
	if !m.mlEndpoints.Has(key(cfg.ResourceGroup, cfg.Workspace, coll, cfg.Endpoint)) {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", cfg.Endpoint)
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "deployment name is required")
	}

	d := &driver.EndpointDeployment{
		ID:   m.wsChildID(cfg.ResourceGroup, cfg.Workspace, coll+"/"+cfg.Endpoint+"/deployments/"+cfg.Name),
		Name: cfg.Name, Model: cfg.Model, InstanceType: cfg.InstanceType, InstanceCount: cfg.InstanceCount,
		ProvisioningState: driver.StateSucceeded, CreatedAt: m.now(),
	}
	m.mlDeploys.Set(key(cfg.ResourceGroup, cfg.Workspace, coll, cfg.Endpoint, "deployments", cfg.Name), d)

	out := *d

	return &out, nil
}

func (m *Mock) GetEndpointDeployment(
	_ context.Context, resourceGroup, workspace, kind, endpoint, name string,
) (*driver.EndpointDeployment, error) {
	d, ok := m.mlDeploys.Get(key(resourceGroup, workspace, endpointCollection(kind), endpoint, "deployments", name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "deployment %q not found", name)
	}

	out := *d

	return &out, nil
}

func (m *Mock) DeleteEndpointDeployment(_ context.Context, resourceGroup, workspace, kind, endpoint, name string) error {
	if !m.mlDeploys.Delete(key(resourceGroup, workspace, endpointCollection(kind), endpoint, "deployments", name)) {
		return errors.Newf(errors.NotFound, "deployment %q not found", name)
	}

	return nil
}

func (m *Mock) ListEndpointDeployments(
	_ context.Context, resourceGroup, workspace, kind, endpoint string,
) ([]driver.EndpointDeployment, error) {
	prefix := key(resourceGroup, workspace, endpointCollection(kind), endpoint, "deployments") + "/"
	out := make([]driver.EndpointDeployment, 0)

	for k, d := range m.mlDeploys.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *d)
		}
	}

	return out, nil
}

// --- Jobs ---

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateJob(_ context.Context, cfg driver.JobConfig) (*driver.Job, error) {
	if err := m.requireWorkspace(cfg.ResourceGroup, cfg.Workspace); err != nil {
		return nil, err
	}

	name := cfg.Name
	if name == "" {
		name = m.nextID("job")
	}

	jt := cfg.JobType
	if jt == "" {
		jt = "Command"
	}

	j := &driver.Job{
		ID: m.wsChildID(cfg.ResourceGroup, cfg.Workspace, "jobs/"+name), Name: name,
		JobType: jt, DisplayName: cfg.DisplayName, Status: "Completed", CreatedAt: m.now(),
	}
	m.jobs.Set(key(cfg.ResourceGroup, cfg.Workspace, "jobs", name), j)

	out := *j

	return &out, nil
}

func (m *Mock) GetJob(_ context.Context, resourceGroup, workspace, name string) (*driver.Job, error) {
	j, ok := m.jobs.Get(key(resourceGroup, workspace, "jobs", name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "job %q not found", name)
	}

	out := *j

	return &out, nil
}

func (m *Mock) ListJobs(_ context.Context, resourceGroup, workspace string) ([]driver.Job, error) {
	prefix := key(resourceGroup, workspace, "jobs") + "/"
	out := make([]driver.Job, 0)

	for k, j := range m.jobs.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *j)
		}
	}

	return out, nil
}

func (m *Mock) CancelJob(_ context.Context, resourceGroup, workspace, name string) error {
	k := key(resourceGroup, workspace, "jobs", name)

	j, ok := m.jobs.Get(k)
	if !ok {
		return errors.Newf(errors.NotFound, "job %q not found", name)
	}

	updated := *j
	updated.Status = "Canceled"
	m.jobs.Set(k, &updated)

	return nil
}

// --- Versioned assets ---

func cloneAsset(a *driver.Asset) *driver.Asset {
	out := *a
	out.Properties = copyMap(a.Properties)

	return &out
}

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateAsset(_ context.Context, cfg driver.AssetConfig) (*driver.Asset, error) {
	if err := m.requireWorkspace(cfg.ResourceGroup, cfg.Workspace); err != nil {
		return nil, err
	}

	switch {
	case cfg.AssetType == "":
		return nil, errors.New(errors.InvalidArgument, "asset type is required")
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "asset name is required")
	}

	version := cfg.Version
	if version == "" {
		version = "1"
	}

	a := &driver.Asset{
		ID:   m.wsChildID(cfg.ResourceGroup, cfg.Workspace, cfg.AssetType+"/"+cfg.Name+"/versions/"+version),
		Name: cfg.Name, Version: version, AssetType: cfg.AssetType, Description: cfg.Description,
		Path: cfg.Path, Properties: copyMap(cfg.Properties), CreatedAt: m.now(),
	}
	m.assets.Set(key(cfg.ResourceGroup, cfg.Workspace, cfg.AssetType, cfg.Name, version), a)

	return cloneAsset(a), nil
}

func (m *Mock) GetAsset(_ context.Context, resourceGroup, workspace, assetType, name, version string) (*driver.Asset, error) {
	a, ok := m.assets.Get(key(resourceGroup, workspace, assetType, name, version))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "%s %q version %q not found", assetType, name, version)
	}

	return cloneAsset(a), nil
}

func (m *Mock) DeleteAsset(_ context.Context, resourceGroup, workspace, assetType, name, version string) error {
	if !m.assets.Delete(key(resourceGroup, workspace, assetType, name, version)) {
		return errors.Newf(errors.NotFound, "%s %q version %q not found", assetType, name, version)
	}

	return nil
}

func (m *Mock) ListAssetVersions(_ context.Context, resourceGroup, workspace, assetType, name string) ([]driver.Asset, error) {
	prefix := key(resourceGroup, workspace, assetType, name) + "/"
	out := make([]driver.Asset, 0)

	for k, a := range m.assets.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *cloneAsset(a))
		}
	}

	return out, nil
}

// ListAssetContainers returns the latest version of each named asset container.
func (m *Mock) ListAssetContainers(_ context.Context, resourceGroup, workspace, assetType string) ([]driver.Asset, error) {
	prefix := key(resourceGroup, workspace, assetType) + "/"
	latest := make(map[string]*driver.Asset)

	for k, a := range m.assets.All() {
		if strings.HasPrefix(k, prefix) {
			if cur, ok := latest[a.Name]; !ok || a.Version > cur.Version {
				latest[a.Name] = a
			}
		}
	}

	out := make([]driver.Asset, 0, len(latest))
	for _, a := range latest {
		out = append(out, *cloneAsset(a))
	}

	return out, nil
}

// --- Datastores ---

//nolint:gocritic,dupl // cfg matches driver sig; uniform child CRUD recurs across collections.
func (m *Mock) CreateDatastore(_ context.Context, cfg driver.DatastoreConfig) (*driver.Datastore, error) {
	if err := m.requireWorkspace(cfg.ResourceGroup, cfg.Workspace); err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "datastore name is required")
	}

	d := &driver.Datastore{
		ID: m.wsChildID(cfg.ResourceGroup, cfg.Workspace, "datastores/"+cfg.Name), Name: cfg.Name,
		StoreType: cfg.StoreType, AccountName: cfg.AccountName, Container: cfg.Container, CreatedAt: m.now(),
	}
	m.datastores.Set(key(cfg.ResourceGroup, cfg.Workspace, "datastores", cfg.Name), d)

	out := *d

	return &out, nil
}

func (m *Mock) GetDatastore(_ context.Context, resourceGroup, workspace, name string) (*driver.Datastore, error) {
	d, ok := m.datastores.Get(key(resourceGroup, workspace, "datastores", name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "datastore %q not found", name)
	}

	out := *d

	return &out, nil
}

func (m *Mock) DeleteDatastore(_ context.Context, resourceGroup, workspace, name string) error {
	if !m.datastores.Delete(key(resourceGroup, workspace, "datastores", name)) {
		return errors.Newf(errors.NotFound, "datastore %q not found", name)
	}

	return nil
}

func (m *Mock) ListDatastores(_ context.Context, resourceGroup, workspace string) ([]driver.Datastore, error) {
	prefix := key(resourceGroup, workspace, "datastores") + "/"
	out := make([]driver.Datastore, 0)

	for k, d := range m.datastores.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *d)
		}
	}

	return out, nil
}

// --- Connections ---

//nolint:gocritic,dupl // cfg matches driver sig; uniform child CRUD recurs across collections.
func (m *Mock) CreateConnection(_ context.Context, cfg driver.ConnectionConfig) (*driver.Connection, error) {
	if err := m.requireWorkspace(cfg.ResourceGroup, cfg.Workspace); err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "connection name is required")
	}

	c := &driver.Connection{
		ID: m.wsChildID(cfg.ResourceGroup, cfg.Workspace, "connections/"+cfg.Name), Name: cfg.Name,
		Category: cfg.Category, Target: cfg.Target, AuthType: cfg.AuthType, CreatedAt: m.now(),
	}
	m.connections.Set(key(cfg.ResourceGroup, cfg.Workspace, "connections", cfg.Name), c)

	out := *c

	return &out, nil
}

func (m *Mock) GetConnection(_ context.Context, resourceGroup, workspace, name string) (*driver.Connection, error) {
	c, ok := m.connections.Get(key(resourceGroup, workspace, "connections", name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "connection %q not found", name)
	}

	out := *c

	return &out, nil
}

func (m *Mock) DeleteConnection(_ context.Context, resourceGroup, workspace, name string) error {
	if !m.connections.Delete(key(resourceGroup, workspace, "connections", name)) {
		return errors.Newf(errors.NotFound, "connection %q not found", name)
	}

	return nil
}

func (m *Mock) ListConnections(_ context.Context, resourceGroup, workspace string) ([]driver.Connection, error) {
	prefix := key(resourceGroup, workspace, "connections") + "/"
	out := make([]driver.Connection, 0)

	for k, c := range m.connections.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *c)
		}
	}

	return out, nil
}

// --- Schedules ---

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateMLSchedule(_ context.Context, cfg driver.MLScheduleConfig) (*driver.MLSchedule, error) {
	if err := m.requireWorkspace(cfg.ResourceGroup, cfg.Workspace); err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "schedule name is required")
	}

	s := &driver.MLSchedule{
		ID: m.wsChildID(cfg.ResourceGroup, cfg.Workspace, "schedules/"+cfg.Name), Name: cfg.Name,
		Cron: cfg.Cron, DisplayName: cfg.DisplayName, IsEnabled: true, CreatedAt: m.now(),
	}
	m.mlSchedules.Set(key(cfg.ResourceGroup, cfg.Workspace, "schedules", cfg.Name), s)

	out := *s

	return &out, nil
}

func (m *Mock) GetMLSchedule(_ context.Context, resourceGroup, workspace, name string) (*driver.MLSchedule, error) {
	s, ok := m.mlSchedules.Get(key(resourceGroup, workspace, "schedules", name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "schedule %q not found", name)
	}

	out := *s

	return &out, nil
}

func (m *Mock) DeleteMLSchedule(_ context.Context, resourceGroup, workspace, name string) error {
	if !m.mlSchedules.Delete(key(resourceGroup, workspace, "schedules", name)) {
		return errors.Newf(errors.NotFound, "schedule %q not found", name)
	}

	return nil
}

func (m *Mock) ListMLSchedules(_ context.Context, resourceGroup, workspace string) ([]driver.MLSchedule, error) {
	prefix := key(resourceGroup, workspace, "schedules") + "/"
	out := make([]driver.MLSchedule, 0)

	for k, s := range m.mlSchedules.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *s)
		}
	}

	return out, nil
}

// --- Registries ---

func cloneRegistry(r *driver.Registry) *driver.Registry {
	out := *r
	out.Tags = copyMap(r.Tags)

	return &out
}

func (m *Mock) CreateRegistry(_ context.Context, cfg driver.RegistryConfig) (*driver.Registry, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "registry name is required")
	case cfg.ResourceGroup == "":
		return nil, errors.New(errors.InvalidArgument, "resource group is required")
	case cfg.Location == "":
		return nil, errors.New(errors.InvalidArgument, "location is required")
	}

	r := &driver.Registry{
		ID: m.mlID(cfg.ResourceGroup, "registries", cfg.Name), Name: cfg.Name, Location: cfg.Location,
		Description: cfg.Description, ProvisioningState: driver.StateSucceeded,
		Tags: copyMap(cfg.Tags), CreatedAt: m.now(),
	}
	m.registries.Set(key(cfg.ResourceGroup, cfg.Name), r)

	return cloneRegistry(r), nil
}

func (m *Mock) GetRegistry(_ context.Context, resourceGroup, name string) (*driver.Registry, error) {
	r, ok := m.registries.Get(key(resourceGroup, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "registry %q not found", name)
	}

	return cloneRegistry(r), nil
}

func (m *Mock) DeleteRegistry(_ context.Context, resourceGroup, name string) error {
	if !m.registries.Delete(key(resourceGroup, name)) {
		return errors.Newf(errors.NotFound, "registry %q not found", name)
	}

	return nil
}

func (m *Mock) ListRegistries(_ context.Context, resourceGroup string) ([]driver.Registry, error) {
	out := make([]driver.Registry, 0)

	for _, r := range m.registries.All() {
		if strings.HasPrefix(r.ID, "/subscriptions/"+m.opts.AccountID+"/resourceGroups/"+resourceGroup+"/") {
			out = append(out, *cloneRegistry(r))
		}
	}

	return out, nil
}
