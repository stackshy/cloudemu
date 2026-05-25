package iam

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	iamdriver "github.com/stackshy/cloudemu/iam/driver"
)

const maxBodyBytes = 1 << 20

// --- ServiceAccounts ---

func (h *Handler) createServiceAccount(w http.ResponseWriter, r *http.Request, project string) {
	var in createServiceAccountRequest
	if !decodeJSONBody(w, r, &in) {
		return
	}

	if in.AccountID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
			"accountId is required")
		return
	}

	email := buildSAEmail(in.AccountID, project)

	// Persist as a driver User. The SA email is the natural primary key.
	if _, err := h.iam.CreateUser(r.Context(), iamdriver.UserConfig{
		Name: email,
		Path: project,
		Tags: map[string]string{
			"displayName": in.ServiceAccount.DisplayName,
			"description": in.ServiceAccount.Description,
		},
	}); err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toServiceAccountJSON(project, email, &in.ServiceAccount))
}

func (h *Handler) getServiceAccount(w http.ResponseWriter, r *http.Request, project, email string) {
	user, err := h.iam.GetUser(r.Context(), email)
	if err != nil {
		writeCErr(w, err)
		return
	}

	sa := saFromUser(user)
	writeJSON(w, toServiceAccountJSON(project, email, &sa))
}

func (h *Handler) listServiceAccounts(w http.ResponseWriter, r *http.Request, project string) {
	users, err := h.iam.ListUsers(r.Context())
	if err != nil {
		writeCErr(w, err)
		return
	}

	out := listServiceAccountsResponse{Accounts: make([]serviceAccount, 0, len(users))}

	for i := range users {
		u := &users[i]
		if u.Path != project {
			continue
		}

		sa := saFromUser(u)
		out.Accounts = append(out.Accounts, toServiceAccountJSON(project, u.Name, &sa))
	}

	writeJSON(w, out)
}

func (h *Handler) deleteServiceAccount(w http.ResponseWriter, r *http.Request, project, email string) {
	_ = project

	if err := h.iam.DeleteUser(r.Context(), email); err != nil {
		writeCErr(w, err)
		return
	}

	// GCP returns an empty body with 200 on successful SA delete.
	writeJSON(w, struct{}{})
}

func (h *Handler) updateServiceAccount(w http.ResponseWriter, r *http.Request, project, email string) {
	var in serviceAccount
	if !decodeJSONBody(w, r, &in) {
		return
	}

	// The driver has no Update, so we do a destructive replace. Tags carry
	// the displayName / description we re-store.
	if err := h.iam.DeleteUser(r.Context(), email); err != nil {
		writeCErr(w, err)
		return
	}

	if _, err := h.iam.CreateUser(r.Context(), iamdriver.UserConfig{
		Name: email,
		Path: project,
		Tags: map[string]string{
			"displayName": in.DisplayName,
			"description": in.Description,
		},
	}); err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toServiceAccountJSON(project, email, &in))
}

// --- Roles ---

func (h *Handler) createRole(w http.ResponseWriter, r *http.Request, project string) {
	var in createRoleRequest
	if !decodeJSONBody(w, r, &in) {
		return
	}

	if in.RoleID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
			"roleId is required")
		return
	}

	props := roleProps{
		Title:               in.Role.Title,
		Description:         in.Role.Description,
		IncludedPermissions: in.Role.IncludedPermissions,
		Stage:               in.Role.Stage,
	}

	propsJSON, err := json.Marshal(props)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL",
			"could not encode role props: "+err.Error())
		return
	}

	if _, err := h.iam.CreateRole(r.Context(), iamdriver.RoleConfig{
		Name:                in.RoleID,
		Path:                project,
		AssumeRolePolicyDoc: string(propsJSON),
	}); err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toRoleJSON(project, in.RoleID, &props))
}

func (h *Handler) getRole(w http.ResponseWriter, r *http.Request, project, roleID string) {
	dr, err := h.iam.GetRole(r.Context(), roleID)
	if err != nil {
		writeCErr(w, err)
		return
	}

	props, perr := decodeRoleProps(dr.AssumeRolePolicyDoc)
	if perr != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL",
			"could not decode stored role props: "+perr.Error())
		return
	}

	writeJSON(w, toRoleJSON(project, roleID, &props))
}

func (h *Handler) listRoles(w http.ResponseWriter, r *http.Request, project string) {
	roles, err := h.iam.ListRoles(r.Context())
	if err != nil {
		writeCErr(w, err)
		return
	}

	out := listRolesResponse{Roles: make([]role, 0, len(roles))}

	for i := range roles {
		dr := &roles[i]
		if dr.Path != project {
			continue
		}

		props, perr := decodeRoleProps(dr.AssumeRolePolicyDoc)
		if perr != nil {
			continue
		}

		out.Roles = append(out.Roles, toRoleJSON(project, dr.Name, &props))
	}

	writeJSON(w, out)
}

func (h *Handler) deleteRole(w http.ResponseWriter, r *http.Request, project, roleID string) {
	_ = project

	dr, err := h.iam.GetRole(r.Context(), roleID)
	if err != nil {
		writeCErr(w, err)
		return
	}

	props, perr := decodeRoleProps(dr.AssumeRolePolicyDoc)
	if perr != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL",
			"could not decode stored role props: "+perr.Error())
		return
	}

	if err := h.iam.DeleteRole(r.Context(), roleID); err != nil {
		writeCErr(w, err)
		return
	}

	// GCP marks the role as deleted in the echoed body.
	out := toRoleJSON(project, roleID, &props)
	out.Deleted = true
	writeJSON(w, out)
}

