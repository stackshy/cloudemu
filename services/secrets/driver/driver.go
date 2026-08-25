// Package driver defines the interface for secret management service implementations.
package driver

import "context"

// SecretConfig describes a secret to create.
type SecretConfig struct {
	Name        string
	Description string
	Tags        map[string]string
	// KMSKeyID is the customer KMS key (ARN, key id, or alias) the secret is
	// encrypted with (AWS Secrets Manager). Empty means the default
	// aws/secretsmanager key. Ignored by Azure/GCP.
	KMSKeyID string
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
	// KMSKeyID is the customer KMS key the secret is encrypted with (AWS Secrets
	// Manager), echoed on DescribeSecret. Empty when the default
	// aws/secretsmanager key is used. Ignored by Azure/GCP.
	KMSKeyID string
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

// KVAttributes are Azure Key Vault per-version secret management attributes.
// Expires and NotBefore are Unix epoch seconds; 0 means unset.
type KVAttributes struct {
	Enabled   bool
	Expires   int64
	NotBefore int64
}

// KVSetParams is the Azure Key Vault SetSecret payload for a new version.
type KVSetParams struct {
	Value       []byte
	ContentType string
	Tags        map[string]string
	Attributes  KVAttributes
}

// KVPatch is the Azure Key Vault UpdateSecretProperties payload. Nil pointer
// fields are left unchanged; SetTags true replaces the tag set with Tags.
type KVPatch struct {
	ContentType *string
	Enabled     *bool
	Expires     *int64
	NotBefore   *int64
	Tags        map[string]string
	SetTags     bool
}

// KVSecret is one Azure Key Vault secret version with its attributes. Created,
// Updated, Expires and NotBefore are Unix epoch seconds (0 = unset). Value is
// empty on list projections.
type KVSecret struct {
	Name        string
	Version     string
	Value       []byte
	ContentType string
	Tags        map[string]string
	Enabled     bool
	Expires     int64
	NotBefore   int64
	Created     int64
	Updated     int64
	Managed     bool
	Current     bool
}

// KVDeletedSecret is a soft-deleted Azure Key Vault secret with its purge
// schedule. DeletedDate and ScheduledPurgeDate are Unix epoch seconds.
type KVDeletedSecret struct {
	KVSecret

	DeletedDate        int64
	ScheduledPurgeDate int64
}

// KeyVaultSecrets is the Azure Key Vault-specific secret surface: per-version
// content type and attributes (enabled/exp/nbf), update, soft-delete/recover,
// and backup/restore. It is kept off the shared Secrets interface — a
// type-asserted optional interface — so the AWS and GCP providers need not
// model Key Vault semantics.
type KeyVaultSecrets interface {
	SetKeyVaultSecret(ctx context.Context, name string, params KVSetParams) (*KVSecret, error)
	GetKeyVaultSecret(ctx context.Context, name, version string) (*KVSecret, error)
	ListKeyVaultSecrets(ctx context.Context) ([]KVSecret, error)
	ListKeyVaultSecretVersions(ctx context.Context, name string) ([]KVSecret, error)
	UpdateKeyVaultSecret(ctx context.Context, name, version string, patch KVPatch) (*KVSecret, error)
	DeleteKeyVaultSecret(ctx context.Context, name string) (*KVDeletedSecret, error)
	GetDeletedKeyVaultSecret(ctx context.Context, name string) (*KVDeletedSecret, error)
	ListDeletedKeyVaultSecrets(ctx context.Context) ([]KVDeletedSecret, error)
	RecoverDeletedKeyVaultSecret(ctx context.Context, name string) (*KVSecret, error)
	PurgeDeletedKeyVaultSecret(ctx context.Context, name string) error
	BackupKeyVaultSecret(ctx context.Context, name string) ([]byte, error)
	RestoreKeyVaultSecret(ctx context.Context, backup []byte) (*KVSecret, error)
}
