// Package secrets emulates the Databricks Secrets data-plane REST API
// (the /api/2.0/secrets/... surface served at the workspace URL) as a
// server.Handler. Point the real github.com/databricks/databricks-sdk-go
// WorkspaceClient at a server registered with this handler and the
// w.Secrets.* operations work end-to-end against an in-memory backend.
//
// Covered endpoints:
//
//	POST     /api/2.0/secrets/scopes/{create,delete}
//	GET      /api/2.0/secrets/scopes/list
//	POST     /api/2.0/secrets/{put,delete}
//	GET      /api/2.0/secrets/{get,list}
//	POST     /api/2.0/secrets/acls/{put,delete}
//	GET      /api/2.0/secrets/acls/{get,list}
package secrets

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxBodyBytes = 5 << 20

// minSegs is the [api, ver, secrets, action] segment count; nested
// scopes/acls paths add a fifth action segment.
const (
	minSegs    = 4
	nestedSegs = 5
)

// resource / sub-resource path segments.
const (
	resSecrets = "secrets"
	subScopes  = "scopes"
	subAcls    = "acls"
)

// action path segments.
const (
	actCreate = "create"
	actDelete = "delete"
	actList   = "list"
	actPut    = "put"
	actGet    = "get"
)

// Databricks error codes.
const (
	codeNotFound      = "RESOURCE_DOES_NOT_EXIST"
	codeAlreadyExists = "RESOURCE_ALREADY_EXISTS"
	codeInvalidParam  = "INVALID_PARAMETER_VALUE"
	codeMalformed     = "MALFORMED_REQUEST"
	codeNotAllowed    = "INVALID_PARAMETER_VALUE"
	codeNotFoundPath  = "ENDPOINT_NOT_FOUND"
)

// scopeBackendDatabricks is the default backend type for a created scope.
const scopeBackendDatabricks = "DATABRICKS"

// scope holds the secrets and ACLs for a single secret scope.
type scope struct {
	backendType string
	secrets     map[string]secretEntry
	acls        map[string]string
}

// secretEntry is a stored secret value plus its last-updated timestamp.
type secretEntry struct {
	value       string
	lastUpdated int64
}

// Handler serves the Databricks Secrets data-plane REST API from in-memory
// state. It is safe for concurrent use.
type Handler struct {
	mu     sync.RWMutex
	scopes map[string]*scope
}

// New returns a ready-to-use Secrets handler with empty state.
func New() *Handler {
	return &Handler{scopes: make(map[string]*scope)}
}

// route binds an action to its required HTTP method and handler.
type route struct {
	method string
	fn     func(http.ResponseWriter, *http.Request)
}

// Matches claims /api/{ver}/secrets/... paths.
func (*Handler) Matches(r *http.Request) bool {
	parts := splitPath(r.URL.Path)

	return len(parts) >= minSegs && parts[0] == "api" && parts[2] == resSecrets
}

// ServeHTTP routes by sub-resource and action.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	if len(parts) < minSegs {
		writeError(w, http.StatusNotFound, codeNotFoundPath, "unsupported path")

		return
	}

	switch parts[3] {
	case subScopes:
		serveNested(w, r, parts, h.scopeRoutes())
	case subAcls:
		serveNested(w, r, parts, h.aclRoutes())
	default:
		dispatch(w, r, parts[3], h.secretRoutes())
	}
}

func (h *Handler) scopeRoutes() map[string]route {
	return map[string]route{
		actCreate: {http.MethodPost, h.createScope},
		actDelete: {http.MethodPost, h.deleteScope},
		actList:   {http.MethodGet, h.listScopes},
	}
}

func (h *Handler) secretRoutes() map[string]route {
	return map[string]route{
		actPut:    {http.MethodPost, h.putSecret},
		actDelete: {http.MethodPost, h.deleteSecret},
		actList:   {http.MethodGet, h.listSecrets},
		actGet:    {http.MethodGet, h.getSecret},
	}
}

func (h *Handler) aclRoutes() map[string]route {
	return map[string]route{
		actPut:    {http.MethodPost, h.putACL},
		actDelete: {http.MethodPost, h.deleteACL},
		actGet:    {http.MethodGet, h.getACL},
		actList:   {http.MethodGet, h.listACLs},
	}
}

