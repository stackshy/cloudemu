package driver

import (
	"context"
	"time"
)

// Grant operation values (the operations a grant may permit).
const (
	GrantOpEncrypt                         = "Encrypt"
	GrantOpDecrypt                         = "Decrypt"
	GrantOpGenerateDataKey                 = "GenerateDataKey"
	GrantOpGenerateDataKeyWithoutPlaintext = "GenerateDataKeyWithoutPlaintext"
	GrantOpReEncryptFrom                   = "ReEncryptFrom"
	GrantOpReEncryptTo                     = "ReEncryptTo"
	GrantOpSign                            = "Sign"
	GrantOpVerify                          = "Verify"
	GrantOpGetPublicKey                    = "GetPublicKey"
	GrantOpCreateGrant                     = "CreateGrant"
	GrantOpRetireGrant                     = "RetireGrant"
	GrantOpDescribeKey                     = "DescribeKey"
	GrantOpGenerateMac                     = "GenerateMac"
	GrantOpVerifyMac                       = "VerifyMac"
)

// Rotation types.
const (
	RotationAutomatic = "AUTOMATIC"
	RotationOnDemand  = "ON_DEMAND"
)

// DefaultPolicyName is the only key-policy name KMS supports.
const DefaultPolicyName = "default"

// GrantConstraints restricts a grant to requests whose encryption context
// matches.
type GrantConstraints struct {
	EncryptionContextSubset map[string]string
	EncryptionContextEquals map[string]string
}

// Grant is a delegation of key use to a principal.
type Grant struct {
	GrantID           string
	GrantToken        string
	KeyID             string
	Name              string
	GranteePrincipal  string
	RetiringPrincipal string
	IssuingAccount    string
	Operations        []string
	Constraints       *GrantConstraints
	CreationDate      time.Time
}

// CreateGrantInput describes a grant to create.
type CreateGrantInput struct {
	KeyID             string
	GranteePrincipal  string
	RetiringPrincipal string
	Name              string
	Operations        []string
	Constraints       *GrantConstraints
}

// RotationStatus reports a key's rotation configuration.
type RotationStatus struct {
	KeyID                 string
	Enabled               bool
	RotationPeriodDays    int32
	NextRotationDate      time.Time
	OnDemandRotationCount int
}

// RotationEvent is a single past rotation.
type RotationEvent struct {
	KeyID        string
	RotationDate time.Time
	RotationType string
}

// Management is the grants/rotation/policy surface of KMS.
type Management interface {
	// Grants.
	CreateGrant(ctx context.Context, in CreateGrantInput) (grantID, grantToken string, err error)
	ListGrants(ctx context.Context, keyID string) ([]Grant, error)
	RevokeGrant(ctx context.Context, keyID, grantID string) error
	RetireGrant(ctx context.Context, grantToken, keyID, grantID string) error
	ListRetirableGrants(ctx context.Context, retiringPrincipal string) ([]Grant, error)

	// Rotation.
	EnableKeyRotation(ctx context.Context, keyID string, rotationPeriodDays int32) error
	DisableKeyRotation(ctx context.Context, keyID string) error
	GetKeyRotationStatus(ctx context.Context, keyID string) (*RotationStatus, error)
	ListKeyRotations(ctx context.Context, keyID string) ([]RotationEvent, error)
	RotateKeyOnDemand(ctx context.Context, keyID string) error

	// Key policies.
	GetKeyPolicy(ctx context.Context, keyID, policyName string) (string, error)
	PutKeyPolicy(ctx context.Context, keyID, policyName, policy string) error
	ListKeyPolicies(ctx context.Context, keyID string) ([]string, error)
}
