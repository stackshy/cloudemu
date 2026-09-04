package sts

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/authctx"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	"github.com/stackshy/cloudemu/v2/server/wire/sigv4"
)

// callerUserName is the synthetic IAM user name reported by GetCallerIdentity.
const callerUserName = "cloudemu"

// defaultSessionName is the RoleSessionName baked into assumed-role responses
// when the request omits one.
const defaultSessionName = "cloudemu-session"

// sessionDuration is the lifetime baked into synthetic temporary credentials.
// Real STS defaults to 1h for AssumeRole and 12h for GetSessionToken; a fixed
// value in the future is all any SDK requires.
const sessionDuration = time.Hour

// getCallerIdentity reports the configured account and the caller identity
// resolved from the request's presented credentials (see
// Handler.resolveCallerIdentity), reflecting an IAM user's own access key, an
// assumed-role/federated session this handler minted, or — failing those — a
// synthetic identity derived from the presented access key id so distinct
// callers are not all collapsed onto one fake identity.
func (h *Handler) getCallerIdentity(w http.ResponseWriter, r *http.Request) {
	id := h.resolveCallerIdentity(r)

	awsquery.WriteXMLResponse(w, getCallerIdentityResponse{
		Xmlns: Namespace,
		Result: getCallerIdentityResult{
			Account: h.accountID,
			Arn:     id.arn,
			UserID:  id.userID,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// callerIdentity is the Arn/UserId pair GetCallerIdentity reports for a
// caller, and what a minted temporary credential set is remembered under (see
// Handler.identities) so a later GetCallerIdentity call made with those
// credentials reflects it.
type callerIdentity struct {
	arn    string
	userID string
}

// resolveCallerIdentity derives the caller identity for r, in priority order:
//
//  1. A principal the SigV4 authentication gate already verified (EnforceAuth
//     on) and resolved to a real IAM user's ARN.
//  2. No presented credentials at all: the fixed placeholder identity (keeps
//     an unsigned/anonymous call's response unchanged).
//  3. A temporary access key id this handler itself minted (AssumeRole,
//     AssumeRoleWithWebIdentity, AssumeRoleWithSAML, GetFederationToken, or
//     GetSessionToken): the identity recorded for it at mint time.
//  4. A long-term access key id a wired IAM driver recognizes: that key's
//     owning user.
//  5. Otherwise: a synthetic-but-stable identity derived from the presented
//     access key id, so distinct callers are not all collapsed onto one fake
//     identity even when cloudemu cannot resolve who they are.
//
// cloudemu's wire layer parses SigV4 material but does not verify it unless
// EnforceAuth is on, so outside that mode this reports who the request claims
// to be, not a cryptographically proven identity — matching AWS's own
// GetCallerIdentity semantics of reflecting the presented credential.
func (h *Handler) resolveCallerIdentity(r *http.Request) callerIdentity {
	if p, ok := authctx.PrincipalFrom(r.Context()); ok && p.ARN != "" {
		return callerIdentity{arn: p.ARN, userID: firstNonEmpty(p.UserID, syntheticUserID(p.AccessKeyID))}
	}

	akid := sigv4.AccessKeyID(r)
	if akid == "" {
		return h.defaultCallerIdentity()
	}

	if id, ok := h.identityFor(akid); ok {
		return id
	}

	if h.resolver != nil {
		if info, ok := h.resolver.AccessKeyByID(r.Context(), akid); ok && info.UserARN != "" {
			return callerIdentity{arn: info.UserARN, userID: firstNonEmpty(info.UserID, syntheticUserID(akid))}
		}
	}

	return h.syntheticCallerIdentity(akid)
}

// defaultCallerIdentity is the well-formed placeholder GetCallerIdentity
// reports for a request that presents no SigV4 credentials at all, preserving
// the prior fixed response for that case.
func (h *Handler) defaultCallerIdentity() callerIdentity {
	return callerIdentity{
		arn:    "arn:aws:iam::" + h.accountID + ":user/" + callerUserName,
		userID: "AIDACLOUDEMU0000000000",
	}
}

// syntheticCallerIdentity derives a stable identity from a presented access
// key id cloudemu cannot otherwise resolve, so distinct callers still get
// distinct (if fake) identities instead of all collapsing onto one constant.
func (h *Handler) syntheticCallerIdentity(akid string) callerIdentity {
	return callerIdentity{
		arn:    "arn:aws:iam::" + h.accountID + ":user/" + strings.ReplaceAll(akid, "/", "-"),
		userID: syntheticUserID(akid),
	}
}

// syntheticUserIDHexLen is the number of hex characters (from a SHA-256 digest
// of the access key id) appended after the AIDA prefix, matching real IAM
// unique ids' length.
const syntheticUserIDHexLen = 16

// syntheticUserID deterministically derives an AIDA-style unique id from an
// access key id, so the same presented key always reports the same synthetic
// UserId and two different keys practically never collide.
func syntheticUserID(akid string) string {
	if akid == "" {
		return "AIDACLOUDEMU0000000000"
	}

	sum := sha256.Sum256([]byte(akid))

	return "AIDA" + strings.ToUpper(hex.EncodeToString(sum[:]))[:syntheticUserIDHexLen]
}

// firstNonEmpty returns a if non-empty, else b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

// assumeRole returns synthetic temporary credentials and an AssumedRoleUser
// derived from the requested RoleArn and RoleSessionName. When an IAM trust
// evaluator is wired, the target role must exist and its trust policy must allow
// the caller to sts:AssumeRole; otherwise AWS returns AccessDenied (403).
func (h *Handler) assumeRole(w http.ResponseWriter, r *http.Request) {
	roleArn := r.Form.Get("RoleArn")
	sessionName := r.Form.Get("RoleSessionName")

	if sessionName == "" {
		sessionName = defaultSessionName
	}

	// The assumed-role ARN AWS returns is
	//   arn:aws:sts::{account}:assumed-role/{role-name}/{session-name}
	// where role-name is the last path segment of the requested RoleArn.
	roleName := roleNameFromArn(roleArn)

	if !h.trustAllows(r, roleName) {
		// Real STS returns AccessDenied (403) both when the trust policy denies
		// the caller and when the role does not exist (it does not disclose which).
		awsquery.WriteXMLError(w, http.StatusForbidden, "AccessDenied",
			"User is not authorized to perform sts:AssumeRole on "+roleArn)

		return
	}

	assumedArn := "arn:aws:sts::" + h.accountID + ":assumed-role/" + roleName + "/" + sessionName
	assumedRoleID := assumedRoleIDPrefix + ":" + sessionName

	creds, ok := h.mintCredentials(w, durationFromForm(r), callerIdentity{arn: assumedArn, userID: assumedRoleID})
	if !ok {
		return
	}

	awsquery.WriteXMLResponse(w, assumeRoleResponse{
		Xmlns: Namespace,
		Result: assumeRoleResult{
			Credentials: creds,
			AssumedRoleUser: assumedRoleUser{
				AssumedRoleID: assumedRoleID,
				Arn:           assumedArn,
			},
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// assumedRoleIDPrefix is the synthetic AssumedRoleId prefix (real STS uses the
// "AROA" prefix followed by a unique id) reported by every AssumeRole-family
// operation, and used to derive the identity a minted session is recorded
// under for GetCallerIdentity.
const assumedRoleIDPrefix = "AROACLOUDEMU0000000000"

// trustAllows reports whether the caller may assume roleName. With no trust
// evaluator wired it stays permissive (standalone init-creds behavior). With one
// wired, a missing role or a trust policy that does not allow the caller both
// deny. The caller principal is the account-root identity — cloudemu does not
// verify SigV4, so it evaluates trust against a consistent same-account root.
func (h *Handler) trustAllows(r *http.Request, roleName string) bool {
	if h.trust == nil {
		return true
	}

	callerPrincipal := "arn:aws:iam::" + h.accountID + ":root"
	_, allowed := h.trust.EvaluateAssumeRoleTrust(r.Context(), roleName, callerPrincipal)

	return allowed
}

// assumeRoleWithWebIdentity mirrors AssumeRole but is fed by an OIDC/WebIdentity
// token (the flow EKS IRSA uses). cloudemu does not validate the token; it
// echoes a synthetic subject/provider derived from the request.
func (h *Handler) assumeRoleWithWebIdentity(w http.ResponseWriter, r *http.Request) {
	sessionName := r.Form.Get("RoleSessionName")
	if sessionName == "" {
		sessionName = defaultSessionName
	}

	roleName := roleNameFromArn(r.Form.Get("RoleArn"))
	assumedArn := "arn:aws:sts::" + h.accountID + ":assumed-role/" + roleName + "/" + sessionName

	provider := r.Form.Get("ProviderId")
	if provider == "" {
		provider = "cloudemu.local"
	}

	assumedRoleID := assumedRoleIDPrefix + ":" + sessionName

	creds, ok := h.mintCredentials(w, durationFromForm(r), callerIdentity{arn: assumedArn, userID: assumedRoleID})
	if !ok {
		return
	}

	awsquery.WriteXMLResponse(w, assumeRoleWithWebIdentityResponse{
		Xmlns: Namespace,
		Result: assumeRoleWithWebIdentityResult{
			Credentials: creds,
			AssumedRoleUser: assumedRoleUser{
				AssumedRoleID: assumedRoleID,
				Arn:           assumedArn,
			},
			SubjectFromWebIdentityToken: "cloudemu-web-identity-subject",
			Provider:                    provider,
			Audience:                    "sts.amazonaws.com",
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// assumeRoleWithSAML mirrors AssumeRole but is fed by a SAML assertion.
// cloudemu does not validate the assertion; it echoes a synthetic subject.
func (h *Handler) assumeRoleWithSAML(w http.ResponseWriter, r *http.Request) {
	roleName := roleNameFromArn(r.Form.Get("RoleArn"))
	sessionName := "cloudemu-saml-session"
	assumedArn := "arn:aws:sts::" + h.accountID + ":assumed-role/" + roleName + "/" + sessionName
	assumedRoleID := assumedRoleIDPrefix + ":" + sessionName

	creds, ok := h.mintCredentials(w, durationFromForm(r), callerIdentity{arn: assumedArn, userID: assumedRoleID})
	if !ok {
		return
	}

	awsquery.WriteXMLResponse(w, assumeRoleWithSAMLResponse{
		Xmlns: Namespace,
		Result: assumeRoleWithSAMLResult{
			Credentials: creds,
			AssumedRoleUser: assumedRoleUser{
				AssumedRoleID: assumedRoleID,
				Arn:           assumedArn,
			},
			Subject:       "cloudemu-saml-subject",
			SubjectType:   "persistent",
			Issuer:        r.Form.Get("PrincipalArn"),
			Audience:      "https://signin.aws.amazon.com/saml",
			NameQualifier: "cloudemu",
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// getFederationToken returns synthetic temporary credentials for a federated
// user named by the request's Name parameter.
func (h *Handler) getFederationToken(w http.ResponseWriter, r *http.Request) {
	name := r.Form.Get("Name")
	if name == "" {
		name = "cloudemu-federated"
	}

	fedArn := "arn:aws:sts::" + h.accountID + ":federated-user/" + name
	fedUserID := h.accountID + ":" + name

	creds, ok := h.mintCredentials(w, durationFromForm(r), callerIdentity{arn: fedArn, userID: fedUserID})
	if !ok {
		return
	}

	awsquery.WriteXMLResponse(w, getFederationTokenResponse{
		Xmlns: Namespace,
		Result: getFederationTokenResult{
			Credentials: creds,
			FederatedUser: federatedUser{
				FederatedUserID: fedUserID,
				Arn:             fedArn,
			},
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// getAccessKeyInfo reports the account an access key belongs to. cloudemu has a
// single configured account, so it always reports that account.
func (h *Handler) getAccessKeyInfo(w http.ResponseWriter, _ *http.Request) {
	awsquery.WriteXMLResponse(w, getAccessKeyInfoResponse{
		Xmlns:    Namespace,
		Result:   getAccessKeyInfoResult{Account: h.accountID},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// decodeAuthorizationMessage returns a synthetic decoded authorization message.
// The real service decodes an encoded authorization failure blob; cloudemu does
// not produce those, so it returns a well-formed placeholder JSON document.
func (h *Handler) decodeAuthorizationMessage(w http.ResponseWriter, _ *http.Request) {
	const decoded = `{"allowed":false,"decodedMessage":"cloudemu does not model authorization decisions"}`

	awsquery.WriteXMLResponse(w, decodeAuthorizationMessageResponse{
		Xmlns:    Namespace,
		Result:   decodeAuthorizationMessageResult{DecodedMessage: decoded},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// durationFromForm reads the optional DurationSeconds parameter, falling back to
// the default session lifetime when it is absent or invalid.
func durationFromForm(r *http.Request) time.Duration {
	if v := r.Form.Get("DurationSeconds"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}

	return sessionDuration
}

// getSessionToken returns temporary credentials for the caller's own
// identity — a GetSessionToken session represents the same caller, not a role
// or a federated user, so the minted credentials are recorded under the
// identity resolveCallerIdentity resolves for the request that asked for them.
func (h *Handler) getSessionToken(w http.ResponseWriter, r *http.Request) {
	creds, ok := h.mintCredentials(w, durationFromForm(r), h.resolveCallerIdentity(r))
	if !ok {
		return
	}

	awsquery.WriteXMLResponse(w, getSessionTokenResponse{
		Xmlns:    Namespace,
		Result:   getSessionTokenResult{Credentials: creds},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// synthCredentials builds a set of temporary credentials with an expiration in
// the future. When a session store is wired (EnforceAuth on) it mints unique,
// verifiable credentials and records their secret so the auth gate can verify a
// signature made with them; otherwise it returns the fixed synthetic values it
// always has (default, auth-off behavior is byte-for-byte unchanged). Either
// way, the returned access key id is recorded under identity so a later
// GetCallerIdentity call made with these credentials reflects it.
func (h *Handler) synthCredentials(dur time.Duration, identity callerIdentity) (credentials, error) {
	if dur <= 0 {
		dur = sessionDuration
	}

	if h.sessions != nil {
		sess, err := h.sessions.Mint(dur)
		if err != nil {
			return credentials{}, err
		}

		h.rememberIdentity(sess.AccessKeyID, identity)

		return credentials{
			AccessKeyID:     sess.AccessKeyID,
			SecretAccessKey: sess.SecretAccessKey,
			SessionToken:    sess.SessionToken,
			Expiration:      sess.Expiration.Format(time.RFC3339),
		}, nil
	}

	const fixedAccessKeyID = "ASIACLOUDEMU000000000"

	h.rememberIdentity(fixedAccessKeyID, identity)

	return credentials{
		AccessKeyID:     fixedAccessKeyID,
		SecretAccessKey: "cloudemuSecretAccessKey0000000000000000",
		SessionToken:    "cloudemu-session-token",
		Expiration:      time.Now().UTC().Add(dur).Format(time.RFC3339),
	}, nil
}

// mintCredentials builds temporary credentials representing identity for a
// handler, writing an InternalFailure error response and reporting ok=false
// when credential generation fails closed (a crypto/rand read error).
func (h *Handler) mintCredentials(w http.ResponseWriter, dur time.Duration, identity callerIdentity) (credentials, bool) {
	creds, err := h.synthCredentials(dur, identity)
	if err != nil {
		awsquery.WriteXMLError(w, http.StatusInternalServerError, "InternalFailure",
			"could not generate temporary credentials")

		return credentials{}, false
	}

	return creds, true
}

// roleNameFromArn extracts the role name (last path segment) from a role ARN
// such as "arn:aws:iam::123456789012:role/path/MyRole". Falls back to a stable
// placeholder when the ARN is missing or malformed.
func roleNameFromArn(arn string) string {
	if arn == "" {
		return "cloudemu-role"
	}

	// Role ARNs are "...:role/<name>" (name may itself contain a path with
	// slashes); take the segment after ":role/", then its last path element.
	name := arn
	if _, after, ok := strings.Cut(arn, ":role/"); ok {
		name = after
	}

	// Trim any trailing slash(es) so a stray "MyRole/" doesn't yield an empty
	// last segment.
	name = strings.TrimRight(name, "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return "cloudemu-role"
	}

	return name
}