// serveNested routes nested /secrets/{scopes,acls}/{action} paths.
func serveNested(w http.ResponseWriter, r *http.Request, parts []string, routes map[string]route) {
	if len(parts) < nestedSegs {
		writeError(w, http.StatusNotFound, codeNotFoundPath, "unsupported path")

		return
	}

	dispatch(w, r, parts[4], routes)
}

// dispatch looks up action, enforces the method, and invokes the handler.
func dispatch(w http.ResponseWriter, r *http.Request, action string, routes map[string]route) {
	rt, ok := routes[action]
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFoundPath, "unknown action: "+action)

		return
	}

	if r.Method != rt.method {
		writeError(w, http.StatusMethodNotAllowed, codeNotAllowed, "method not allowed")

		return
	}

	rt.fn(w, r)
}

// requireScope returns the named scope, writing a RESOURCE_DOES_NOT_EXIST
// error and reporting false when it does not exist. Callers must hold the
// appropriate lock.
func (h *Handler) requireScope(w http.ResponseWriter, name string) (*scope, bool) {
	sc, ok := h.scopes[name]
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "no such scope: "+name)

		return nil, false
	}

	return sc, true
}

// deleteMember removes key from m within scopeName, writing a not-found error
// (using kind in the message) when either the scope or the key is absent.
func deleteMember[V any](w http.ResponseWriter, h *Handler, scopeName, key, kind string, pick func(*scope) map[string]V) {
	h.mu.Lock()
	defer h.mu.Unlock()

	sc, ok := h.requireScope(w, scopeName)
	if !ok {
		return
	}

	m := pick(sc)
	if _, ok = m[key]; !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "no such "+kind+": "+key)

		return
	}

	delete(m, key)
	writeJSON(w, struct{}{})
}

// --- scope ops ---

type createScopeRequest struct {
	Scope                  string `json:"scope"`
	InitialManagePrincipal string `json:"initial_manage_principal,omitempty"`
	ScopeBackendType       string `json:"scope_backend_type,omitempty"`
}

func (h *Handler) createScope(w http.ResponseWriter, r *http.Request) {
	var in createScopeRequest
	if !decode(w, r, &in) {
		return
	}

	if in.Scope == "" {
		writeError(w, http.StatusBadRequest, codeInvalidParam, "scope is required")

		return
	}

	backend := in.ScopeBackendType
	if backend == "" {
		backend = scopeBackendDatabricks
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.scopes[in.Scope]; ok {
		writeError(w, http.StatusConflict, codeAlreadyExists, "scope already exists: "+in.Scope)

		return
	}

	h.scopes[in.Scope] = &scope{
		backendType: backend,
		secrets:     make(map[string]secretEntry),
		acls:        make(map[string]string),
	}

	writeJSON(w, struct{}{})
}

type scopeRequest struct {
	Scope string `json:"scope"`
}

func (h *Handler) deleteScope(w http.ResponseWriter, r *http.Request) {
	var in scopeRequest
	if !decode(w, r, &in) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.scopes[in.Scope]; !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "no such scope: "+in.Scope)

		return
	}

	delete(h.scopes, in.Scope)
	writeJSON(w, struct{}{})
}

type secretScopeJSON struct {
	Name        string `json:"name"`
	BackendType string `json:"backend_type"`
}

type listScopesResponse struct {
	Scopes []secretScopeJSON `json:"scopes"`
}

func (h *Handler) listScopes(w http.ResponseWriter, _ *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]secretScopeJSON, 0, len(h.scopes))
	for name, sc := range h.scopes {
		out = append(out, secretScopeJSON{Name: name, BackendType: sc.backendType})
	}

	writeJSON(w, listScopesResponse{Scopes: out})
}

// --- secret ops ---

type putSecretRequest struct {
	Scope       string `json:"scope"`
	Key         string `json:"key"`
	StringValue string `json:"string_value"`
}

func (h *Handler) putSecret(w http.ResponseWriter, r *http.Request) {
	var in putSecretRequest
	if !decode(w, r, &in) {
		return
	}

	if in.Key == "" {
		writeError(w, http.StatusBadRequest, codeInvalidParam, "key is required")

		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	sc, ok := h.requireScope(w, in.Scope)
	if !ok {
		return
	}

	sc.secrets[in.Key] = secretEntry{value: in.StringValue, lastUpdated: time.Now().UnixMilli()}

	writeJSON(w, struct{}{})
}

type deleteSecretRequest struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
}

