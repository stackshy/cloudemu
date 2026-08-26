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
	// ClientRequestToken pins the initial version's id (AWS Secrets Manager uses
	// the token as the VersionId, a UUID). Empty means the provider generates
	// one. Ignored by Azure/GCP.
	ClientRequestToken string
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
	// Etag is an opaque optimistic-concurrency tag (GCP Secret Manager), echoed
	// on the secret resource. Empty for providers that don't model it.
	Etag string
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
	// State is the lifecycle state of the version (GCP Secret Manager):
	// "ENABLED", "DISABLED", or "DESTROYED". Empty for providers that don't
	// model per-version state.
	State string
	// DestroyTime is the RFC3339 time the version was destroyed (GCP Secret
	// Manager); empty unless State is "DESTROYED".
	DestroyTime string
	// Etag is an opaque optimistic-concurrency tag (GCP Secret Manager), echoed
	// on the version resource. Empty for providers that don't model it.
	Etag string
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

// Version lifecycle states reported by GCP Secret Manager.
const (
	VersionEnabled   = "ENABLED"
	VersionDisabled  = "DISABLED"
	VersionDestroyed = "DESTROYED"
)

// GCPSecretPatch is the GCP Secret Manager secrets.patch payload. Only the
// fields named in the update mask are applied; SetLabels true replaces the
// label set with Labels (including clearing it when Labels is empty).
type GCPSecretPatch struct {
	Labels    map[string]string
	SetLabels bool
}

// GCPIAMBinding binds an IAM role to a set of members.
type GCPIAMBinding struct {
	Role    string
	Members []string
}

// GCPIAMPolicy is a GCP IAM policy as served by getIamPolicy/setIamPolicy.
type GCPIAMPolicy struct {
	Version  int
	Bindings []GCPIAMBinding
	Etag     string
}

// GCPSecrets is the GCP Secret Manager-specific surface kept off the shared
// Secrets interface — a type-asserted optional interface — so the AWS and Azure
// providers need not model version lifecycle, secret patch, or IAM semantics.
type GCPSecrets interface {
	// EnableSecretVersion moves a version to ENABLED. It is idempotent on an
	// already-enabled version and fails on a DESTROYED one.
	EnableSecretVersion(ctx context.Context, name, versionID string) (*SecretVersion, error)
	// DisableSecretVersion moves a version to DISABLED. It fails on a DESTROYED
	// version.
	DisableSecretVersion(ctx context.Context, name, versionID string) (*SecretVersion, error)
	// DestroySecretVersion moves a version to DESTROYED, wipes its payload, and
	// stamps its destroyTime. It fails on an already-DESTROYED version.
	DestroySecretVersion(ctx context.Context, name, versionID string) (*SecretVersion, error)
	// PatchSecret applies a partial update (labels) to a secret's metadata.
	PatchSecret(ctx context.Context, name string, patch GCPSecretPatch) (*SecretInfo, error)
	// GetSecretIAMPolicy returns the secret's stored IAM policy (an empty
	// versioned policy when none has been set).
	GetSecretIAMPolicy(ctx context.Context, name string) (*GCPIAMPolicy, error)
	// SetSecretIAMPolicy stores the secret's IAM policy and returns it with a
	// refreshed etag.
	SetSecretIAMPolicy(ctx context.Context, name string, policy GCPIAMPolicy) (*GCPIAMPolicy, error)
	// TestSecretIAMPermissions returns the subset of permissions the caller
	// holds. CloudEmu does not enforce IAM, so all requested permissions are
	// reported as granted.
	TestSecretIAMPermissions(ctx context.Context, name string, permissions []string) ([]string, error)
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
//
// Every method takes vault, the vault name the request is scoped to (derived
// by the wire layer from the request host, e.g. the {vault-name} label of
// {vault-name}.vault.azure.net). Each vault is an isolated namespace: the
// same secret name in two different vaults refers to two different secrets.
type KeyVaultSecrets interface {
	SetKeyVaultSecret(ctx context.Context, vault, name string, params KVSetParams) (*KVSecret, error)
	GetKeyVaultSecret(ctx context.Context, vault, name, version string) (*KVSecret, error)
	ListKeyVaultSecrets(ctx context.Context, vault string) ([]KVSecret, error)
	ListKeyVaultSecretVersions(ctx context.Context, vault, name string) ([]KVSecret, error)
	UpdateKeyVaultSecret(ctx context.Context, vault, name, version string, patch KVPatch) (*KVSecret, error)
	DeleteKeyVaultSecret(ctx context.Context, vault, name string) (*KVDeletedSecret, error)
	GetDeletedKeyVaultSecret(ctx context.Context, vault, name string) (*KVDeletedSecret, error)
	ListDeletedKeyVaultSecrets(ctx context.Context, vault string) ([]KVDeletedSecret, error)
	RecoverDeletedKeyVaultSecret(ctx context.Context, vault, name string) (*KVSecret, error)
	PurgeDeletedKeyVaultSecret(ctx context.Context, vault, name string) error
	BackupKeyVaultSecret(ctx context.Context, vault, name string) ([]byte, error)
	RestoreKeyVaultSecret(ctx context.Context, vault string, backup []byte) (*KVSecret, error)
}
