package cognito

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// epochOrNil renders a time as a Unix-epoch float the Cognito SDK decodes into a
// *time.Time, or nil for the zero time.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// --- nested wire shapes ---

type passwordPolicyJSON struct {
	MinimumLength                 int32 `json:"MinimumLength,omitempty"`
	RequireUppercase              bool  `json:"RequireUppercase"`
	RequireLowercase              bool  `json:"RequireLowercase"`
	RequireNumbers                bool  `json:"RequireNumbers"`
	RequireSymbols                bool  `json:"RequireSymbols"`
	TemporaryPasswordValidityDays int32 `json:"TemporaryPasswordValidityDays,omitempty"`
}

type policiesJSON struct {
	PasswordPolicy *passwordPolicyJSON `json:"PasswordPolicy,omitempty"`
}

type stringConstraintsJSON struct {
	MinLength string `json:"MinLength,omitempty"`
	MaxLength string `json:"MaxLength,omitempty"`
}

type numberConstraintsJSON struct {
	MinValue string `json:"MinValue,omitempty"`
	MaxValue string `json:"MaxValue,omitempty"`
}

type schemaAttributeJSON struct {
	Name                       string                 `json:"Name,omitempty"`
	AttributeDataType          string                 `json:"AttributeDataType,omitempty"`
	DeveloperOnlyAttribute     bool                   `json:"DeveloperOnlyAttribute"`
	Mutable                    bool                   `json:"Mutable"`
	Required                   bool                   `json:"Required"`
	StringAttributeConstraints *stringConstraintsJSON `json:"StringAttributeConstraints,omitempty"`
	NumberAttributeConstraints *numberConstraintsJSON `json:"NumberAttributeConstraints,omitempty"`
}

type userPoolJSON struct {
	ID                     string                `json:"Id,omitempty"`
	Name                   string                `json:"Name,omitempty"`
	Arn                    string                `json:"Arn,omitempty"`
	Policies               *policiesJSON         `json:"Policies,omitempty"`
	MfaConfiguration       string                `json:"MfaConfiguration,omitempty"`
	DeletionProtection     string                `json:"DeletionProtection,omitempty"`
	UserPoolTier           string                `json:"UserPoolTier,omitempty"`
	EstimatedNumberOfUsers int32                 `json:"EstimatedNumberOfUsers"`
	SchemaAttributes       []schemaAttributeJSON `json:"SchemaAttributes,omitempty"`
	AutoVerifiedAttributes []string              `json:"AutoVerifiedAttributes,omitempty"`
	AliasAttributes        []string              `json:"AliasAttributes,omitempty"`
	UsernameAttributes     []string              `json:"UsernameAttributes,omitempty"`
	CreationDate           *float64              `json:"CreationDate,omitempty"`
	LastModifiedDate       *float64              `json:"LastModifiedDate,omitempty"`
	UserPoolTags           map[string]string     `json:"UserPoolTags,omitempty"`
}

type userPoolDescriptionJSON struct {
	ID               string   `json:"Id,omitempty"`
	Name             string   `json:"Name,omitempty"`
	CreationDate     *float64 `json:"CreationDate,omitempty"`
	LastModifiedDate *float64 `json:"LastModifiedDate,omitempty"`
}

type tokenValidityUnitsJSON struct {
	AccessToken  string `json:"AccessToken,omitempty"`
	IDToken      string `json:"IdToken,omitempty"`
	RefreshToken string `json:"RefreshToken,omitempty"`
}

