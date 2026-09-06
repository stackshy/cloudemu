package driver

import "time"

// MFA configuration values. API-created user pools default to OFF.
const (
	MFAConfigurationOff      = "OFF"
	MFAConfigurationOn       = "ON"
	MFAConfigurationOptional = "OPTIONAL"
)

// Deletion-protection values. API-created user pools default to INACTIVE.
const (
	DeletionProtectionActive   = "ACTIVE"
	DeletionProtectionInactive = "INACTIVE"
)

// UserPoolTierEssentials is the default feature tier of a new user pool.
const UserPoolTierEssentials = "ESSENTIALS"

// DomainStatusActive is the status a hosted-UI domain settles to on creation.
const DomainStatusActive = "ACTIVE"

// PreventUserExistenceErrorsLegacy is the default existence-error behavior of an
// API-created app client.
const PreventUserExistenceErrorsLegacy = "LEGACY"

// Token-validity unit values. The defaults are hours for access and id tokens
// and days for the refresh token.
const (
	TimeUnitSeconds = "seconds"
	TimeUnitMinutes = "minutes"
	TimeUnitHours   = "hours"
	TimeUnitDays    = "days"
)

// Attribute data-type values used by schema attributes.
const (
	AttributeTypeString   = "String"
	AttributeTypeNumber   = "Number"
	AttributeTypeBoolean  = "Boolean"
	AttributeTypeDateTime = "DateTime"
)

// Pagination carries a next token and a max-results cap for list operations.
type Pagination struct {
	NextToken  string
	MaxResults int32
}

// UserPool is a Cognito user pool and its configuration.
type UserPool struct {
	ID                     string
	Name                   string
	ARN                    string
	Policies               Policies
	MFAConfiguration       string
	SoftwareTokenMfaConfig *SoftwareTokenMfaConfig
	SmsMfaConfig           *SmsMfaConfig
	DeletionProtection     string
	UserPoolTier           string
	EstimatedNumberOfUsers int32
	SchemaAttributes       []SchemaAttribute
	AutoVerifiedAttributes []string
	AliasAttributes        []string
	UsernameAttributes     []string
	CreationDate           time.Time
	LastModifiedDate       time.Time
	Tags                   map[string]string
}

// UserPoolDescription is the light projection returned by ListUserPools.
type UserPoolDescription struct {
	ID               string
	Name             string
	CreationDate     time.Time
	LastModifiedDate time.Time
}

// Policies holds the user pool's policy set. Cognito only models a password
// policy today.
type Policies struct {
	PasswordPolicy PasswordPolicy
}

// PasswordPolicy is the password strength policy applied to a user pool. Real
// Cognito defaults an API-created pool to length 8 with all four character
// classes required and a 7-day temporary-password validity.
type PasswordPolicy struct {
	MinimumLength                 int32
	RequireUppercase              bool
	RequireLowercase              bool
	RequireNumbers                bool
	RequireSymbols                bool
	TemporaryPasswordValidityDays int32
}

// SchemaAttribute describes one user attribute in a pool's schema. The 20
// default OIDC attributes are seeded on pool creation.
type SchemaAttribute struct {
	Name                       string
	AttributeDataType          string
	DeveloperOnlyAttribute     bool
	Mutable                    bool
	Required                   bool
	StringAttributeConstraints *StringAttributeConstraints
	NumberAttributeConstraints *NumberAttributeConstraints
}

// StringAttributeConstraints bounds a String attribute's length. The bounds are
// strings on the wire, matching the Cognito API.
type StringAttributeConstraints struct {
	MinLength string
	MaxLength string
}

// NumberAttributeConstraints bounds a Number attribute's value.
type NumberAttributeConstraints struct {
	MinValue string
	MaxValue string
}

// CreateUserPoolInput is the input to CreateUserPool.
type CreateUserPoolInput struct {
	Name                   string
	Policies               *Policies
	MFAConfiguration       string
	DeletionProtection     string
	AutoVerifiedAttributes []string
	AliasAttributes        []string
	UsernameAttributes     []string
	SchemaAttributes       []SchemaAttribute
	UserPoolTags           map[string]string
}

