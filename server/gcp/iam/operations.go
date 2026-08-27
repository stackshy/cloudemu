package iam

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const (
	maxBodyBytes = 1 << 20

	saUniqueIDDigits = 21 // real GCP service-account uniqueIds are 21-digit numerics
	keyValidityYears = 10 // user-managed key default validity window
	decimalBase      = 10 // base for the uniqueId digit-range math

	defaultPageSize = 100
	maxPageSize     = 1000
)

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

	h.mu.RLock()
	sa.Disabled = h.disabled[email]
	h.mu.RUnlock()

	writeJSON(w, toServiceAccountJSON(project, email, &sa))
}

// listServiceAccounts returns SAs at the given project. A literal "-" in the
// project segment is the GCP-wide wildcard meaning "every project the caller
// can see" — we treat it as match-all because there's no concept of caller
// identity in the emulator.
func (h *Handler) listServiceAccounts(w http.ResponseWriter, r *http.Request, project string) {
	users, err := h.iam.ListUsers(r.Context())
	if err != nil {
		writeCErr(w, err)
		return
	}

	wildcard := project == "-"
	matched := make([]serviceAccount, 0, len(users))

	h.mu.RLock()
	for i := range users {
		u := &users[i]
		if !wildcard && u.Path != project {
			continue
		}

		// When responding to a wildcard query, render each SA against its
		// own real project; the URL "-" never appears in the returned name.
		responseProject := project
		if wildcard {
			responseProject = u.Path
		}

		sa := saFromUser(u)
		sa.Disabled = h.disabled[u.Name] // reflect the disabled bit in list, like Get
		matched = append(matched, toServiceAccountJSON(responseProject, u.Name, &sa))
	}
	h.mu.RUnlock()

	// ListUsers returns map order (random), so sort by the stable resource name
	// before applying the offset page token — otherwise page boundaries shift
	// between requests and callers see duplicated or skipped accounts.
	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })

	page, next := paginate(matched, pageSizeParam(r), decodePageToken(pageTokenParam(r)))

	writeJSON(w, listServiceAccountsResponse{Accounts: page, NextPageToken: next})
}

func (h *Handler) deleteServiceAccount(w http.ResponseWriter, r *http.Request, email string) {
	// Capture a tombstone before the hard delete so a subsequent undelete can
	// restore the account (real GCP soft-deletes for ~30 days).
	user, gerr := h.iam.GetUser(r.Context(), email)

	if err := h.iam.DeleteUser(r.Context(), email); err != nil {
		writeCErr(w, err)
		return
	}

	if gerr == nil {
		sa := saFromUser(user)

		h.mu.Lock()
		h.deletedSA[email] = &deletedSA{
			project:     user.Path,
			displayName: sa.DisplayName,
			description: sa.Description,
			disabled:    h.disabled[email],
		}
		delete(h.disabled, email)
		delete(h.saPolicy, email)
		delete(h.policyVersion, email)
		h.mu.Unlock()
	}

	// GCP returns an empty body with 200 on successful SA delete.
	writeJSON(w, struct{}{})
}

// updateServiceAccount handles PATCH .../serviceAccounts/{email}. The GCP
// SDK wraps the payload as {"serviceAccount": {...}, "updateMask": "..."} —
// decoding into a bare serviceAccount silently loses every field, so we
// must decode the wrapper. updateMask itself is ignored; the emulator
// always full-replaces.
//
// When the URL uses the wildcard project segment "projects/-/...", project
// arrives as "-". We look up the existing SA first and reuse its stored
// project for the re-create so the SA doesn't move to a synthetic "-"
// project bucket that would later disappear from listServiceAccounts.
//
// The Delete+Create dance is non-atomic — a concurrent reader between the
// two driver calls observes NotFound. The driver lacks an Update entry
// point so this is the simplest workaround.
func (h *Handler) updateServiceAccount(w http.ResponseWriter, r *http.Request, project, email string) {
	var in patchServiceAccountRequest
	if !decodeJSONBody(w, r, &in) {
		return
	}

	storedProject := project
	if storedProject == "-" {
		existing, err := h.iam.GetUser(r.Context(), email)
		if err != nil {
			writeCErr(w, err)
			return
		}

		storedProject = existing.Path
	}

	if err := h.iam.DeleteUser(r.Context(), email); err != nil {
		writeCErr(w, err)
		return
	}

	if _, err := h.iam.CreateUser(r.Context(), iamdriver.UserConfig{
		Name: email,
		Path: storedProject,
		Tags: map[string]string{
			"displayName": in.ServiceAccount.DisplayName,
			"description": in.ServiceAccount.Description,
		},
	}); err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toServiceAccountJSON(storedProject, email, &in.ServiceAccount))
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

	matched := make([]role, 0, len(roles))

	for i := range roles {
		dr := &roles[i]
		if dr.Path != project {
			continue
		}

		// If the stored doc is malformed (e.g. a portable test stashed a
		// non-JSON value via the shared driver), emit the role with just
		// its name rather than silently dropping it from the list — the
		// underlying entry exists and the caller should see something.
		props, _ := decodeRoleProps(dr.AssumeRolePolicyDoc)
		matched = append(matched, toRoleJSON(project, dr.Name, &props))
	}

	// ListRoles returns map order (random), so sort by the stable role name
	// before applying the offset page token to keep page boundaries consistent
	// across requests (no duplicated or skipped roles).
	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })

	page, next := paginate(matched, pageSizeParam(r), decodePageToken(pageTokenParam(r)))

	writeJSON(w, listRolesResponse{Roles: page, NextPageToken: next})
}

