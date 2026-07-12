package azureai

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateAccount(ctx context.Context, cfg driver.AccountConfig) (*driver.Account, error) {
	return cast[*driver.Account](a.do(ctx, "CreateAccount", cfg, func() (any, error) { return a.drv.CreateAccount(ctx, cfg) }))
}

func (a *AzureAI) GetAccount(ctx context.Context, rg, name string) (*driver.Account, error) {
	return cast[*driver.Account](a.do(ctx, "GetAccount", name, func() (any, error) { return a.drv.GetAccount(ctx, rg, name) }))
}

func (a *AzureAI) DeleteAccount(ctx context.Context, rg, name string) error {
	return a.act(ctx, "DeleteAccount", name, func() error { return a.drv.DeleteAccount(ctx, rg, name) })
}

func (a *AzureAI) UpdateAccountTags(ctx context.Context, rg, name string, tags map[string]string) (*driver.Account, error) {
	return cast[*driver.Account](a.do(ctx, "UpdateAccountTags", name, func() (any, error) {
		return a.drv.UpdateAccountTags(ctx, rg, name, tags)
	}))
}

func (a *AzureAI) ListAccountsByResourceGroup(ctx context.Context, rg string) ([]driver.Account, error) {
	return cast[[]driver.Account](a.do(ctx, "ListAccountsByResourceGroup", rg, func() (any, error) {
		return a.drv.ListAccountsByResourceGroup(ctx, rg)
	}))
}

func (a *AzureAI) ListAccounts(ctx context.Context) ([]driver.Account, error) {
	return cast[[]driver.Account](a.do(ctx, "ListAccounts", nil, func() (any, error) { return a.drv.ListAccounts(ctx) }))
}

func (a *AzureAI) ListAccountKeys(ctx context.Context, rg, name string) (*driver.AccountKeys, error) {
	return cast[*driver.AccountKeys](a.do(ctx, "ListAccountKeys", name, func() (any, error) {
		return a.drv.ListAccountKeys(ctx, rg, name)
	}))
}

func (a *AzureAI) RegenerateAccountKey(ctx context.Context, rg, name, keyName string) (*driver.AccountKeys, error) {
	return cast[*driver.AccountKeys](a.do(ctx, "RegenerateAccountKey", name, func() (any, error) {
		return a.drv.RegenerateAccountKey(ctx, rg, name, keyName)
	}))
}

func (a *AzureAI) ListAccountUsages(ctx context.Context, rg, name string) ([]driver.Usage, error) {
	return cast[[]driver.Usage](a.do(ctx, "ListAccountUsages", name, func() (any, error) {
		return a.drv.ListAccountUsages(ctx, rg, name)
	}))
}

func (a *AzureAI) ListAccountModels(ctx context.Context, rg, name string) ([]driver.AccountModel, error) {
	return cast[[]driver.AccountModel](a.do(ctx, "ListAccountModels", name, func() (any, error) {
		return a.drv.ListAccountModels(ctx, rg, name)
	}))
}