type userPoolClientJSON struct {
	ClientID                        string                  `json:"ClientId,omitempty"`
	ClientName                      string                  `json:"ClientName,omitempty"`
	UserPoolID                      string                  `json:"UserPoolId,omitempty"`
	ClientSecret                    string                  `json:"ClientSecret,omitempty"`
	RefreshTokenValidity            int32                   `json:"RefreshTokenValidity"`
	AccessTokenValidity             *int32                  `json:"AccessTokenValidity,omitempty"`
	IDTokenValidity                 *int32                  `json:"IdTokenValidity,omitempty"`
	TokenValidityUnits              *tokenValidityUnitsJSON `json:"TokenValidityUnits,omitempty"`
	ExplicitAuthFlows               []string                `json:"ExplicitAuthFlows,omitempty"`
	AuthSessionValidity             int32                   `json:"AuthSessionValidity,omitempty"`
	EnableTokenRevocation           bool                    `json:"EnableTokenRevocation"`
	PreventUserExistenceErrors      string                  `json:"PreventUserExistenceErrors,omitempty"`
	CallbackURLs                    []string                `json:"CallbackURLs,omitempty"`
	LogoutURLs                      []string                `json:"LogoutURLs,omitempty"`
	DefaultRedirectURI              string                  `json:"DefaultRedirectURI,omitempty"`
	AllowedOAuthFlows               []string                `json:"AllowedOAuthFlows,omitempty"`
	AllowedOAuthScopes              []string                `json:"AllowedOAuthScopes,omitempty"`
	AllowedOAuthFlowsUserPoolClient bool                    `json:"AllowedOAuthFlowsUserPoolClient"`
	SupportedIdentityProviders      []string                `json:"SupportedIdentityProviders,omitempty"`
	ReadAttributes                  []string                `json:"ReadAttributes,omitempty"`
	WriteAttributes                 []string                `json:"WriteAttributes,omitempty"`
	CreationDate                    *float64                `json:"CreationDate,omitempty"`
	LastModifiedDate                *float64                `json:"LastModifiedDate,omitempty"`
}

type userPoolClientDescriptionJSON struct {
	ClientID   string `json:"ClientId,omitempty"`
	ClientName string `json:"ClientName,omitempty"`
	UserPoolID string `json:"UserPoolId,omitempty"`
}

type domainDescriptionJSON struct {
	Domain                 string `json:"Domain,omitempty"`
	UserPoolID             string `json:"UserPoolId,omitempty"`
	AWSAccountID           string `json:"AWSAccountId,omitempty"`
	S3Bucket               string `json:"S3Bucket,omitempty"`
	CloudFrontDistribution string `json:"CloudFrontDistribution,omitempty"`
	Version                string `json:"Version,omitempty"`
	Status                 string `json:"Status,omitempty"`
}

// --- driver <-> wire conversion ---

func policiesToWire(p driver.Policies) *policiesJSON {
	pp := p.PasswordPolicy

	return &policiesJSON{PasswordPolicy: &passwordPolicyJSON{
		MinimumLength:                 pp.MinimumLength,
		RequireUppercase:              pp.RequireUppercase,
		RequireLowercase:              pp.RequireLowercase,
		RequireNumbers:                pp.RequireNumbers,
		RequireSymbols:                pp.RequireSymbols,
		TemporaryPasswordValidityDays: pp.TemporaryPasswordValidityDays,
	}}
}

func policiesFromWire(p *policiesJSON) *driver.Policies {
	if p == nil || p.PasswordPolicy == nil {
		return nil
	}

	pp := p.PasswordPolicy

	return &driver.Policies{PasswordPolicy: driver.PasswordPolicy{
		MinimumLength:                 pp.MinimumLength,
		RequireUppercase:              pp.RequireUppercase,
		RequireLowercase:              pp.RequireLowercase,
		RequireNumbers:                pp.RequireNumbers,
		RequireSymbols:                pp.RequireSymbols,
		TemporaryPasswordValidityDays: pp.TemporaryPasswordValidityDays,
	}}
}

func schemaAttributesToWire(in []driver.SchemaAttribute) []schemaAttributeJSON {
	if len(in) == 0 {
		return nil
	}

	out := make([]schemaAttributeJSON, len(in))
	for i, a := range in {
		out[i] = schemaAttributeJSON{
			Name:                   a.Name,
			AttributeDataType:      a.AttributeDataType,
			DeveloperOnlyAttribute: a.DeveloperOnlyAttribute,
			Mutable:                a.Mutable,
			Required:               a.Required,
		}
		if c := a.StringAttributeConstraints; c != nil {
			out[i].StringAttributeConstraints = &stringConstraintsJSON{MinLength: c.MinLength, MaxLength: c.MaxLength}
		}

		if c := a.NumberAttributeConstraints; c != nil {
			out[i].NumberAttributeConstraints = &numberConstraintsJSON{MinValue: c.MinValue, MaxValue: c.MaxValue}
		}
	}

	return out
}

