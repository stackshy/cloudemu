// Package driver defines the interface for secret manager service implementations.
package driver

import "context"

// SecretConfig describes a secret to create.
type SecretConfig struct {
	Name        string
	Value       string
	Description string
	Tags        map[string]string
}

// SecretInfo describes a stored secret.
type SecretInfo struct {
	Name        string
	ARN         string
	Description string
	Version     int
	Tags        map[string]string
	CreatedAt   string
	UpdatedAt   string
	DeletedAt   string // non-empty if scheduled for deletion
}

// SecretValue holds the secret payload and version metadata.
type SecretValue struct {
	Name    string
	ARN     string
	Value   string
	Version int
}

// VersionInfo describes a single version of a secret.
type VersionInfo struct {
	Version   int
	CreatedAt string
}

// RotateCallback is invoked during secret rotation with the current value.
// It returns the new rotated value.
type RotateCallback func(currentValue string) (string, error)

// SecretManager is the interface that secret manager provider implementations must satisfy.
type SecretManager interface {
	CreateSecret(ctx context.Context, cfg SecretConfig) (*SecretInfo, error)
	GetSecret(ctx context.Context, name string) (*SecretValue, error)
	UpdateSecret(ctx context.Context, name, value string) (*SecretInfo, error)
	DeleteSecret(ctx context.Context, name string) error
	ListSecrets(ctx context.Context) ([]SecretInfo, error)
	GetSecretVersion(ctx context.Context, name string, version int) (*SecretValue, error)
	RotateSecret(ctx context.Context, name string, callback RotateCallback) (*SecretInfo, error)
	DescribeSecret(ctx context.Context, name string) (*SecretInfo, error)
	TagResource(ctx context.Context, name string, tags map[string]string) error
	UntagResource(ctx context.Context, name string, tagKeys []string) error
	RestoreSecret(ctx context.Context, name string) (*SecretInfo, error)
	ListSecretVersions(ctx context.Context, name string) ([]VersionInfo, error)
}
