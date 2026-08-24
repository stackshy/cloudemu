package driver

import "context"

// KVKeyAttributes are Azure Key Vault per-version key management attributes.
// Expires and NotBefore are Unix epoch seconds; 0 means unset.
type KVKeyAttributes struct {
	Enabled   bool
	Expires   int64
	NotBefore int64
}

// KVCreateKeyParams is the Azure Key Vault CreateKey payload. Kty is the JSON
// Web Key type ("RSA", "RSA-HSM", "EC", "EC-HSM", "oct"). KeySize is the RSA
// modulus size in bits (2048/3072/4096); Curve is the EC curve name ("P-256",
// "P-384", "P-521"). PublicExponent is optional (defaults to 65537).
type KVCreateKeyParams struct {
	Kty            string
	KeySize        int
	Curve          string
	PublicExponent int
	KeyOps         []string
	Tags           map[string]string
	Attributes     KVKeyAttributes
}

// KVImportJWK carries the raw JSON Web Key material for an ImportKey call. RSA
// keys populate N/E and the private fields; EC keys populate Curve/X/Y/D; oct
// keys populate K. All byte slices are the raw big-endian integer bytes (the
// wire layer handles base64url).
type KVImportJWK struct {
	Kty    string
	Curve  string
	KeyOps []string

	// RSA components.
	N  []byte
	E  []byte
	D  []byte
	P  []byte
	Q  []byte
	DP []byte
	DQ []byte
	QI []byte

	// EC components.
	X []byte
	Y []byte

	// Symmetric (oct) key material.
	K []byte
}

// KVImportKeyParams is the Azure Key Vault ImportKey payload.
type KVImportKeyParams struct {
	Key        KVImportJWK
	HSM        bool
	Tags       map[string]string
	Attributes KVKeyAttributes
}

// KVKeyPatch is the Azure Key Vault UpdateKey payload. Nil pointer fields are
// left unchanged; SetTags true replaces the tag set with Tags; SetKeyOps true
// replaces the key operations with KeyOps.
type KVKeyPatch struct {
	Enabled   *bool
	Expires   *int64
	NotBefore *int64
	Tags      map[string]string
	SetTags   bool
	KeyOps    []string
	SetKeyOps bool
}

// KVKey is one Azure Key Vault key version and its public material. RSA keys
// carry N/E; EC keys carry Curve/X/Y. Private components are never returned.
// Created, Updated, Expires and NotBefore are Unix epoch seconds (0 = unset).
type KVKey struct {
	Name    string
	Version string
	Kty     string
	Curve   string
	KeyOps  []string

	// RSA public components.
	N []byte
	E []byte

	// EC public components.
	X []byte
	Y []byte

	Tags      map[string]string
	Enabled   bool
	Expires   int64
	NotBefore int64
	Created   int64
	Updated   int64
	Managed   bool
}

// KVDeletedKey is a soft-deleted Azure Key Vault key with its purge schedule.
// DeletedDate and ScheduledPurgeDate are Unix epoch seconds.
type KVDeletedKey struct {
	KVKey

	DeletedDate        int64
	ScheduledPurgeDate int64
}

// KVCryptoParams carries the input for a single-block cryptographic operation.
// Algorithm is the Key Vault algorithm identifier. Value is the payload to
// operate on (plaintext, ciphertext, digest, or key to wrap). Signature is the
// signature to check on a verify.
type KVCryptoParams struct {
	Algorithm string
	Value     []byte
	Signature []byte
}

// KVCryptoResult is the output of a cryptographic operation. Version identifies
// the key version that performed it (the wire layer builds the key id from it).
type KVCryptoResult struct {
	Version string
	Value   []byte
}

// KeyVaultKeys is the Azure Key Vault keys data-plane surface: key lifecycle
// (create/import/get/list/update/soft-delete/recover/purge) plus the real
// cryptographic operations (encrypt/decrypt/wrap/unwrap/sign/verify) performed
// with the private key material held in the provider. It is kept off the shared
// Secrets interface — a type-asserted optional interface — so the AWS and GCP
// providers need not model Key Vault key semantics.
type KeyVaultKeys interface {
	CreateKey(ctx context.Context, name string, params *KVCreateKeyParams) (*KVKey, error)
	ImportKey(ctx context.Context, name string, params *KVImportKeyParams) (*KVKey, error)
	GetKey(ctx context.Context, name, version string) (*KVKey, error)
	ListKeys(ctx context.Context) ([]KVKey, error)
	ListKeyVersions(ctx context.Context, name string) ([]KVKey, error)
	UpdateKey(ctx context.Context, name, version string, patch KVKeyPatch) (*KVKey, error)
	DeleteKey(ctx context.Context, name string) (*KVDeletedKey, error)
	GetDeletedKey(ctx context.Context, name string) (*KVDeletedKey, error)
	ListDeletedKeys(ctx context.Context) ([]KVDeletedKey, error)
	RecoverDeletedKey(ctx context.Context, name string) (*KVKey, error)
	PurgeDeletedKey(ctx context.Context, name string) error

	EncryptKey(ctx context.Context, name, version string, params KVCryptoParams) (*KVCryptoResult, error)
	DecryptKey(ctx context.Context, name, version string, params KVCryptoParams) (*KVCryptoResult, error)
	WrapKey(ctx context.Context, name, version string, params KVCryptoParams) (*KVCryptoResult, error)
	UnwrapKey(ctx context.Context, name, version string, params KVCryptoParams) (*KVCryptoResult, error)
	SignKey(ctx context.Context, name, version string, params KVCryptoParams) (*KVCryptoResult, error)
	VerifyKey(ctx context.Context, name, version string, params KVCryptoParams) (bool, error)
}