func schemaAttributesFromWire(in []schemaAttributeJSON) []driver.SchemaAttribute {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.SchemaAttribute, len(in))
	for i, a := range in {
		out[i] = driver.SchemaAttribute{
			Name:                   a.Name,
			AttributeDataType:      a.AttributeDataType,
			DeveloperOnlyAttribute: a.DeveloperOnlyAttribute,
			Mutable:                a.Mutable,
			Required:               a.Required,
		}
		if c := a.StringAttributeConstraints; c != nil {
			out[i].StringAttributeConstraints = &driver.StringAttributeConstraints{MinLength: c.MinLength, MaxLength: c.MaxLength}
		}

		if c := a.NumberAttributeConstraints; c != nil {
			out[i].NumberAttributeConstraints = &driver.NumberAttributeConstraints{MinValue: c.MinValue, MaxValue: c.MaxValue}
		}
	}

	return out
}

func userPoolToWire(p *driver.UserPool) userPoolJSON {
	return userPoolJSON{
		ID:                     p.ID,
		Name:                   p.Name,
		Arn:                    p.ARN,
		Policies:               policiesToWire(p.Policies),
		MfaConfiguration:       p.MFAConfiguration,
		DeletionProtection:     p.DeletionProtection,
		UserPoolTier:           p.UserPoolTier,
		EstimatedNumberOfUsers: p.EstimatedNumberOfUsers,
		SchemaAttributes:       schemaAttributesToWire(p.SchemaAttributes),
		AutoVerifiedAttributes: p.AutoVerifiedAttributes,
		AliasAttributes:        p.AliasAttributes,
		UsernameAttributes:     p.UsernameAttributes,
		CreationDate:           epochOrNil(p.CreationDate),
		LastModifiedDate:       epochOrNil(p.LastModifiedDate),
		UserPoolTags:           p.Tags,
	}
}

func tokenValidityUnitsToWire(u *driver.TokenValidityUnits) *tokenValidityUnitsJSON {
	if u == nil {
		return nil
	}

	return &tokenValidityUnitsJSON{AccessToken: u.AccessToken, IDToken: u.IDToken, RefreshToken: u.RefreshToken}
}

func tokenValidityUnitsFromWire(u *tokenValidityUnitsJSON) *driver.TokenValidityUnits {
	if u == nil {
		return nil
	}

	return &driver.TokenValidityUnits{AccessToken: u.AccessToken, IDToken: u.IDToken, RefreshToken: u.RefreshToken}
}

func userPoolClientToWire(c *driver.UserPoolClient) userPoolClientJSON {
	return userPoolClientJSON{
		ClientID:                        c.ClientID,
		ClientName:                      c.ClientName,
		UserPoolID:                      c.UserPoolID,
		ClientSecret:                    c.ClientSecret,
		RefreshTokenValidity:            c.RefreshTokenValidity,
		AccessTokenValidity:             c.AccessTokenValidity,
		IDTokenValidity:                 c.IDTokenValidity,
		TokenValidityUnits:              tokenValidityUnitsToWire(c.TokenValidityUnits),
		ExplicitAuthFlows:               c.ExplicitAuthFlows,
		AuthSessionValidity:             c.AuthSessionValidity,
		EnableTokenRevocation:           c.EnableTokenRevocation,
		PreventUserExistenceErrors:      c.PreventUserExistenceErrors,
		CallbackURLs:                    c.CallbackURLs,
		LogoutURLs:                      c.LogoutURLs,
		DefaultRedirectURI:              c.DefaultRedirectURI,
		AllowedOAuthFlows:               c.AllowedOAuthFlows,
		AllowedOAuthScopes:              c.AllowedOAuthScopes,
		AllowedOAuthFlowsUserPoolClient: c.AllowedOAuthFlowsUserPoolClient,
		SupportedIdentityProviders:      c.SupportedIdentityProviders,
		ReadAttributes:                  c.ReadAttributes,
		WriteAttributes:                 c.WriteAttributes,
		CreationDate:                    epochOrNil(c.CreationDate),
		LastModifiedDate:                epochOrNil(c.LastModifiedDate),
	}
}

func domainToWire(d *driver.UserPoolDomain) domainDescriptionJSON {
	return domainDescriptionJSON{
		Domain:                 d.Domain,
		UserPoolID:             d.UserPoolID,
		AWSAccountID:           d.AWSAccountID,
		S3Bucket:               d.S3Bucket,
		CloudFrontDistribution: d.CloudFrontDistribution,
		Version:                d.Version,
		Status:                 d.Status,
	}
}