func (h *Handler) deleteRole(w http.ResponseWriter, r *http.Request, project, roleID string) {
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

	// Tombstone the role so it can be undeleted (real GCP soft-deletes custom
	// roles for 7 days).
	h.mu.Lock()
	h.deletedRole[roleID] = &deletedRole{project: project, props: props}
	h.mu.Unlock()

	// GCP marks the role as deleted in the echoed body.
	out := toRoleJSON(project, roleID, &props)
	out.Deleted = true
	writeJSON(w, out)
}

// undeleteRole restores a soft-deleted custom role from its tombstone.
func (h *Handler) undeleteRole(w http.ResponseWriter, r *http.Request, project, roleID string) {
	h.mu.Lock()
	tomb := h.deletedRole[roleID]
	delete(h.deletedRole, roleID) // no-op when absent
	h.mu.Unlock()

	if tomb == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND",
			"role "+roleID+" was not found or is not recoverable")

		return
	}

	propsJSON, err := json.Marshal(tomb.props)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL",
			"could not encode role props: "+err.Error())
		return
	}

	if _, err := h.iam.CreateRole(r.Context(), iamdriver.RoleConfig{
		Name:                roleID,
		Path:                tomb.project,
		AssumeRolePolicyDoc: string(propsJSON),
	}); err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toRoleJSON(project, roleID, &tomb.props))
}

// updateRole handles PATCH .../roles/{roleId}. Unlike SA Patch the role
// payload is the bare resource body, with updateMask passed as a ?updateMask=
// query parameter — we ignore the mask (emulator always full-replaces).
//
// The Delete+Create dance is non-atomic — a concurrent reader between the
// two driver calls observes NotFound. The driver lacks an Update entry
// point so this is the simplest workaround.
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

	// privateKeyData is the base64 credentials file, returned once at create.
	keyFile := buildKeyFileData(project, email, k.AccessKeyID, k.SecretAccessKey)
	writeJSON(w, toKeyJSON(project, email, k.AccessKeyID, keyFile, k.CreatedAt))
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
			writeJSON(w, toKeyJSON(project, email, keyID, "", keys[i].CreatedAt))
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
		out.Keys = append(out.Keys,
			toKeyJSON(project, email, keys[i].AccessKeyID, "", keys[i].CreatedAt))
	}

	writeJSON(w, out)
}

func (h *Handler) deleteKey(w http.ResponseWriter, r *http.Request, email, keyID string) {
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
		out.UniqueID = saUniqueID(email)
	}

	// In real GCP the OAuth2 client id equals the numeric unique id, and every
	// SA carries a (deprecated but populated) etag.
	out.OAuth2ClientID = out.UniqueID
	out.Etag = saEtag(email)

	return out
}

// saUniqueID derives a stable 21-digit numeric unique id from the SA email,
// matching the shape of a real GCP service-account uniqueId (and oauth2ClientId).
func saUniqueID(email string) string {
	sum := sha256.Sum256([]byte("uid:" + email))
	n := new(big.Int).SetBytes(sum[:])

	base := big.NewInt(decimalBase)
	// 21-digit numbers span [10^20, 10^21); map the hash into that half-open range.
	lo := new(big.Int).Exp(base, big.NewInt(saUniqueIDDigits-1), nil)
	hi := new(big.Int).Exp(base, big.NewInt(saUniqueIDDigits), nil)
	span := new(big.Int).Sub(hi, lo)

	n.Mod(n, span)
	n.Add(n, lo)

	return n.String()
}

