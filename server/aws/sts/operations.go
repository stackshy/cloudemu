package sts

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
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

// getCallerIdentity reports the configured account, a synthetic user ARN, and a
// synthetic user id. This is the call most SDK init paths make.
func (h *Handler) getCallerIdentity(w http.ResponseWriter, _ *http.Request) {
	awsquery.WriteXMLResponse(w, getCallerIdentityResponse{
		Xmlns: Namespace,
		Result: getCallerIdentityResult{
			Account: h.accountID,
			Arn:     "arn:aws:iam::" + h.accountID + ":user/" + callerUserName,
			UserID:  "AIDACLOUDEMU0000000000",
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
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

	awsquery.WriteXMLResponse(w, assumeRoleResponse{
		Xmlns: Namespace,
		Result: assumeRoleResult{
			Credentials: h.synthCredentials(durationFromForm(r)),
			AssumedRoleUser: assumedRoleUser{
				AssumedRoleID: "AROACLOUDEMU0000000000:" + sessionName,
				Arn:           assumedArn,
			},
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

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

	awsquery.WriteXMLResponse(w, assumeRoleWithWebIdentityResponse{
		Xmlns: Namespace,
		Result: assumeRoleWithWebIdentityResult{
			Credentials: h.synthCredentials(durationFromForm(r)),
			AssumedRoleUser: assumedRoleUser{
				AssumedRoleID: "AROACLOUDEMU0000000000:" + sessionName,
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

	awsquery.WriteXMLResponse(w, assumeRoleWithSAMLResponse{
		Xmlns: Namespace,
		Result: assumeRoleWithSAMLResult{
			Credentials: h.synthCredentials(durationFromForm(r)),
			AssumedRoleUser: assumedRoleUser{
				AssumedRoleID: "AROACLOUDEMU0000000000:" + sessionName,
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

	awsquery.WriteXMLResponse(w, getFederationTokenResponse{
		Xmlns: Namespace,
		Result: getFederationTokenResult{
			Credentials: h.synthCredentials(durationFromForm(r)),
			FederatedUser: federatedUser{
				FederatedUserID: h.accountID + ":" + name,
				Arn:             "arn:aws:sts::" + h.accountID + ":federated-user/" + name,
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

// getSessionToken returns synthetic temporary credentials.
func (h *Handler) getSessionToken(w http.ResponseWriter, r *http.Request) {
	awsquery.WriteXMLResponse(w, getSessionTokenResponse{
		Xmlns:    Namespace,
		Result:   getSessionTokenResult{Credentials: h.synthCredentials(durationFromForm(r))},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// synthCredentials builds a deterministic set of temporary credentials with an
// expiration in the future. cloudemu does not validate signatures, so any
// non-empty values satisfy SDK clients.
func (h *Handler) synthCredentials(dur time.Duration) credentials {
	if dur <= 0 {
		dur = sessionDuration
	}

	return credentials{
		AccessKeyID:     "ASIACLOUDEMU000000000",
		SecretAccessKey: "cloudemuSecretAccessKey0000000000000000",
		SessionToken:    "cloudemu-session-token",
		Expiration:      time.Now().UTC().Add(dur).Format(time.RFC3339),
	}
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
