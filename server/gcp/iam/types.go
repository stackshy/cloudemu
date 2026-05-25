package iam

import (
	"encoding/json"
	"net/http"
)

const contentTypeJSON = "application/json"

// --- ServiceAccount ---

// serviceAccount mirrors the GCP iam.googleapis.com v1 ServiceAccount JSON.
// Fields are populated from a driver UserInfo plus the project the request
// was scoped to.
type serviceAccount struct {
	Name           string `json:"name"`
	ProjectID      string `json:"projectId,omitempty"`
	UniqueID       string `json:"uniqueId,omitempty"`
	Email          string `json:"email,omitempty"`
	DisplayName    string `json:"displayName,omitempty"`
	Description    string `json:"description,omitempty"`
	OAuth2ClientID string `json:"oauth2ClientId,omitempty"`
	Etag           string `json:"etag,omitempty"`
}

// createServiceAccountRequest is the POST body for ServiceAccounts.Create.
type createServiceAccountRequest struct {
	AccountID      string         `json:"accountId"`
	ServiceAccount serviceAccount `json:"serviceAccount"`
}

// patchServiceAccountRequest is the PATCH body for ServiceAccounts.Patch.
// The SDK wraps the resource in this envelope and adds an updateMask telling
// the server which fields to touch. We ignore the mask (emulator always
// full-replaces) but must decode the wrapper to find the resource at all —
// the wrapper field is mandatory.
type patchServiceAccountRequest struct {
	ServiceAccount serviceAccount `json:"serviceAccount"`
	UpdateMask     string         `json:"updateMask,omitempty"`
}

// listServiceAccountsResponse is the wire shape for the SA list response.
type listServiceAccountsResponse struct {
	Accounts      []serviceAccount `json:"accounts"`
	NextPageToken string           `json:"nextPageToken,omitempty"`
}

// --- Role ---

// role mirrors the GCP iam.googleapis.com v1 Role JSON. The driver only
// carries name + path + permissions doc; everything else is reconstructed
// from the request payload.
type role struct {
	Name                string   `json:"name"`
	Title               string   `json:"title,omitempty"`
	Description         string   `json:"description,omitempty"`
	IncludedPermissions []string `json:"includedPermissions,omitempty"`
	Stage               string   `json:"stage,omitempty"`
	Etag                string   `json:"etag,omitempty"`
	Deleted             bool     `json:"deleted,omitempty"`
}

// createRoleRequest is the POST body for Roles.Create.
type createRoleRequest struct {
	RoleID string `json:"roleId"`
	Role   role   `json:"role"`
}

// listRolesResponse is the wire shape for the role list response.
type listRolesResponse struct {
	Roles         []role `json:"roles"`
	NextPageToken string `json:"nextPageToken,omitempty"`
}

// roleProps is the JSON shape we stash in driver Role.AssumeRolePolicyDoc.
// The driver field has no native place for GCP-style permissions, so we
// serialize the GCP-specific properties block as JSON.
type roleProps struct {
	Title               string   `json:"title,omitempty"`
	Description         string   `json:"description,omitempty"`
	IncludedPermissions []string `json:"includedPermissions,omitempty"`
	Stage               string   `json:"stage,omitempty"`
}

// --- ServiceAccountKey ---

// serviceAccountKey mirrors the GCP v1 ServiceAccountKey JSON.
type serviceAccountKey struct {
	Name            string `json:"name"`
	PrivateKeyType  string `json:"privateKeyType,omitempty"`
	KeyAlgorithm    string `json:"keyAlgorithm,omitempty"`
	PrivateKeyData  string `json:"privateKeyData,omitempty"`
	PublicKeyData   string `json:"publicKeyData,omitempty"`
	ValidAfterTime  string `json:"validAfterTime,omitempty"`
	ValidBeforeTime string `json:"validBeforeTime,omitempty"`
	KeyOrigin       string `json:"keyOrigin,omitempty"`
	KeyType         string `json:"keyType,omitempty"`
}

// listKeysResponse is the wire shape for the SA key list response.
type listKeysResponse struct {
	Keys []serviceAccountKey `json:"keys"`
}

// --- error envelope ---

// gcpError is the JSON shape every GCP API client expects for non-2xx
// responses: {"error": {"code": 404, "message": "...", "status": "NOT_FOUND"}}.
type gcpError struct {
	Error gcpErrorBody `json:"error"`
}

type gcpErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status,omitempty"`
}

// writeJSON writes v as a 200 OK JSON response. The IAM handler always
// returns 200 on success; non-2xx flows go through writeError.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, statusStr, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(gcpError{
		Error: gcpErrorBody{Code: status, Message: msg, Status: statusStr},
	})
}
