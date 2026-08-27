package sts

import "encoding/xml"

// responseMetadata is the <ResponseMetadata><RequestId/></ResponseMetadata>
// trailer every STS response carries.
type responseMetadata struct {
	RequestID string `xml:"RequestId"`
}

// credentials mirrors the STS <Credentials> element. The SDK deserializes
// AccessKeyId, SecretAccessKey, SessionToken, and Expiration (ISO-8601).
type credentials struct {
	AccessKeyID     string `xml:"AccessKeyId"`
	SecretAccessKey string `xml:"SecretAccessKey"`
	SessionToken    string `xml:"SessionToken"`
	Expiration      string `xml:"Expiration"`
}

// assumedRoleUser mirrors the STS <AssumedRoleUser> element.
type assumedRoleUser struct {
	AssumedRoleID string `xml:"AssumedRoleId"`
	Arn           string `xml:"Arn"`
}

// GetCallerIdentity ---------------------------------------------------------

type getCallerIdentityResponse struct {
	XMLName  xml.Name                `xml:"GetCallerIdentityResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   getCallerIdentityResult `xml:"GetCallerIdentityResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type getCallerIdentityResult struct {
	Arn     string `xml:"Arn"`
	UserID  string `xml:"UserId"`
	Account string `xml:"Account"`
}

// AssumeRole ----------------------------------------------------------------

type assumeRoleResponse struct {
	XMLName  xml.Name         `xml:"AssumeRoleResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   assumeRoleResult `xml:"AssumeRoleResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type assumeRoleResult struct {
	Credentials      credentials     `xml:"Credentials"`
	AssumedRoleUser  assumedRoleUser `xml:"AssumedRoleUser"`
	PackedPolicySize int             `xml:"PackedPolicySize"`
}

// GetSessionToken -----------------------------------------------------------

type getSessionTokenResponse struct {
	XMLName  xml.Name              `xml:"GetSessionTokenResponse"`
	Xmlns    string                `xml:"xmlns,attr"`
	Result   getSessionTokenResult `xml:"GetSessionTokenResult"`
	Metadata responseMetadata      `xml:"ResponseMetadata"`
}

type getSessionTokenResult struct {
	Credentials credentials `xml:"Credentials"`
}

// AssumeRoleWithWebIdentity -------------------------------------------------

type assumeRoleWithWebIdentityResponse struct {
	XMLName  xml.Name                        `xml:"AssumeRoleWithWebIdentityResponse"`
	Xmlns    string                          `xml:"xmlns,attr"`
	Result   assumeRoleWithWebIdentityResult `xml:"AssumeRoleWithWebIdentityResult"`
	Metadata responseMetadata                `xml:"ResponseMetadata"`
}

type assumeRoleWithWebIdentityResult struct {
	Credentials                 credentials     `xml:"Credentials"`
	AssumedRoleUser             assumedRoleUser `xml:"AssumedRoleUser"`
	SubjectFromWebIdentityToken string          `xml:"SubjectFromWebIdentityToken"`
	Provider                    string          `xml:"Provider"`
	Audience                    string          `xml:"Audience"`
	PackedPolicySize            int             `xml:"PackedPolicySize"`
}

// AssumeRoleWithSAML --------------------------------------------------------

type assumeRoleWithSAMLResponse struct {
	XMLName  xml.Name                 `xml:"AssumeRoleWithSAMLResponse"`
	Xmlns    string                   `xml:"xmlns,attr"`
	Result   assumeRoleWithSAMLResult `xml:"AssumeRoleWithSAMLResult"`
	Metadata responseMetadata         `xml:"ResponseMetadata"`
}

type assumeRoleWithSAMLResult struct {
	Credentials      credentials     `xml:"Credentials"`
	AssumedRoleUser  assumedRoleUser `xml:"AssumedRoleUser"`
	Subject          string          `xml:"Subject"`
	SubjectType      string          `xml:"SubjectType"`
	Issuer           string          `xml:"Issuer"`
	Audience         string          `xml:"Audience"`
	NameQualifier    string          `xml:"NameQualifier"`
	PackedPolicySize int             `xml:"PackedPolicySize"`
}

// GetFederationToken --------------------------------------------------------

type getFederationTokenResponse struct {
	XMLName  xml.Name                 `xml:"GetFederationTokenResponse"`
	Xmlns    string                   `xml:"xmlns,attr"`
	Result   getFederationTokenResult `xml:"GetFederationTokenResult"`
	Metadata responseMetadata         `xml:"ResponseMetadata"`
}

type getFederationTokenResult struct {
	Credentials      credentials   `xml:"Credentials"`
	FederatedUser    federatedUser `xml:"FederatedUser"`
	PackedPolicySize int           `xml:"PackedPolicySize"`
}

type federatedUser struct {
	FederatedUserID string `xml:"FederatedUserId"`
	Arn             string `xml:"Arn"`
}

// GetAccessKeyInfo ----------------------------------------------------------

type getAccessKeyInfoResponse struct {
	XMLName  xml.Name               `xml:"GetAccessKeyInfoResponse"`
	Xmlns    string                 `xml:"xmlns,attr"`
	Result   getAccessKeyInfoResult `xml:"GetAccessKeyInfoResult"`
	Metadata responseMetadata       `xml:"ResponseMetadata"`
}

type getAccessKeyInfoResult struct {
	Account string `xml:"Account"`
}

// DecodeAuthorizationMessage ------------------------------------------------

type decodeAuthorizationMessageResponse struct {
	XMLName  xml.Name                         `xml:"DecodeAuthorizationMessageResponse"`
	Xmlns    string                           `xml:"xmlns,attr"`
	Result   decodeAuthorizationMessageResult `xml:"DecodeAuthorizationMessageResult"`
	Metadata responseMetadata                 `xml:"ResponseMetadata"`
}

type decodeAuthorizationMessageResult struct {
	DecodedMessage string `xml:"DecodedMessage"`
}
