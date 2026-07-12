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
