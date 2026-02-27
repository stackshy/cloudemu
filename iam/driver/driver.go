// Package driver defines the interface for IAM service implementations.
package driver

import "context"

// UserConfig describes a user to create.
type UserConfig struct {
	Name string
	Path string
	Tags map[string]string
}

// UserInfo describes an IAM user.
type UserInfo struct {
	Name      string
	ID        string
	ARN       string
	Path      string
	Tags      map[string]string
	CreatedAt string
}

// RoleConfig describes a role to create.
type RoleConfig struct {
	Name                 string
	Path                 string
	AssumeRolePolicyDoc  string
	Tags                 map[string]string
}

// RoleInfo describes an IAM role.
type RoleInfo struct {
	Name                 string
	ID                   string
	ARN                  string
	Path                 string
	AssumeRolePolicyDoc  string
	Tags                 map[string]string
}

// PolicyConfig describes a policy to create.
type PolicyConfig struct {
	Name           string
	Path           string
	PolicyDocument string
	Description    string
}

// PolicyInfo describes an IAM policy.
type PolicyInfo struct {
	Name           string
	ID             string
	ARN            string
	Path           string
	PolicyDocument string
	Description    string
}

// IAM is the interface that IAM provider implementations must satisfy.
type IAM interface {
	CreateUser(ctx context.Context, config UserConfig) (*UserInfo, error)
	DeleteUser(ctx context.Context, name string) error
	GetUser(ctx context.Context, name string) (*UserInfo, error)
	ListUsers(ctx context.Context) ([]UserInfo, error)

	CreateRole(ctx context.Context, config RoleConfig) (*RoleInfo, error)
	DeleteRole(ctx context.Context, name string) error
	GetRole(ctx context.Context, name string) (*RoleInfo, error)
	ListRoles(ctx context.Context) ([]RoleInfo, error)

	CreatePolicy(ctx context.Context, config PolicyConfig) (*PolicyInfo, error)
	DeletePolicy(ctx context.Context, arn string) error
	GetPolicy(ctx context.Context, arn string) (*PolicyInfo, error)
	ListPolicies(ctx context.Context) ([]PolicyInfo, error)

	AttachUserPolicy(ctx context.Context, userName, policyARN string) error
	DetachUserPolicy(ctx context.Context, userName, policyARN string) error
	AttachRolePolicy(ctx context.Context, roleName, policyARN string) error
	DetachRolePolicy(ctx context.Context, roleName, policyARN string) error

	ListAttachedUserPolicies(ctx context.Context, userName string) ([]string, error)
	ListAttachedRolePolicies(ctx context.Context, roleName string) ([]string, error)

	CheckPermission(ctx context.Context, principal, action, resource string) (bool, error)
}
