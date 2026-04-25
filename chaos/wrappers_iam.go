package chaos

import (
	"context"

	iamdriver "github.com/stackshy/cloudemu/iam/driver"
)

// chaosIAM wraps an IAM driver. Hot-path: user/role/policy CRUD plus
// CheckPermission. Groups, access keys, and instance profiles delegate through.
type chaosIAM struct {
	iamdriver.IAM
	engine *Engine
}

// WrapIAM returns an IAM driver that consults engine on principal/policy
// management and permission checks.
func WrapIAM(inner iamdriver.IAM, engine *Engine) iamdriver.IAM {
	return &chaosIAM{IAM: inner, engine: engine}
}

func (c *chaosIAM) CreateUser(ctx context.Context, cfg iamdriver.UserConfig) (*iamdriver.UserInfo, error) {
	if err := applyChaos(ctx, c.engine, "iam", "CreateUser"); err != nil {
		return nil, err
	}

	return c.IAM.CreateUser(ctx, cfg)
}

func (c *chaosIAM) DeleteUser(ctx context.Context, name string) error {
	if err := applyChaos(ctx, c.engine, "iam", "DeleteUser"); err != nil {
		return err
	}

	return c.IAM.DeleteUser(ctx, name)
}

func (c *chaosIAM) GetUser(ctx context.Context, name string) (*iamdriver.UserInfo, error) {
	if err := applyChaos(ctx, c.engine, "iam", "GetUser"); err != nil {
		return nil, err
	}

	return c.IAM.GetUser(ctx, name)
}

func (c *chaosIAM) ListUsers(ctx context.Context) ([]iamdriver.UserInfo, error) {
	if err := applyChaos(ctx, c.engine, "iam", "ListUsers"); err != nil {
		return nil, err
	}

	return c.IAM.ListUsers(ctx)
}

func (c *chaosIAM) CreateRole(ctx context.Context, cfg iamdriver.RoleConfig) (*iamdriver.RoleInfo, error) {
	if err := applyChaos(ctx, c.engine, "iam", "CreateRole"); err != nil {
		return nil, err
	}

	return c.IAM.CreateRole(ctx, cfg)
}

func (c *chaosIAM) DeleteRole(ctx context.Context, name string) error {
	if err := applyChaos(ctx, c.engine, "iam", "DeleteRole"); err != nil {
		return err
	}

	return c.IAM.DeleteRole(ctx, name)
}

func (c *chaosIAM) GetRole(ctx context.Context, name string) (*iamdriver.RoleInfo, error) {
	if err := applyChaos(ctx, c.engine, "iam", "GetRole"); err != nil {
		return nil, err
	}

	return c.IAM.GetRole(ctx, name)
}

func (c *chaosIAM) ListRoles(ctx context.Context) ([]iamdriver.RoleInfo, error) {
	if err := applyChaos(ctx, c.engine, "iam", "ListRoles"); err != nil {
		return nil, err
	}

	return c.IAM.ListRoles(ctx)
}

func (c *chaosIAM) CreatePolicy(ctx context.Context, cfg iamdriver.PolicyConfig) (*iamdriver.PolicyInfo, error) {
	if err := applyChaos(ctx, c.engine, "iam", "CreatePolicy"); err != nil {
		return nil, err
	}

	return c.IAM.CreatePolicy(ctx, cfg)
}

func (c *chaosIAM) DeletePolicy(ctx context.Context, arn string) error {
	if err := applyChaos(ctx, c.engine, "iam", "DeletePolicy"); err != nil {
		return err
	}

	return c.IAM.DeletePolicy(ctx, arn)
}

func (c *chaosIAM) GetPolicy(ctx context.Context, arn string) (*iamdriver.PolicyInfo, error) {
	if err := applyChaos(ctx, c.engine, "iam", "GetPolicy"); err != nil {
		return nil, err
	}

	return c.IAM.GetPolicy(ctx, arn)
}

func (c *chaosIAM) ListPolicies(ctx context.Context) ([]iamdriver.PolicyInfo, error) {
	if err := applyChaos(ctx, c.engine, "iam", "ListPolicies"); err != nil {
		return nil, err
	}

	return c.IAM.ListPolicies(ctx)
}

func (c *chaosIAM) CheckPermission(ctx context.Context, principal, action, resource string) (bool, error) {
	if err := applyChaos(ctx, c.engine, "iam", "CheckPermission"); err != nil {
		return false, err
	}

	return c.IAM.CheckPermission(ctx, principal, action, resource)
}
