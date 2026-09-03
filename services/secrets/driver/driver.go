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
	// Replication is the GCP Secret Manager replication policy (automatic or
	// user-managed). Required by GCP's secrets.create; ignored by AWS/Azure.
	Replication *GCPReplication
	// Annotations is GCP Secret Manager custom metadata, distinct from labels.
	// Ignored by AWS/Azure.
	Annotations map[string]string
	// TTL is the input-only GCP Secret Manager time-to-live (a duration such as
	// "3600s"); the provider derives ExpireTime from it. Ignored by AWS/Azure.
	TTL string
	// ExpireTime is an explicit GCP Secret Manager RFC3339 expiry; an alternative
	// to TTL. Ignored by AWS/Azure.
	ExpireTime string
	// Rotation is the GCP Secret Manager rotation policy. Ignored by AWS/Azure.
	Rotation *GCPRotation
	// Topics are the GCP Secret Manager Pub/Sub topic names notified on control
	// plane operations. Ignored by AWS/Azure.
	Topics []string
	// VersionAliases maps GCP Secret Manager version aliases to version ids.
	// Ignored by AWS/Azure.
	VersionAliases map[string]string
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
	// Replication is the GCP Secret Manager replication policy echoed on the
	// secret resource. Nil for providers that don't model it.
	Replication *GCPReplication
	// Annotations is GCP Secret Manager custom metadata echoed on the secret.
	// Nil for providers that don't model it.
	Annotations map[string]string
	// ExpireTime is the GCP Secret Manager RFC3339 expiry, always emitted on
	// output when set (derived from TTL on input). Empty when unset.
	ExpireTime string
	// Rotation is the GCP Secret Manager rotation policy echoed on the secret.
	// Nil for providers that don't model it.
	Rotation *GCPRotation
	// Topics are the GCP Secret Manager Pub/Sub topic names echoed on the secret.
	// Nil for providers that don't model it.
	Topics []string
	// VersionAliases maps GCP Secret Manager version aliases to version ids,
	// echoed on the secret and honored by GetSecretValue. Nil when unset.
	VersionAliases map[string]string
}

// GCPReplica is one location a user-managed GCP secret replicates to, with an
// optional customer-managed encryption key.
type GCPReplica struct {
	Location   string
	KMSKeyName string
}

// GCPReplication is a GCP Secret Manager replication policy: automatic (with an
// optional customer-managed key) or user-managed with an explicit replica list.
type GCPReplication struct {
	Automatic           bool
	AutomaticKMSKeyName string
	UserManaged         []GCPReplica
}

// GCPRotation is a GCP Secret Manager rotation policy. RotationPeriod is
// input-only; NextRotationTime is echoed on output.
type GCPRotation struct {
	RotationPeriod   string
	NextRotationTime string
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

// SecretRotationRules is the AWS Secrets Manager RotateSecret rotation-schedule
// configuration (RotationRules on the wire). AutomaticallyAfterDays of 0 means
// unset.
type SecretRotationRules struct {
	AutomaticallyAfterDays int64
	Duration               string
	ScheduleExpression     string
}

// SecretRotationInfo is a secret's AWS Secrets Manager rotation configuration,
// echoed by DescribeSecret/ListSecrets. LastRotatedDate and NextRotationDate
// are RFC3339, empty when not applicable.
type SecretRotationInfo struct {
	Enabled          bool
	LambdaARN        string
	Rules            SecretRotationRules
	LastRotatedDate  string
	NextRotationDate string
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
// fields whose Set* flag is true (mapped from the update mask) are applied; a
// Set* flag with an empty/nil value clears that field.
type GCPSecretPatch struct {
	Labels    map[string]string
	SetLabels bool

	Annotations    map[string]string
	SetAnnotations bool

	Topics    []string
	SetTopics bool

	VersionAliases    map[string]string
	SetVersionAliases bool

	Rotation    *GCPRotation
	SetRotation bool

	// ExpireTime / TTL carry the new expiry: TTL (a duration such as "3600s") is
	// resolved against the provider clock, ExpireTime is an explicit RFC3339
	// stamp. SetExpireTime true with both empty clears the expiry.
	ExpireTime    string
	TTL           string
	SetExpireTime bool

	// Etag is the caller-supplied optimistic-concurrency precondition: when
	// non-empty, the patch is only applied if it matches the secret's currently
	// stored etag (real GCP's leniency — an empty Etag always skips the check).
	Etag string
}

// GCPSecretPreconditionError signals a caller-supplied etag precondition (on a
// version lifecycle verb or secrets.patch) that did not match the currently
// stored resource's etag. Real Secret Manager answers 412 Precondition Failed
// with reason "conditionNotMet", matching GCS/Compute Engine's fingerprint
// convention elsewhere in cloudemu — which does NOT map to the canonical
// FailedPrecondition→409 the gcprest default uses, so providers return this
// typed error and the handler matches it with errors.As to emit the exact 412.
type GCPSecretPreconditionError struct {
	Message string
}

func (e *GCPSecretPreconditionError) Error() string {
	if e.Message == "" {
		return "conditionNotMet"
	}

	return e.Message
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
	// already-enabled version and fails on a DESTROYED one. A non-empty etag
	// must match the version's currently stored etag or the call fails with a
	// *GCPSecretPreconditionError; an empty etag always succeeds.
	EnableSecretVersion(ctx context.Context, name, versionID, etag string) (*SecretVersion, error)
	// DisableSecretVersion moves a version to DISABLED. It fails on a DESTROYED
	// version. A non-empty etag must match the version's currently stored etag
	// or the call fails with a *GCPSecretPreconditionError; an empty etag
	// always succeeds.
	DisableSecretVersion(ctx context.Context, name, versionID, etag string) (*SecretVersion, error)
	// DestroySecretVersion moves a version to DESTROYED, wipes its payload, and
	// stamps its destroyTime. It fails on an already-DESTROYED version. A
	// non-empty etag must match the version's currently stored etag or the call
	// fails with a *GCPSecretPreconditionError; an empty etag always succeeds.
	DestroySecretVersion(ctx context.Context, name, versionID, etag string) (*SecretVersion, error)
	// PatchSecret applies a partial update (labels, annotations, topics, version
	// aliases, rotation, expiry) to a secret's metadata, honoring the update
	// mask. A non-empty patch.Etag must match the secret's currently stored
	// etag or the call fails with a *GCPSecretPreconditionError.
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
