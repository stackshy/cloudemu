package sts

import (
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// callerUserName is the synthetic IAM user name reported by GetCallerIdentity.
const callerUserName = "cloudemu"

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
// derived from the requested RoleArn and RoleSessionName.
func (h *Handler) assumeRole(w http.ResponseWriter, r *http.Request) {
	roleArn := r.Form.Get("RoleArn")
	sessionName := r.Form.Get("RoleSessionName")

	if sessionName == "" {
		sessionName = "cloudemu-session"
	}

	// The assumed-role ARN AWS returns is
	//   arn:aws:sts::{account}:assumed-role/{role-name}/{session-name}
	// where role-name is the last path segment of the requested RoleArn.
	roleName := roleNameFromArn(roleArn)
	assumedArn := "arn:aws:sts::" + h.accountID + ":assumed-role/" + roleName + "/" + sessionName

	awsquery.WriteXMLResponse(w, assumeRoleResponse{
		Xmlns: Namespace,
		Result: assumeRoleResult{
			Credentials: h.synthCredentials(),
			AssumedRoleUser: assumedRoleUser{
				AssumedRoleID: "AROACLOUDEMU0000000000:" + sessionName,
				Arn:           assumedArn,
			},
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// getSessionToken returns synthetic temporary credentials.
func (h *Handler) getSessionToken(w http.ResponseWriter, _ *http.Request) {
	awsquery.WriteXMLResponse(w, getSessionTokenResponse{
		Xmlns:    Namespace,
		Result:   getSessionTokenResult{Credentials: h.synthCredentials()},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// synthCredentials builds a deterministic set of temporary credentials with an
// expiration in the future. cloudemu does not validate signatures, so any
// non-empty values satisfy SDK clients.
func (h *Handler) synthCredentials() credentials {
	return credentials{
		AccessKeyID:     "ASIACLOUDEMU000000000",
		SecretAccessKey: "cloudemuSecretAccessKey0000000000000000",
		SessionToken:    "cloudemu-session-token",
		Expiration:      time.Now().UTC().Add(sessionDuration).Format(time.RFC3339),
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
