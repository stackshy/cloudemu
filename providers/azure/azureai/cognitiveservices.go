package azureai

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/azureai/driver"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
)

// subResourceID builds the ARM ID for a child resource of an account.
func (m *Mock) subResourceID(resourceGroup, account, childType, name string) string {
	return idgen.AzureID(m.opts.AccountID, resourceGroup, csProvider, "accounts", account) +
		"/" + childType + "/" + name
}

// requireAccount returns NotFound when the parent account is absent.
func (m *Mock) requireAccount(resourceGroup, account string) error {
	if !m.accounts.Has(key(resourceGroup, account)) {
		return errors.Newf(errors.NotFound, "account %q not found", account)
	}

	return nil
}

// childKey builds a store key scoped under an account: rg/account/type/name.
func childKey(resourceGroup, account, childType, name string) string {
	return key(resourceGroup, account, childType, name)
}

// childPrefix builds the store-key prefix for listing children of an account.
func childPrefix(resourceGroup, account, childType string) string {
	return key(resourceGroup, account, childType) + "/"
}

// --- Deployments ---

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateDeployment(_ context.Context, cfg driver.DeploymentConfig) (*driver.Deployment, error) {
	if err := m.requireAccount(cfg.ResourceGroup, cfg.Account); err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "deployment name is required")
	}

	skuName := cfg.SKUName
	if skuName == "" {
		skuName = "Standard"
	}

	d := &driver.Deployment{
		ID:                m.subResourceID(cfg.ResourceGroup, cfg.Account, "deployments", cfg.Name),
		Name:              cfg.Name,
		ModelName:         cfg.ModelName,
		ModelVersion:      cfg.ModelVersion,
		ModelFormat:       cfg.ModelFormat,
		SKUName:           skuName,
		SKUCapacity:       cfg.SKUCapacity,
		ProvisioningState: driver.StateSucceeded,
		CreatedAt:         m.now(),
	}
	m.deployments.Set(childKey(cfg.ResourceGroup, cfg.Account, "deployments", cfg.Name), d)
	m.emitMetric("deployment/count", 1, map[string]string{"model": cfg.ModelName})

	out := *d

	return &out, nil
}

func (m *Mock) GetDeployment(_ context.Context, resourceGroup, account, name string) (*driver.Deployment, error) {
	d, ok := m.deployments.Get(childKey(resourceGroup, account, "deployments", name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "deployment %q not found", name)
	}

	out := *d

	return &out, nil
}

func (m *Mock) DeleteDeployment(_ context.Context, resourceGroup, account, name string) error {
	if !m.deployments.Delete(childKey(resourceGroup, account, "deployments", name)) {
		return errors.Newf(errors.NotFound, "deployment %q not found", name)
	}

	return nil
}

func (m *Mock) ListDeployments(_ context.Context, resourceGroup, account string) ([]driver.Deployment, error) {
	prefix := childPrefix(resourceGroup, account, "deployments")
	out := make([]driver.Deployment, 0)

	for k, d := range m.deployments.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *d)
		}
	}

	return out, nil
}

// --- Projects (AI Foundry) ---

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateProject(_ context.Context, cfg driver.ProjectConfig) (*driver.Project, error) {
	if err := m.requireAccount(cfg.ResourceGroup, cfg.Account); err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "project name is required")
	}

	p := &driver.Project{
		ID:                m.subResourceID(cfg.ResourceGroup, cfg.Account, "projects", cfg.Name),
		Name:              cfg.Name,
		Location:          cfg.Location,
		DisplayName:       cfg.DisplayName,
		Description:       cfg.Description,
		ProvisioningState: driver.StateSucceeded,
		Tags:              copyMap(cfg.Tags),
		CreatedAt:         m.now(),
	}
	m.projects.Set(childKey(cfg.ResourceGroup, cfg.Account, "projects", cfg.Name), p)

	return cloneProject(p), nil
}

func cloneProject(p *driver.Project) *driver.Project {
	out := *p
	out.Tags = copyMap(p.Tags)

	return &out
}

func (m *Mock) GetProject(_ context.Context, resourceGroup, account, name string) (*driver.Project, error) {
	p, ok := m.projects.Get(childKey(resourceGroup, account, "projects", name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "project %q not found", name)
	}

	return cloneProject(p), nil
}

func (m *Mock) DeleteProject(_ context.Context, resourceGroup, account, name string) error {
	if !m.projects.Delete(childKey(resourceGroup, account, "projects", name)) {
		return errors.Newf(errors.NotFound, "project %q not found", name)
	}

	return nil
}

func (m *Mock) ListProjects(_ context.Context, resourceGroup, account string) ([]driver.Project, error) {
	prefix := childPrefix(resourceGroup, account, "projects")
	out := make([]driver.Project, 0)

	for k, p := range m.projects.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *cloneProject(p))
		}
	}

	return out, nil
}