func (h *Handler) updateRole(w http.ResponseWriter, r *http.Request, project, roleID string) {
	var in role
	if !decodeJSONBody(w, r, &in) {
		return
	}

	props := roleProps{
		Title:               in.Title,
		Description:         in.Description,
		IncludedPermissions: in.IncludedPermissions,
		Stage:               in.Stage,
	}

	propsJSON, err := json.Marshal(props)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL",
			"could not encode role props: "+err.Error())
		return
	}

	if err := h.iam.DeleteRole(r.Context(), roleID); err != nil {
		writeCErr(w, err)
		return
	}

	if _, err := h.iam.CreateRole(r.Context(), iamdriver.RoleConfig{
		Name:                roleID,
		Path:                project,
		AssumeRolePolicyDoc: string(propsJSON),
	}); err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toRoleJSON(project, roleID, &props))
}

// --- Service Account Keys ---

func (h *Handler) createKey(w http.ResponseWriter, r *http.Request, project, email string) {
	// SDK sometimes sends an empty body, sometimes a body with key algorithm
	// hints we don't honor — accept either.
	_ = drainBody(r)

	k, err := h.iam.CreateAccessKey(r.Context(), iamdriver.AccessKeyConfig{
		UserName: email,
	})
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toKeyJSON(project, email, k.AccessKeyID, k.SecretAccessKey))
}

func (h *Handler) getKey(w http.ResponseWriter, r *http.Request, project, email, keyID string) {
	keys, err := h.iam.ListAccessKeys(r.Context(), email)
	if err != nil {
		writeCErr(w, err)
		return
	}

	for i := range keys {
		if keys[i].AccessKeyID == keyID {
			// Empty private-key body on GET — GCP only returns the private
			// material once at create time.
			writeJSON(w, toKeyJSON(project, email, keyID, ""))
			return
		}
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND",
		"service account key "+keyID+" not found")
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, project, email string) {
	keys, err := h.iam.ListAccessKeys(r.Context(), email)
	if err != nil {
		writeCErr(w, err)
		return
	}

	out := listKeysResponse{Keys: make([]serviceAccountKey, 0, len(keys))}
	for i := range keys {
		out.Keys = append(out.Keys, toKeyJSON(project, email, keys[i].AccessKeyID, ""))
	}

	writeJSON(w, out)
}

func (h *Handler) deleteKey(w http.ResponseWriter, r *http.Request, project, email, keyID string) {
	_ = project

	if err := h.iam.DeleteAccessKey(r.Context(), email, keyID); err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, struct{}{})
}

// --- helpers ---

// buildSAEmail constructs a GCP-shaped service account email. Real GCP
// derives the domain from the project ID; we follow the same convention so
// returned values look like the real thing.
func buildSAEmail(accountID, project string) string {
	return accountID + "@" + project + ".iam.gserviceaccount.com"
}

// saFromUser reconstructs the wire-shape ServiceAccount from a driver User.
// DisplayName + Description come back via the tags we stashed at create.
func saFromUser(u *iamdriver.UserInfo) serviceAccount {
	out := serviceAccount{}

	if u.Tags != nil {
		out.DisplayName = u.Tags["displayName"]
		out.Description = u.Tags["description"]
	}

	return out
}

// toServiceAccountJSON fills the wire envelope with derived fields the
// driver doesn't carry. The "name" field is always the resource path; email
// is the same as the URL segment.
func toServiceAccountJSON(project, email string, sa *serviceAccount) serviceAccount {
	out := *sa
	out.Name = "projects/" + project + "/serviceAccounts/" + email
	out.ProjectID = project
	out.Email = email

	if out.UniqueID == "" {
		// Stable synthetic ID derived from the email so tests can match it.
		out.UniqueID = "uid-" + strings.ReplaceAll(email, "@", "-")
	}

	return out
}

// toRoleJSON builds the wire envelope for a custom role. The "name" field
// is the canonical resource path.
func toRoleJSON(project, roleID string, props *roleProps) role {
	return role{
		Name:                "projects/" + project + "/roles/" + roleID,
		Title:               props.Title,
		Description:         props.Description,
		IncludedPermissions: props.IncludedPermissions,
		Stage:               props.Stage,
	}
}

// toKeyJSON builds the wire envelope for a service-account key. private is
// only populated on create (GCP returns the private key material exactly
// once); GET / LIST pass an empty string.
func toKeyJSON(project, email, keyID, private string) serviceAccountKey {
	return serviceAccountKey{
		Name: "projects/" + project + "/serviceAccounts/" + email +
			"/keys/" + keyID,
		PrivateKeyType: "TYPE_GOOGLE_CREDENTIALS_FILE",
		KeyAlgorithm:   "KEY_ALG_RSA_2048",
		PrivateKeyData: private,
		KeyOrigin:      "GOOGLE_PROVIDED",
		KeyType:        "USER_MANAGED",
	}
}

func decodeRoleProps(doc string) (roleProps, error) {
	if doc == "" {
		return roleProps{}, nil
	}

	var props roleProps
	if err := json.Unmarshal([]byte(doc), &props); err != nil {
		return roleProps{}, err
	}

	return props, nil
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer func() { _ = r.Body.Close() }()

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
			"could not read request body: "+err.Error())
		return false
	}

	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
			"empty request body")
		return false
	}

	if err := json.Unmarshal(raw, v); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
			"could not parse JSON body: "+err.Error())
		return false
	}

	return true
}

// drainBody reads and discards the body so the connection isn't left in
// a half-read state. Used by endpoints that don't need request data.
func drainBody(r *http.Request) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	defer func() { _ = r.Body.Close() }()

	_, err := io.Copy(io.Discard, r.Body)

	return err
}

// writeCErr maps canonical cloudemu errors to GCP JSON error envelopes.
func writeCErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
