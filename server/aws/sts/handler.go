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
	"sync"

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
	// resolver, when the wired IAM driver supports it, resolves a presented
	// long-term (AKIA) access key id to its owning IAM user so
	// GetCallerIdentity can reflect that user instead of a constant.
	resolver iamdriver.AccessKeyResolver
	// sessions, when set, records the temporary credentials this handler mints
	// so the SigV4 authentication gate can verify signatures made with them. It
	// is wired only when EnforceAuth is on; left nil the handler returns the
	// fixed synthetic credentials it always has (auth-off byte-for-byte).
	sessions *SessionStore

	// identities records, for a temporary (ASIA) access key id this handler has
	// minted, the caller identity it represents (an AssumedRoleUser, a
	// FederatedUser, or the resolved caller for GetSessionToken), so a later
	// GetCallerIdentity call made with that access key reflects the operation
	// that minted it rather than a constant. It is populated regardless of
	// EnforceAuth. With EnforceAuth off every mint shares cloudemu's one fixed
	// synthetic access key id (see mintCredentials), so this map holds a single
	// entry that reflects whichever operation minted most recently — a known
	// limitation of the unauthenticated default. With EnforceAuth on each mint
	// gets a unique key, so entries never collide.
	identMu    sync.RWMutex
	identities map[string]callerIdentity
}

// SetSessions wires the temporary-credential store. When set, AssumeRole /
// GetSessionToken / GetFederationToken (and the other credential-minting
// operations) issue unique, verifiable credentials recorded in the store.
func (h *Handler) SetSessions(s *SessionStore) { h.sessions = s }

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

	h := &Handler{accountID: accountID, region: region, identities: make(map[string]callerIdentity)}
	if te, ok := iam.(roleTrustEvaluator); ok {
		h.trust = te
	}

	if resolver, ok := iam.(iamdriver.AccessKeyResolver); ok {
		h.resolver = resolver
	}

	return h
}

// rememberIdentity records the caller identity a just-minted temporary access
// key id represents. A blank key or an identity with nothing to report is a
// no-op.
func (h *Handler) rememberIdentity(accessKeyID string, id callerIdentity) {
	if accessKeyID == "" || (id.arn == "" && id.userID == "") {
		return
	}

	h.identMu.Lock()
	h.identities[accessKeyID] = id
	h.identMu.Unlock()
}

// identityFor returns the caller identity recorded for a temporary access key
// id, if any.
func (h *Handler) identityFor(accessKeyID string) (callerIdentity, bool) {
	h.identMu.RLock()
	defer h.identMu.RUnlock()

	id, ok := h.identities[accessKeyID]

	return id, ok
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
