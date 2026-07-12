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
// actions, so this handler MUST register before EC2. Its action set
// (GetCallerIdentity, AssumeRole, GetSessionToken) is disjoint from RDS,
// Redshift, IAM, ELBv2, ElastiCache, SNS, and EC2, so no shadowing occurs.
package sts

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/server/wire/awsquery"
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
	"GetCallerIdentity": {},
	"AssumeRole":        {},
	"GetSessionToken":   {},
}

// Handler serves STS query-protocol requests. It carries the account and region
// the AWS server was configured with; there is no backing driver.
type Handler struct {
	accountID string
	region    string
}

// New returns an STS handler that reports the given accountID and region.
// Empty values fall back to sensible defaults so a well-formed identity is
// always returned.
func New(accountID, region string) *Handler {
	if accountID == "" {
		accountID = defaultAccountID
	}

	if region == "" {
		region = defaultRegion
	}

	return &Handler{accountID: accountID, region: region}
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
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidAction", "unknown STS action: "+r.Form.Get("Action"))
	}
}
