package driver

import (
	"context"
	"time"
)

// Wrapping algorithms for importing key material.
const (
	WrapRSAESOAEPSHA1   = "RSAES_OAEP_SHA_1"
	WrapRSAESOAEPSHA256 = "RSAES_OAEP_SHA_256"
	WrapRSAESPKCS1V15   = "RSAES_PKCS1_V1_5"
)

// Wrapping key specs.
const (
	WrapKeySpecRSA2048 = "RSA_2048"
	WrapKeySpecRSA3072 = "RSA_3072"
	WrapKeySpecRSA4096 = "RSA_4096"
)

// Expiration models for imported key material.
const (
	ExpirationKeyMaterialExpires       = "KEY_MATERIAL_EXPIRES"
	ExpirationKeyMaterialDoesNotExpire = "KEY_MATERIAL_DOES_NOT_EXPIRE"
)

// GetParametersForImportInput describes a GetParametersForImport request.
type GetParametersForImportInput struct {
	KeyID             string
	WrappingAlgorithm string
	WrappingKeySpec   string
}

// GetParametersForImportOutput carries the wrapping public key and import token.
type GetParametersForImportOutput struct {
	KeyID             string
	ImportToken       []byte
	PublicKey         []byte // SPKI DER of the RSA wrapping public key
	ParametersValidTo time.Time
}

// ImportKeyMaterialInput describes an ImportKeyMaterial request.
type ImportKeyMaterialInput struct {
	KeyID                string
	ImportToken          []byte
	EncryptedKeyMaterial []byte
	ValidTo              time.Time
	ExpirationModel      string
}

// ReplicateKeyInput describes a ReplicateKey request.
type ReplicateKeyInput struct {
	KeyID         string
	ReplicaRegion string
	Description   string
	Policy        string
	Tags          map[string]string
}

// ReplicateKeyOutput carries the created replica's metadata, policy, and tags.
type ReplicateKeyOutput struct {
	ReplicaKeyMetadata *KeyMetadata
	ReplicaPolicy      string
	ReplicaTags        map[string]string
}

// KeyImport is the import-material and multi-region surface of KMS.
type KeyImport interface {
	GetParametersForImport(ctx context.Context, in GetParametersForImportInput) (*GetParametersForImportOutput, error)
	ImportKeyMaterial(ctx context.Context, in ImportKeyMaterialInput) error
	DeleteImportedKeyMaterial(ctx context.Context, keyID string) error

	ReplicateKey(ctx context.Context, in ReplicateKeyInput) (*ReplicateKeyOutput, error)
	UpdatePrimaryRegion(ctx context.Context, keyID, primaryRegion string) error
}
