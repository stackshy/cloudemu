// Package driver defines the interface and types for AWS KMS service
// implementations. It models customer master keys, aliases, and tags; the
// cryptographic operations, grants, rotation, import, and multi-region
// replication are layered on in later files of the same package.
package driver

import (
	"context"
	"time"
)

// Key usage values. A key's usage fixes which cryptographic operations it
// supports and cannot change after creation.
const (
	UsageEncryptDecrypt    = "ENCRYPT_DECRYPT"
	UsageSignVerify        = "SIGN_VERIFY"
	UsageGenerateVerifyMac = "GENERATE_VERIFY_MAC"
)

// Key spec values (the algorithm/parameters of the key material).
const (
	SpecSymmetricDefault = "SYMMETRIC_DEFAULT"
	SpecRSA2048          = "RSA_2048"
	SpecRSA3072          = "RSA_3072"
	SpecRSA4096          = "RSA_4096"
	SpecECCNISTP256      = "ECC_NIST_P256"
	SpecECCNISTP384      = "ECC_NIST_P384"
	SpecECCNISTP521      = "ECC_NIST_P521"
	SpecHMAC224          = "HMAC_224"
	SpecHMAC256          = "HMAC_256"
	SpecHMAC384          = "HMAC_384"
	SpecHMAC512          = "HMAC_512"
)

// Key state values.
const (
	StateEnabled         = "Enabled"
	StateDisabled        = "Disabled"
	StatePendingDeletion = "PendingDeletion"
	StatePendingImport   = "PendingImport"
)

// Key origin values.
const (
	OriginAWSKMS   = "AWS_KMS"
	OriginExternal = "EXTERNAL"
)

// Key manager values.
const (
	ManagerCustomer = "CUSTOMER"
	ManagerAWS      = "AWS"
)

// KeyMetadata is the public description of a KMS key, mirroring the fields the
// KMS API returns in KeyMetadata. Secret key material is never part of this.
type KeyMetadata struct {
	KeyID        string
	ARN          string
	AWSAccountID string
	Description  string
	Enabled      bool
	KeyUsage     string
	KeyState     string
	KeySpec      string
	Origin       string
	KeyManager   string
	MultiRegion  bool
	CreationDate time.Time
	DeletionDate time.Time // zero unless KeyState is PendingDeletion
	// ValidTo is the expiry of imported key material (zero when not set).
	ValidTo time.Time

	// Algorithm lists advertised for the key, populated from KeySpec+KeyUsage.
	// Only the one matching the usage is set: EncryptionAlgorithms for
	// ENCRYPT_DECRYPT, SigningAlgorithms for SIGN_VERIFY, MacAlgorithms for
	// GENERATE_VERIFY_MAC.
	EncryptionAlgorithms []string
	SigningAlgorithms    []string
	MacAlgorithms        []string

	// Multi-region configuration (populated only when MultiRegion is true).
	// MultiRegionKeyType is "PRIMARY" or "REPLICA".
	MultiRegionKeyType string
	PrimaryRegion      string
	ReplicaRegions     []string
}

// CreateKeyInput describes a key to create.
type CreateKeyInput struct {
	Description string
	KeyUsage    string // defaults to ENCRYPT_DECRYPT
	KeySpec     string // defaults to SYMMETRIC_DEFAULT
	Origin      string // defaults to AWS_KMS
	MultiRegion bool
	Policy      string
	Tags        map[string]string
}

// Alias is a friendly name pointing at a key.
type Alias struct {
	Name         string
	ARN          string
	TargetKeyID  string
	CreationDate time.Time
	UpdatedDate  time.Time
}

// KMS is the interface a KMS backend implements. Methods that take a key
// identifier accept a key ID, key ARN, alias name ("alias/foo"), or alias ARN
// and resolve them uniformly.
type KMS interface {
	Crypto
	Management
	KeyImport

	// Key lifecycle.
	CreateKey(ctx context.Context, in CreateKeyInput) (*KeyMetadata, error)
	DescribeKey(ctx context.Context, keyID string) (*KeyMetadata, error)
	ListKeys(ctx context.Context) ([]KeyMetadata, error)
	EnableKey(ctx context.Context, keyID string) error
	DisableKey(ctx context.Context, keyID string) error
	UpdateKeyDescription(ctx context.Context, keyID, description string) error
	ScheduleKeyDeletion(ctx context.Context, keyID string, pendingWindowDays int32) (*KeyMetadata, error)
	CancelKeyDeletion(ctx context.Context, keyID string) (*KeyMetadata, error)

	// Aliases.
	CreateAlias(ctx context.Context, aliasName, targetKeyID string) error
	UpdateAlias(ctx context.Context, aliasName, targetKeyID string) error
	DeleteAlias(ctx context.Context, aliasName string) error
	ListAliases(ctx context.Context, keyID string) ([]Alias, error)

	// Tags.
	TagResource(ctx context.Context, keyID string, tags map[string]string) error
	UntagResource(ctx context.Context, keyID string, tagKeys []string) error
	ListResourceTags(ctx context.Context, keyID string) (map[string]string, error)
}