func (a *AzureAI) ListAccountSkus(ctx context.Context, rg, name string) ([]driver.AccountSKU, error) {
	return cast[[]driver.AccountSKU](a.do(ctx, "ListAccountSkus", name, func() (any, error) {
		return a.drv.ListAccountSkus(ctx, rg, name)
	}))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateDeployment(ctx context.Context, cfg driver.DeploymentConfig) (*driver.Deployment, error) {
	return cast[*driver.Deployment](a.do(ctx, "CreateDeployment", cfg, func() (any, error) { return a.drv.CreateDeployment(ctx, cfg) }))
}

func (a *AzureAI) GetDeployment(ctx context.Context, rg, account, name string) (*driver.Deployment, error) {
	return cast[*driver.Deployment](a.do(ctx, "GetDeployment", name, func() (any, error) {
		return a.drv.GetDeployment(ctx, rg, account, name)
	}))
}

func (a *AzureAI) DeleteDeployment(ctx context.Context, rg, account, name string) error {
	return a.act(ctx, "DeleteDeployment", name, func() error { return a.drv.DeleteDeployment(ctx, rg, account, name) })
}

func (a *AzureAI) ListDeployments(ctx context.Context, rg, account string) ([]driver.Deployment, error) {
	return cast[[]driver.Deployment](a.do(ctx, "ListDeployments", account, func() (any, error) {
		return a.drv.ListDeployments(ctx, rg, account)
	}))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateProject(ctx context.Context, cfg driver.ProjectConfig) (*driver.Project, error) {
	return cast[*driver.Project](a.do(ctx, "CreateProject", cfg, func() (any, error) { return a.drv.CreateProject(ctx, cfg) }))
}

func (a *AzureAI) GetProject(ctx context.Context, rg, account, name string) (*driver.Project, error) {
	return cast[*driver.Project](a.do(ctx, "GetProject", name, func() (any, error) {
		return a.drv.GetProject(ctx, rg, account, name)
	}))
}

func (a *AzureAI) DeleteProject(ctx context.Context, rg, account, name string) error {
	return a.act(ctx, "DeleteProject", name, func() error { return a.drv.DeleteProject(ctx, rg, account, name) })
}

func (a *AzureAI) ListProjects(ctx context.Context, rg, account string) ([]driver.Project, error) {
	return cast[[]driver.Project](a.do(ctx, "ListProjects", account, func() (any, error) {
		return a.drv.ListProjects(ctx, rg, account)
	}))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateRaiPolicy(ctx context.Context, cfg driver.RaiPolicyConfig) (*driver.RaiPolicy, error) {
	return cast[*driver.RaiPolicy](a.do(ctx, "CreateRaiPolicy", cfg, func() (any, error) { return a.drv.CreateRaiPolicy(ctx, cfg) }))
}

func (a *AzureAI) GetRaiPolicy(ctx context.Context, rg, account, name string) (*driver.RaiPolicy, error) {
	return cast[*driver.RaiPolicy](a.do(ctx, "GetRaiPolicy", name, func() (any, error) {
		return a.drv.GetRaiPolicy(ctx, rg, account, name)
	}))
}

func (a *AzureAI) DeleteRaiPolicy(ctx context.Context, rg, account, name string) error {
	return a.act(ctx, "DeleteRaiPolicy", name, func() error { return a.drv.DeleteRaiPolicy(ctx, rg, account, name) })
}

func (a *AzureAI) ListRaiPolicies(ctx context.Context, rg, account string) ([]driver.RaiPolicy, error) {
	return cast[[]driver.RaiPolicy](a.do(ctx, "ListRaiPolicies", account, func() (any, error) {
		return a.drv.ListRaiPolicies(ctx, rg, account)
	}))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateCommitmentPlan(ctx context.Context, cfg driver.CommitmentPlanConfig) (*driver.CommitmentPlan, error) {
	return cast[*driver.CommitmentPlan](a.do(ctx, "CreateCommitmentPlan", cfg, func() (any, error) {
		return a.drv.CreateCommitmentPlan(ctx, cfg)
	}))
}

func (a *AzureAI) GetCommitmentPlan(ctx context.Context, rg, account, name string) (*driver.CommitmentPlan, error) {
	return cast[*driver.CommitmentPlan](a.do(ctx, "GetCommitmentPlan", name, func() (any, error) {
		return a.drv.GetCommitmentPlan(ctx, rg, account, name)
	}))
}

func (a *AzureAI) DeleteCommitmentPlan(ctx context.Context, rg, account, name string) error {
	return a.act(ctx, "DeleteCommitmentPlan", name, func() error { return a.drv.DeleteCommitmentPlan(ctx, rg, account, name) })
}

func (a *AzureAI) ListCommitmentPlans(ctx context.Context, rg, account string) ([]driver.CommitmentPlan, error) {
	return cast[[]driver.CommitmentPlan](a.do(ctx, "ListCommitmentPlans", account, func() (any, error) {
		return a.drv.ListCommitmentPlans(ctx, rg, account)
	}))
}

func (a *AzureAI) PutPrivateEndpointConnection(
	ctx context.Context, rg, account, name, status string,
) (*driver.PrivateEndpointConnection, error) {
	return cast[*driver.PrivateEndpointConnection](a.do(ctx, "PutPrivateEndpointConnection", name, func() (any, error) {
		return a.drv.PutPrivateEndpointConnection(ctx, rg, account, name, status)
	}))
}

func (a *AzureAI) GetPrivateEndpointConnection(
	ctx context.Context, rg, account, name string,
) (*driver.PrivateEndpointConnection, error) {
	return cast[*driver.PrivateEndpointConnection](a.do(ctx, "GetPrivateEndpointConnection", name, func() (any, error) {
		return a.drv.GetPrivateEndpointConnection(ctx, rg, account, name)
	}))
}

func (a *AzureAI) DeletePrivateEndpointConnection(ctx context.Context, rg, account, name string) error {
	return a.act(ctx, "DeletePrivateEndpointConnection", name, func() error {
		return a.drv.DeletePrivateEndpointConnection(ctx, rg, account, name)
	})
}

func (a *AzureAI) ListPrivateEndpointConnections(
	ctx context.Context, rg, account string,
) ([]driver.PrivateEndpointConnection, error) {
	return cast[[]driver.PrivateEndpointConnection](a.do(ctx, "ListPrivateEndpointConnections", account, func() (any, error) {
		return a.drv.ListPrivateEndpointConnections(ctx, rg, account)
	}))
}
