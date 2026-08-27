package kms

import (
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// grantConstraintsJSON is the wire shape of grant constraints.
type grantConstraintsJSON struct {
	EncryptionContextSubset map[string]string `json:"EncryptionContextSubset,omitempty"`
	EncryptionContextEquals map[string]string `json:"EncryptionContextEquals,omitempty"`
}

func toDriverConstraints(c *grantConstraintsJSON) *kmsdriver.GrantConstraints {
	if c == nil {
		return nil
	}

	return &kmsdriver.GrantConstraints{
		EncryptionContextSubset: c.EncryptionContextSubset,
		EncryptionContextEquals: c.EncryptionContextEquals,
	}
}

func fromDriverConstraints(c *kmsdriver.GrantConstraints) *grantConstraintsJSON {
	if c == nil {
		return nil
	}

	return &grantConstraintsJSON{
		EncryptionContextSubset: c.EncryptionContextSubset,
		EncryptionContextEquals: c.EncryptionContextEquals,
	}
}

// grantJSON is the wire shape of a grant in list responses.
type grantJSON struct {
	GrantID           string                `json:"GrantId"`
	KeyID             string                `json:"KeyId"`
	Name              string                `json:"Name,omitempty"`
	GranteePrincipal  string                `json:"GranteePrincipal,omitempty"`
	RetiringPrincipal string                `json:"RetiringPrincipal,omitempty"`
	IssuingAccount    string                `json:"IssuingAccount,omitempty"`
	Operations        []string              `json:"Operations,omitempty"`
	Constraints       *grantConstraintsJSON `json:"Constraints,omitempty"`
	CreationDate      *float64              `json:"CreationDate,omitempty"`
}

func grantToJSON(g *kmsdriver.Grant) grantJSON {
	return grantJSON{
		GrantID:           g.GrantID,
		KeyID:             g.KeyID,
		Name:              g.Name,
		GranteePrincipal:  g.GranteePrincipal,
		RetiringPrincipal: g.RetiringPrincipal,
		IssuingAccount:    g.IssuingAccount,
		Operations:        g.Operations,
		Constraints:       fromDriverConstraints(g.Constraints),
		CreationDate:      epochOrNil(g.CreationDate),
	}
}

// --- request/response shapes ---

type createGrantRequest struct {
	KeyID             string                `json:"KeyId"`
	GranteePrincipal  string                `json:"GranteePrincipal"`
	RetiringPrincipal string                `json:"RetiringPrincipal"`
	Name              string                `json:"Name"`
	Operations        []string              `json:"Operations"`
	Constraints       *grantConstraintsJSON `json:"Constraints"`
}

type createGrantResponse struct {
	GrantID    string `json:"GrantId"`
	GrantToken string `json:"GrantToken"`
}

type listGrantsRequest struct {
	KeyID  string `json:"KeyId"`
	Limit  int32  `json:"Limit"`
	Marker string `json:"Marker"`
}

type listGrantsResponse struct {
	Grants     []grantJSON `json:"Grants"`
	NextMarker string      `json:"NextMarker,omitempty"`
	Truncated  bool        `json:"Truncated"`
}

type revokeGrantRequest struct {
	KeyID   string `json:"KeyId"`
	GrantID string `json:"GrantId"`
}

type retireGrantRequest struct {
	GrantToken string `json:"GrantToken"`
	KeyID      string `json:"KeyId"`
	GrantID    string `json:"GrantId"`
}

type listRetirableGrantsRequest struct {
	RetiringPrincipal string `json:"RetiringPrincipal"`
	Limit             int32  `json:"Limit"`
	Marker            string `json:"Marker"`
}

type listKeyRotationsRequest struct {
	KeyID  string `json:"KeyId"`
	Limit  int32  `json:"Limit"`
	Marker string `json:"Marker"`
}

type listKeyPoliciesRequest struct {
	KeyID  string `json:"KeyId"`
	Limit  int32  `json:"Limit"`
	Marker string `json:"Marker"`
}

type enableKeyRotationRequest struct {
	KeyID                string `json:"KeyId"`
	RotationPeriodInDays int32  `json:"RotationPeriodInDays"`
}

type getKeyRotationStatusResponse struct {
	KeyID                 string   `json:"KeyId"`
	KeyRotationEnabled    bool     `json:"KeyRotationEnabled"`
	RotationPeriodInDays  int32    `json:"RotationPeriodInDays,omitempty"`
	NextRotationDate      *float64 `json:"NextRotationDate,omitempty"`
	OnDemandRotationCount int      `json:"OnDemandRotationsPerformed,omitempty"`
}

type rotationJSON struct {
	KeyID        string   `json:"KeyId"`
	RotationDate *float64 `json:"RotationDate,omitempty"`
	RotationType string   `json:"RotationType"`
}

type listKeyRotationsResponse struct {
	Rotations  []rotationJSON `json:"Rotations"`
	NextMarker string         `json:"NextMarker,omitempty"`
	Truncated  bool           `json:"Truncated"`
}

type rotateKeyOnDemandResponse struct {
	KeyID string `json:"KeyId"`
}

type getKeyPolicyRequest struct {
	KeyID      string `json:"KeyId"`
	PolicyName string `json:"PolicyName"`
}

type getKeyPolicyResponse struct {
	Policy     string `json:"Policy"`
	PolicyName string `json:"PolicyName"`
}

type putKeyPolicyRequest struct {
	KeyID      string `json:"KeyId"`
	PolicyName string `json:"PolicyName"`
	Policy     string `json:"Policy"`
}

type listKeyPoliciesResponse struct {
	PolicyNames []string `json:"PolicyNames"`
	NextMarker  string   `json:"NextMarker,omitempty"`
	Truncated   bool     `json:"Truncated"`
}