// saEtag returns a stable, populated etag for a service account resource.
func saEtag(email string) string {
	return base64.StdEncoding.EncodeToString([]byte("sa:" + email))
}

// roleEtag returns a stable, populated etag for a custom role resource.
func roleEtag(project, roleID string) string {
	return base64.StdEncoding.EncodeToString([]byte("role:" + project + "/" + roleID))
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
		Etag:                roleEtag(project, roleID),
	}
}

// toKeyJSON builds the wire envelope for a service-account key. private is
// only populated on create (GCP returns the private key material exactly
// once); GET / LIST pass an empty string. createdAt (RFC3339) drives the
// validity window; a zero value means "now".
func toKeyJSON(project, email, keyID, private, createdAt string) serviceAccountKey {
	validAfter := createdAt
	if validAfter == "" {
		validAfter = time.Now().UTC().Format(time.RFC3339)
	}

	validBefore := validAfter
	if t, err := time.Parse(time.RFC3339, validAfter); err == nil {
		validBefore = t.AddDate(keyValidityYears, 0, 0).UTC().Format(time.RFC3339)
	}

	return serviceAccountKey{
		Name: "projects/" + project + "/serviceAccounts/" + email +
			"/keys/" + keyID,
		PrivateKeyType:  "TYPE_GOOGLE_CREDENTIALS_FILE",
		KeyAlgorithm:    "KEY_ALG_RSA_2048",
		PrivateKeyData:  private,
		ValidAfterTime:  validAfter,
		ValidBeforeTime: validBefore,
		KeyOrigin:       "GOOGLE_PROVIDED",
		KeyType:         "USER_MANAGED",
	}
}

// buildKeyFileData returns the base64-encoded service-account credentials file
// (JSON) that real GCP hands back exactly once, at key-create time. The private
// key material is a synthetic placeholder — cloudemu never mints real RSA keys —
// but the envelope is the standard google-credentials shape clients parse.
func buildKeyFileData(project, email, keyID, secret string) string {
	// Synthetic PEM body — cloudemu never mints real RSA keys.
	pemBody := base64.StdEncoding.EncodeToString([]byte("cloudemu:" + secret))
	pem := "-----BEGIN PRIVATE KEY-----\n" + pemBody + "\n-----END PRIVATE KEY-----\n"

	keyFile := map[string]string{ //nolint:gosec // synthetic key file, no real credentials
		"type":                        "service_account",
		"project_id":                  project,
		"private_key_id":              keyID,
		"private_key":                 pem,
		"client_email":                email,
		"client_id":                   saUniqueID(email),
		"auth_uri":                    "https://accounts.google.com/o/oauth2/auth",
		"token_uri":                   "https://oauth2.googleapis.com/token",
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
		"client_x509_cert_url":        "https://www.googleapis.com/robot/v1/metadata/x509/" + url.PathEscape(email),
	}

	raw, err := json.Marshal(keyFile)
	if err != nil {
		return ""
	}

	return base64.StdEncoding.EncodeToString(raw)
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

// --- pagination ---

// paginate returns the slice window starting at offset (capped to pageSize)
// and the opaque nextPageToken for the following window, or "" when exhausted.
func paginate[T any](items []T, pageSize, offset int) (page []T, nextToken string) {
	if offset < 0 || offset >= len(items) {
		return []T{}, ""
	}

	end := offset + pageSize
	if end >= len(items) {
		return items[offset:], ""
	}

	return items[offset:end], encodePageToken(end)
}

// pageSizeParam reads ?pageSize, clamping to a sane default/max.
func pageSizeParam(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if err != nil || n <= 0 {
		return defaultPageSize
	}

	if n > maxPageSize {
		return maxPageSize
	}

	return n
}

func pageTokenParam(r *http.Request) string {
	return r.URL.Query().Get("pageToken")
}

// encodePageToken/decodePageToken carry a slice offset as an opaque base64 token.
func encodePageToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodePageToken(tok string) int {
	if tok == "" {
		return 0
	}

	b, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return 0
	}

	n, err := strconv.Atoi(string(b))
	if err != nil || n < 0 {
		return 0
	}

	return n
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