func (h *Handler) deleteSecret(w http.ResponseWriter, r *http.Request) {
	var in deleteSecretRequest
	if !decode(w, r, &in) {
		return
	}

	deleteMember(w, h, in.Scope, in.Key, "secret", func(sc *scope) map[string]secretEntry {
		return sc.secrets
	})
}

type secretMetadataJSON struct {
	Key                  string `json:"key"`
	LastUpdatedTimestamp int64  `json:"last_updated_timestamp"`
}

type listSecretsResponse struct {
	Secrets []secretMetadataJSON `json:"secrets"`
}

func (h *Handler) listSecrets(w http.ResponseWriter, r *http.Request) {
	scopeName := r.URL.Query().Get("scope")

	h.mu.RLock()
	defer h.mu.RUnlock()

	sc, ok := h.requireScope(w, scopeName)
	if !ok {
		return
	}

	out := make([]secretMetadataJSON, 0, len(sc.secrets))
	for key, entry := range sc.secrets {
		out = append(out, secretMetadataJSON{Key: key, LastUpdatedTimestamp: entry.lastUpdated})
	}

	writeJSON(w, listSecretsResponse{Secrets: out})
}

type getSecretResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (h *Handler) getSecret(w http.ResponseWriter, r *http.Request) {
	scopeName := r.URL.Query().Get("scope")
	key := r.URL.Query().Get("key")

	h.mu.RLock()
	defer h.mu.RUnlock()

	sc, ok := h.requireScope(w, scopeName)
	if !ok {
		return
	}

	entry, ok := sc.secrets[key]
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "no such secret: "+key)

		return
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(entry.value))
	writeJSON(w, getSecretResponse{Key: key, Value: encoded})
}

// --- acl ops ---

type putACLRequest struct {
	Scope      string `json:"scope"`
	Principal  string `json:"principal"`
	Permission string `json:"permission"`
}

func (h *Handler) putACL(w http.ResponseWriter, r *http.Request) {
	var in putACLRequest
	if !decode(w, r, &in) {
		return
	}

	if in.Principal == "" {
		writeError(w, http.StatusBadRequest, codeInvalidParam, "principal is required")

		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	sc, ok := h.requireScope(w, in.Scope)
	if !ok {
		return
	}

	sc.acls[in.Principal] = in.Permission

	writeJSON(w, struct{}{})
}

type deleteACLRequest struct {
	Scope     string `json:"scope"`
	Principal string `json:"principal"`
}

func (h *Handler) deleteACL(w http.ResponseWriter, r *http.Request) {
	var in deleteACLRequest
	if !decode(w, r, &in) {
		return
	}

	deleteMember(w, h, in.Scope, in.Principal, "acl", func(sc *scope) map[string]string {
		return sc.acls
	})
}

type aclItemJSON struct {
	Principal  string `json:"principal"`
	Permission string `json:"permission"`
}

func (h *Handler) getACL(w http.ResponseWriter, r *http.Request) {
	scopeName := r.URL.Query().Get("scope")
	principal := r.URL.Query().Get("principal")

	h.mu.RLock()
	defer h.mu.RUnlock()

	sc, ok := h.requireScope(w, scopeName)
	if !ok {
		return
	}

	perm, ok := sc.acls[principal]
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "no such acl: "+principal)

		return
	}

	writeJSON(w, aclItemJSON{Principal: principal, Permission: perm})
}

type listACLsResponse struct {
	Items []aclItemJSON `json:"items"`
}

func (h *Handler) listACLs(w http.ResponseWriter, r *http.Request) {
	scopeName := r.URL.Query().Get("scope")

	h.mu.RLock()
	defer h.mu.RUnlock()

	sc, ok := h.requireScope(w, scopeName)
	if !ok {
		return
	}

	out := make([]aclItemJSON, 0, len(sc.acls))
	for principal, perm := range sc.acls {
		out = append(out, aclItemJSON{Principal: principal, Permission: perm})
	}

	writeJSON(w, listACLsResponse{Items: out})
}

// --- helpers ---

// splitPath strips surrounding slashes and returns the path segments.
// Path /api/2.0/secrets/scopes/create → [api, 2.0, secrets, scopes, create].
func splitPath(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "/")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, codeMalformed, "invalid JSON: "+err.Error())

		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

// errorBody is the Databricks error envelope shape.
type errorBody struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{ErrorCode: code, Message: msg})
}
