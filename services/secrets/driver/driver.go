// Package driver defines the interface for secret management service implementations.
package driver

import "context"

// SecretConfig describes a secret to create.
type SecretConfig struct {
	Name        string
	Description string
	Tags        map[string]string
}

// SecretInfo describes a secret.
type SecretInfo struct {
	ID          string
	Name        string
	ResourceID  string
	Description string
	CreatedAt   string
	UpdatedAt   string
	Tags        map[string]string
}

// SecretVersion represents a specific version of a secret value.
type SecretVersion struct {
	VersionID string
	Value     []byte
	CreatedAt string
	Current   bool
	// Binary reports that the value was stored via SecretBinary (rather than
	// SecretString), so a reader returns it in the SecretBinary field. AWS Secrets
	// Manager keeps these mutually exclusive; other providers leave it false.
	Binary bool
}

// Secrets is the interface that secret management provider implementations must satisfy.
type Secrets interface {
	CreateSecret(ctx context.Context, config SecretConfig, value []byte) (*SecretInfo, error)
	DeleteSecret(ctx context.Context, name string) error
	GetSecret(ctx context.Context, name string) (*SecretInfo, error)
	ListSecrets(ctx context.Context) ([]SecretInfo, error)

	PutSecretValue(ctx context.Context, name string, value []byte) (*SecretVersion, error)
	GetSecretValue(ctx context.Context, name string, versionID string) (*SecretVersion, error)
	ListSecretVersions(ctx context.Context, name string) ([]SecretVersion, error)
}
