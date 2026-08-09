// Package driver defines the interface and types for AWS ACM (Certificate
// Manager) implementations. It models public/imported/private certificates,
// their validation and renewal state, tags, and account configuration.
package driver

import (
	"context"
	"time"
)

// Certificate types.
const (
	TypeAmazonIssued = "AMAZON_ISSUED"
	TypeImported     = "IMPORTED"
	TypePrivate      = "PRIVATE"
)

// Certificate statuses.
const (
	StatusPendingValidation = "PENDING_VALIDATION"
	StatusIssued            = "ISSUED"
	StatusInactive          = "INACTIVE"
	StatusExpired           = "EXPIRED"
	StatusValidationTimeout = "VALIDATION_TIMED_OUT"
	StatusRevoked           = "REVOKED"
	StatusFailed            = "FAILED"
)

// Validation methods.
const (
	ValidationDNS   = "DNS"
	ValidationEmail = "EMAIL"
)

// Key algorithms.
const (
	KeyAlgRSA2048 = "RSA_2048"
	KeyAlgRSA1024 = "RSA_1024"
	KeyAlgRSA4096 = "RSA_4096"
	KeyAlgECP256  = "EC_prime256v1"
	KeyAlgECP384  = "EC_secp384r1"
)

// Certificate transparency logging preferences.
const (
	CTLoggingEnabled  = "ENABLED"
	CTLoggingDisabled = "DISABLED"
)

// RenewalEligibility values.
const (
	RenewalEligible   = "ELIGIBLE"
	RenewalIneligible = "INELIGIBLE"
)

// DomainValidation is the per-domain validation state of a certificate.
type DomainValidation struct {
	DomainName       string
	ValidationDomain string
	ValidationStatus string
	ValidationMethod string
	ResourceRecordN  string // DNS validation record name
	ResourceRecordT  string // DNS validation record type (CNAME)
	ResourceRecordV  string // DNS validation record value
}

// Certificate is the full ACM certificate description plus the material needed
// to serve GetCertificate/ExportCertificate. Private material is never part of
// the wire DescribeCertificate response.
type Certificate struct {
	ARN                     string
	DomainName              string
	SubjectAlternativeNames []string
	DomainValidationOptions []DomainValidation
	Serial                  string
	Subject                 string
	Issuer                  string
	CreatedAt               time.Time
	IssuedAt                time.Time
	ImportedAt              time.Time
	NotBefore               time.Time
	NotAfter                time.Time
	Status                  string
	KeyAlgorithm            string
	SignatureAlgorithm      string
	Type                    string
	RenewalEligibility      string
	InUseBy                 []string
	ValidationMethod        string
	CTLoggingPreference     string
	Tags                    map[string]string

	// PEM material (server-side only).
	CertificatePEM string
	ChainPEM       string
	PrivateKeyPEM  string
}

// RequestCertificateInput describes a certificate to request.
type RequestCertificateInput struct {
	DomainName              string
	SubjectAlternativeNames []string
	ValidationMethod        string
	KeyAlgorithm            string
	IdempotencyToken        string
	CTLoggingPreference     string
	Tags                    map[string]string
}

// ImportCertificateInput describes a certificate to import (or re-import when
// ARN is set).
type ImportCertificateInput struct {
	ARN            string
	CertificatePEM string
	PrivateKeyPEM  string
	ChainPEM       string
	Tags           map[string]string
}

// ListFilter narrows ListCertificates.
type ListFilter struct {
	Statuses []string
}

// AccountConfiguration is the account-level ACM configuration.
type AccountConfiguration struct {
	DaysBeforeExpiry int32
}

// ACM is the interface an ACM backend implements. A certificate identifier is
// always its ARN.
type ACM interface {
	RequestCertificate(ctx context.Context, in RequestCertificateInput) (string, error)
	ImportCertificate(ctx context.Context, in ImportCertificateInput) (string, error)
	DescribeCertificate(ctx context.Context, arn string) (*Certificate, error)
	ListCertificates(ctx context.Context, filter ListFilter) ([]Certificate, error)
	DeleteCertificate(ctx context.Context, arn string) error
	GetCertificate(ctx context.Context, arn string) (certPEM, chainPEM string, err error)
	ExportCertificate(ctx context.Context, arn string, passphrase []byte) (certPEM, chainPEM, keyPEM string, err error)
	RenewCertificate(ctx context.Context, arn string) error
	ResendValidationEmail(ctx context.Context, arn string) error
	UpdateCertificateOptions(ctx context.Context, arn, ctLoggingPreference string) error
	RevokeCertificate(ctx context.Context, arn, reason string) (string, error)
	SearchCertificates(ctx context.Context, filter ListFilter) ([]Certificate, error)

	AddTagsToCertificate(ctx context.Context, arn string, tags map[string]string) error
	RemoveTagsFromCertificate(ctx context.Context, arn string, tagKeys []string) error
	ListTagsForCertificate(ctx context.Context, arn string) (map[string]string, error)

	GetAccountConfiguration(ctx context.Context) (*AccountConfiguration, error)
	PutAccountConfiguration(ctx context.Context, cfg AccountConfiguration) error
}
