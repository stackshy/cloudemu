package driver

import "context"

// KVCreateCertificateParams is the Azure Key Vault CreateCertificate payload.
// Subject, DNSNames and ValidityMonths are parsed out of the request policy to
// generate a self-signed X.509 certificate; PolicyRaw is the verbatim policy
// JSON, round-tripped back on GetCertificate so unknown policy fields survive.
type KVCreateCertificateParams struct {
	Subject        string
	DNSNames       []string
	ValidityMonths int
	ContentType    string
	Tags           map[string]string
	Attributes     KVAttributes
	PolicyRaw      []byte
}

// KVCertificate is one Azure Key Vault certificate version. Created, Updated,
// Expires and NotBefore are Unix epoch seconds (0 = unset). CER is the
// DER-encoded X.509 certificate; Thumbprint is its SHA-1 digest (the x5t
// value). PolicyRaw is the verbatim certificate policy JSON.
type KVCertificate struct {
	Name        string
	Version     string
	CER         []byte
	Thumbprint  []byte
	ContentType string
	Tags        map[string]string
	Enabled     bool
	Expires     int64
	NotBefore   int64
	Created     int64
	Updated     int64
	PolicyRaw   []byte
}

// KVDeletedCertificate is a soft-deleted Azure Key Vault certificate with its
// purge schedule. DeletedDate and ScheduledPurgeDate are Unix epoch seconds.
type KVDeletedCertificate struct {
	KVCertificate

	DeletedDate        int64
	ScheduledPurgeDate int64
}

// KeyVaultCertificates is the Azure Key Vault-specific certificate data-plane
// surface: create (self-signed), get, list, versions and soft-delete/recover.
// Like KeyVaultSecrets and KeyVaultKeys it is a type-asserted optional
// interface kept off the shared Secrets interface, so the AWS and GCP providers
// need not model Key Vault certificate semantics.
//
// Every method takes vault, the vault name the request is scoped to (derived by
// the wire layer from the request host). Each vault is an isolated namespace:
// the same certificate name in two different vaults refers to two different
// certificates.
type KeyVaultCertificates interface {
	CreateCertificate(ctx context.Context, vault, name string, params KVCreateCertificateParams) (*KVCertificate, error)
	GetCertificate(ctx context.Context, vault, name, version string) (*KVCertificate, error)
	ListCertificates(ctx context.Context, vault string) ([]KVCertificate, error)
	ListCertificateVersions(ctx context.Context, vault, name string) ([]KVCertificate, error)
	DeleteCertificate(ctx context.Context, vault, name string) (*KVDeletedCertificate, error)
	GetDeletedCertificate(ctx context.Context, vault, name string) (*KVDeletedCertificate, error)
	ListDeletedCertificates(ctx context.Context, vault string) ([]KVDeletedCertificate, error)
	RecoverDeletedCertificate(ctx context.Context, vault, name string) (*KVCertificate, error)
	PurgeDeletedCertificate(ctx context.Context, vault, name string) error
}