// --- RAI policies ---

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateRaiPolicy(_ context.Context, cfg driver.RaiPolicyConfig) (*driver.RaiPolicy, error) {
	if err := m.requireAccount(cfg.ResourceGroup, cfg.Account); err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "RAI policy name is required")
	}

	mode := cfg.Mode
	if mode == "" {
		mode = kindDefault
	}

	p := &driver.RaiPolicy{
		ID:         m.subResourceID(cfg.ResourceGroup, cfg.Account, "raiPolicies", cfg.Name),
		Name:       cfg.Name,
		Mode:       mode,
		BasePolicy: cfg.BasePolicy,
		CreatedAt:  m.now(),
	}
	m.raiPolicies.Set(childKey(cfg.ResourceGroup, cfg.Account, "raiPolicies", cfg.Name), p)

	out := *p

	return &out, nil
}

func (m *Mock) GetRaiPolicy(_ context.Context, resourceGroup, account, name string) (*driver.RaiPolicy, error) {
	p, ok := m.raiPolicies.Get(childKey(resourceGroup, account, "raiPolicies", name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "RAI policy %q not found", name)
	}

	out := *p

	return &out, nil
}

func (m *Mock) DeleteRaiPolicy(_ context.Context, resourceGroup, account, name string) error {
	if !m.raiPolicies.Delete(childKey(resourceGroup, account, "raiPolicies", name)) {
		return errors.Newf(errors.NotFound, "RAI policy %q not found", name)
	}

	return nil
}

func (m *Mock) ListRaiPolicies(_ context.Context, resourceGroup, account string) ([]driver.RaiPolicy, error) {
	prefix := childPrefix(resourceGroup, account, "raiPolicies")
	out := make([]driver.RaiPolicy, 0)

	for k, p := range m.raiPolicies.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *p)
		}
	}

	return out, nil
}

// --- Commitment plans ---

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateCommitmentPlan(_ context.Context, cfg driver.CommitmentPlanConfig) (*driver.CommitmentPlan, error) {
	if err := m.requireAccount(cfg.ResourceGroup, cfg.Account); err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "commitment plan name is required")
	}

	p := &driver.CommitmentPlan{
		ID:                m.subResourceID(cfg.ResourceGroup, cfg.Account, "commitmentPlans", cfg.Name),
		Name:              cfg.Name,
		PlanType:          cfg.PlanType,
		Tier:              cfg.Tier,
		AutoRenew:         cfg.AutoRenew,
		ProvisioningState: driver.StateSucceeded,
		CreatedAt:         m.now(),
	}
	m.commitmentPlans.Set(childKey(cfg.ResourceGroup, cfg.Account, "commitmentPlans", cfg.Name), p)

	out := *p

	return &out, nil
}

func (m *Mock) GetCommitmentPlan(_ context.Context, resourceGroup, account, name string) (*driver.CommitmentPlan, error) {
	p, ok := m.commitmentPlans.Get(childKey(resourceGroup, account, "commitmentPlans", name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "commitment plan %q not found", name)
	}

	out := *p

	return &out, nil
}

func (m *Mock) DeleteCommitmentPlan(_ context.Context, resourceGroup, account, name string) error {
	if !m.commitmentPlans.Delete(childKey(resourceGroup, account, "commitmentPlans", name)) {
		return errors.Newf(errors.NotFound, "commitment plan %q not found", name)
	}

	return nil
}

func (m *Mock) ListCommitmentPlans(_ context.Context, resourceGroup, account string) ([]driver.CommitmentPlan, error) {
	prefix := childPrefix(resourceGroup, account, "commitmentPlans")
	out := make([]driver.CommitmentPlan, 0)

	for k, p := range m.commitmentPlans.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *p)
		}
	}

	return out, nil
}

// --- Private-endpoint connections ---

func (m *Mock) PutPrivateEndpointConnection(
	_ context.Context, resourceGroup, account, name, status string,
) (*driver.PrivateEndpointConnection, error) {
	if err := m.requireAccount(resourceGroup, account); err != nil {
		return nil, err
	}

	if status == "" {
		status = "Approved"
	}

	c := &driver.PrivateEndpointConnection{
		ID:                m.subResourceID(resourceGroup, account, "privateEndpointConnections", name),
		Name:              name,
		Status:            status,
		ProvisioningState: driver.StateSucceeded,
	}
	m.privateEndpoints.Set(childKey(resourceGroup, account, "privateEndpointConnections", name), c)

	out := *c

	return &out, nil
}

func (m *Mock) GetPrivateEndpointConnection(
	_ context.Context, resourceGroup, account, name string,
) (*driver.PrivateEndpointConnection, error) {
	c, ok := m.privateEndpoints.Get(childKey(resourceGroup, account, "privateEndpointConnections", name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "private endpoint connection %q not found", name)
	}

	out := *c

	return &out, nil
}

func (m *Mock) DeletePrivateEndpointConnection(_ context.Context, resourceGroup, account, name string) error {
	if !m.privateEndpoints.Delete(childKey(resourceGroup, account, "privateEndpointConnections", name)) {
		return errors.Newf(errors.NotFound, "private endpoint connection %q not found", name)
	}

	return nil
}

func (m *Mock) ListPrivateEndpointConnections(
	_ context.Context, resourceGroup, account string,
) ([]driver.PrivateEndpointConnection, error) {
	prefix := childPrefix(resourceGroup, account, "privateEndpointConnections")
	out := make([]driver.PrivateEndpointConnection, 0)

	for k, c := range m.privateEndpoints.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *c)
		}
	}

	return out, nil
}