// UpdateUserPoolInput is the delta applied by UpdateUserPool. A nil pointer or
// nil slice leaves the corresponding attribute unchanged; a non-nil UserPoolTags
// replaces the pool's tag set.
type UpdateUserPoolInput struct {
	ID                     string
	Policies               *Policies
	MFAConfiguration       string
	DeletionProtection     string
	AutoVerifiedAttributes []string
	UserPoolTags           map[string]string
}

// UserPoolClient is an app client of a user pool.
type UserPoolClient struct {
	ClientID                        string
	ClientName                      string
	UserPoolID                      string
	ClientSecret                    string
	RefreshTokenValidity            int32
	AccessTokenValidity             *int32
	IDTokenValidity                 *int32
	TokenValidityUnits              *TokenValidityUnits
	ExplicitAuthFlows               []string
	AuthSessionValidity             int32
	EnableTokenRevocation           bool
	PreventUserExistenceErrors      string
	CallbackURLs                    []string
	LogoutURLs                      []string
	DefaultRedirectURI              string
	AllowedOAuthFlows               []string
	AllowedOAuthScopes              []string
	AllowedOAuthFlowsUserPoolClient bool
	SupportedIdentityProviders      []string
	ReadAttributes                  []string
	WriteAttributes                 []string
	CreationDate                    time.Time
	LastModifiedDate                time.Time
}

// UserPoolClientDescription is the light projection returned by
// ListUserPoolClients.
type UserPoolClientDescription struct {
	ClientID   string
	ClientName string
	UserPoolID string
}

// TokenValidityUnits selects the time unit each token-validity value is
// expressed in. The defaults are hours for access and id tokens and days for the
// refresh token.
type TokenValidityUnits struct {
	AccessToken  string
	IDToken      string
	RefreshToken string
}

// CreateUserPoolClientInput is the input to CreateUserPoolClient and (reused for
// the full replace) UpdateUserPoolClient.
type CreateUserPoolClientInput struct {
	UserPoolID                      string
	ClientName                      string
	ClientID                        string // set only by UpdateUserPoolClient
	GenerateSecret                  bool
	RefreshTokenValidity            *int32
	AccessTokenValidity             *int32
	IDTokenValidity                 *int32
	TokenValidityUnits              *TokenValidityUnits
	ExplicitAuthFlows               []string
	AuthSessionValidity             *int32
	EnableTokenRevocation           *bool
	PreventUserExistenceErrors      string
	CallbackURLs                    []string
	LogoutURLs                      []string
	DefaultRedirectURI              string
	AllowedOAuthFlows               []string
	AllowedOAuthScopes              []string
	AllowedOAuthFlowsUserPoolClient bool
	SupportedIdentityProviders      []string
	ReadAttributes                  []string
	WriteAttributes                 []string
}

// UserPoolMfaConfig is a user pool's multi-factor-authentication configuration.
type UserPoolMfaConfig struct {
	MFAConfiguration              string
	SoftwareTokenMfaConfiguration *SoftwareTokenMfaConfig
	SmsMfaConfiguration           *SmsMfaConfig
}

// SoftwareTokenMfaConfig toggles time-based one-time-password (TOTP) MFA.
type SoftwareTokenMfaConfig struct {
	Enabled bool
}

// SmsMfaConfig holds the SMS MFA message and delivery settings.
type SmsMfaConfig struct {
	SmsAuthenticationMessage string
	SmsConfiguration         *SmsConfiguration
}

// SmsConfiguration is the SNS delivery configuration for SMS MFA.
type SmsConfiguration struct {
	SnsCallerARN string
	ExternalID   string
	SnsRegion    string
}

// SetUserPoolMfaConfigInput is the input to SetUserPoolMfaConfig.
type SetUserPoolMfaConfigInput struct {
	UserPoolID                    string
	MFAConfiguration              string
	SoftwareTokenMfaConfiguration *SoftwareTokenMfaConfig
	SmsMfaConfiguration           *SmsMfaConfig
}

// UserPoolDomain is a hosted-UI domain bound to a user pool.
type UserPoolDomain struct {
	Domain                 string
	UserPoolID             string
	AWSAccountID           string
	S3Bucket               string
	CloudFrontDistribution string
	Version                string
	Status                 string
}

// CreateUserPoolDomainInput is the input to CreateUserPoolDomain.
type CreateUserPoolDomainInput struct {
	Domain     string
	UserPoolID string
}
