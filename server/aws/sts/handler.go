// Package sts implements the AWS STS query-protocol as a server.Handler.
// Point the real aws-sdk-go-v2 STS client at a Server registered with this
// handler and GetCallerIdentity / AssumeRole / GetSessionToken work against
// cloudemu's configured identity.
//
// STS has no backing driver: identity is derived from the AccountID and Region
// the AWS server was configured with. This exists so SDK code paths that call
// sts:GetCallerIdentity or sts:AssumeRole on init succeed against cloudemu.
//
// STS shares the AWS query wire shape with EC2, RDS, Redshift, IAM, and the
// other query-protocol handlers (POST + form-encoded body, XML response). To
// keep dispatch unambiguous, this handler's Matches predicate parses the form
// body once and only claims requests whose Action is one of the known STS
// operations. The EC2 handler is the catch-all for all other query-protocol
// actions, so this handler MUST register before EC2. Its action set (see
// stsActions) is disjoint from RDS, Redshift, IAM, ELBv2, ElastiCache, SNS,
// and EC2, so no shadowing occurs.
package sts

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// Namespace is the XML namespace for AWS STS responses.
const Namespace = "https://sts.amazonaws.com/doc/2011-06-15/"

const (
	formContentType  = "application/x-www-form-urlencoded"
	maxFormBodyBytes = 1 << 20
)

// stsActions is the set of Action values this handler recognizes. Matches uses
// it to decide whether to claim a request.
var stsActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"GetCallerIdentity":          {},
	"AssumeRole":                 {},
	"GetSessionToken":            {},
	"AssumeRoleWithWebIdentity":  {},
	"AssumeRoleWithSAML":         {},
	"GetFederationToken":         {},
	"GetAccessKeyInfo":           {},
	"DecodeAuthorizationMessage": {},
}

// roleTrustEvaluator is the AWS-specific trust-policy surface AssumeRole uses to
// decide whether the caller may assume a role. It is not part of the portable
// IAM driver, so the handler type-asserts the injected IAM driver for it. When
// the driver does not implement it (or none was wired), AssumeRole stays
// permissive — the standalone behavior for callers that only need init creds.
type roleTrustEvaluator interface {
	EvaluateAssumeRoleTrust(ctx context.Context, roleName, callerPrincipal string) (roleExists, allowed bool)
}

// Handler serves STS query-protocol requests. It carries the account and region
// the AWS server was configured with. It has no backing driver of its own, but
// when an IAM driver that can evaluate trust policies is wired, AssumeRole
// enforces the target role's trust policy and existence.
type Handler struct {
	accountID string
	region    string
	trust     roleTrustEvaluator
}

// New returns an STS handler that reports the given accountID and region. Empty
// values fall back to sensible defaults so a well-formed identity is always
// returned. When iam is non-nil and can evaluate trust policies, AssumeRole
// checks the target role's trust policy (and existence); otherwise AssumeRole
// stays permissive.
func New(accountID, region string, iam iamdriver.IAM) *Handler {
	if accountID == "" {
		accountID = defaultAccountID
	}

	if region == "" {
		region = defaultRegion
	}

	h := &Handler{accountID: accountID, region: region}
	if te, ok := iam.(roleTrustEvaluator); ok {
		h.trust = te
	}

	return h
}

const (
	defaultAccountID = "000000000000"
	defaultRegion    = "us-east-1"
)

// Matches returns true if the request looks like an AWS STS query-protocol call
// (POST + form-encoded body whose Action is one of the known STS operations).
// Calling ParseForm here caches the parsed form on the request so ServeHTTP can
// use it without re-reading the body.
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}

	if r.Method != http.MethodPost {
		return false
	}

	if !strings.HasPrefix(r.Header.Get("Content-Type"), formContentType) {
		return false
	}

	r.Body = http.MaxBytesReader(nil, r.Body, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		return false
	}

	_, ok := stsActions[r.Form.Get("Action")]

	return ok
}

// ServeHTTP dispatches on Action. The form has already been parsed by Matches.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Form.Get("Action") {
	case "GetCallerIdentity":
		h.getCallerIdentity(w, r)
	case "AssumeRole":
		h.assumeRole(w, r)
	case "GetSessionToken":
		h.getSessionToken(w, r)
	case "AssumeRoleWithWebIdentity":
		h.assumeRoleWithWebIdentity(w, r)
	case "AssumeRoleWithSAML":
		h.assumeRoleWithSAML(w, r)
	case "GetFederationToken":
		h.getFederationToken(w, r)
	case "GetAccessKeyInfo":
		h.getAccessKeyInfo(w, r)
	case "DecodeAuthorizationMessage":
		h.decodeAuthorizationMessage(w, r)
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidAction", "unknown STS action: "+r.Form.Get("Action"))
	}
}
