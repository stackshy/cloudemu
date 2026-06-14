// Package gitcredentials emulates the Databricks Git Credentials data-plane
// REST API (the /api/2.0/git-credentials surface served at the workspace URL)
// as a server.Handler. Point the real
// github.com/databricks/databricks-sdk-go WorkspaceClient at a server
// registered with this handler and w.GitCredentials Create/Get/List/Update/
// Delete work end-to-end against an in-memory backend.
//
// Covered endpoints:
//
//	POST   /api/2.0/git-credentials
//	GET    /api/2.0/git-credentials
//	GET    /api/2.0/git-credentials/{credential_id}
//	PATCH  /api/2.0/git-credentials/{credential_id}
//	DELETE /api/2.0/git-credentials/{credential_id}
package gitcredentials

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const maxBodyBytes = 1 << 20

// resourceSegment is the path segment this handler claims.
const resourceSegment = "git-credentials"

// minResourceSegs is the [api, ver, git-credentials] segment count; a
// credential id, when present, is the 4th segment.
const (
	minResourceSegs = 3
	idSegIndex      = 3
	withIDSegs      = 4
)

const intBase = 10

// credential is the in-memory record for a stored Git credential.
type credential struct {
	id          int64
	gitProvider string
	gitUsername string
	token       string
}

// Handler serves the Databricks Git Credentials data-plane REST API.
type Handler struct {
	mu     sync.RWMutex
	store  map[int64]*credential
	nextID int64
}

// New returns a Git Credentials handler with an empty in-memory store.
func New() *Handler {
	return &Handler{
		store:  make(map[int64]*credential),
		nextID: 1,
	}
}

// Matches claims /api/{ver}/git-credentials[/...] paths.
func (*Handler) Matches(r *http.Request) bool {
	parts := splitPath(r.URL.Path)

	return len(parts) >= minResourceSegs && parts[0] == "api" && parts[2] == resourceSegment
}

// ServeHTTP routes by HTTP method and the presence of a {credential_id} segment.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	if len(parts) < minResourceSegs {
		writeErr(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "unsupported path")

		return
	}

	if len(parts) >= withIDSegs {
		h.serveByID(w, r, parts[idSegIndex])

		return
	}

	h.serveCollection(w, r)
}

// serveCollection handles the id-less endpoints: POST (create) and GET (list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.create(w, r)
	case http.MethodGet:
		h.list(w)
	default:
		methodNotAllowed(w)
	}
}

// serveByID handles the {credential_id} endpoints: GET, PATCH, DELETE.
func (h *Handler) serveByID(w http.ResponseWriter, r *http.Request, idSeg string) {
	id, err := strconv.ParseInt(idSeg, intBase, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "invalid credential_id: "+idSeg)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.get(w, id)
	case http.MethodPatch:
		h.update(w, r, id)
	case http.MethodDelete:
		h.delete(w, id)
	default:
		methodNotAllowed(w)
	}
}

// createRequest is the Create/Update request body shape.
type createRequest struct {
	GitProvider         string `json:"git_provider"`
	GitUsername         string `json:"git_username,omitempty"`
	PersonalAccessToken string `json:"personal_access_token,omitempty"`
}

// credentialResponse is the public view of a credential (token omitted).
type credentialResponse struct {
	CredentialID int64  `json:"credential_id"`
	GitProvider  string `json:"git_provider"`
	GitUsername  string `json:"git_username,omitempty"`
}

// listResponse is the List endpoint body shape.
type listResponse struct {
	Credentials []credentialResponse `json:"credentials,omitempty"`
}

func toResponse(c *credential) credentialResponse {
	return credentialResponse{
		CredentialID: c.id,
		GitProvider:  c.gitProvider,
		GitUsername:  c.gitUsername,
	}
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !decode(w, r, &req) {
		return
	}

	if req.GitProvider == "" {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "git_provider is required")

		return
	}

	h.mu.Lock()
	c := &credential{
		id:          h.nextID,
		gitProvider: req.GitProvider,
		gitUsername: req.GitUsername,
		token:       req.PersonalAccessToken,
	}
	h.store[c.id] = c
	h.nextID++
	h.mu.Unlock()

	writeJSON(w, toResponse(c))
}

func (h *Handler) get(w http.ResponseWriter, id int64) {
	h.mu.RLock()
	c, ok := h.store[id]
	h.mu.RUnlock()

	if !ok {
		writeNotFound(w, id)

		return
	}

	writeJSON(w, toResponse(c))
}

func (h *Handler) list(w http.ResponseWriter) {
	h.mu.RLock()
	out := make([]credentialResponse, 0, len(h.store))

	for _, c := range h.store {
		out = append(out, toResponse(c))
	}
	h.mu.RUnlock()

	writeJSON(w, listResponse{Credentials: out})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request, id int64) {
	var req createRequest
	if !decode(w, r, &req) {
		return
	}

	if req.GitProvider == "" {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "git_provider is required")

		return
	}

	h.mu.Lock()
	c, ok := h.store[id]

	if ok {
		c.gitProvider = req.GitProvider
		c.gitUsername = req.GitUsername

		if req.PersonalAccessToken != "" {
			c.token = req.PersonalAccessToken
		}
	}
	h.mu.Unlock()

	if !ok {
		writeNotFound(w, id)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) delete(w http.ResponseWriter, id int64) {
	h.mu.Lock()
	_, ok := h.store[id]

	if ok {
		delete(h.store, id)
	}
	h.mu.Unlock()

	if !ok {
		writeNotFound(w, id)

		return
	}

	writeJSON(w, struct{}{})
}

// splitPath strips surrounding slashes and returns the path segments. Path
// /api/2.0/git-credentials/5 → ["api","2.0","git-credentials","5"].
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
		writeErr(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "invalid JSON: "+err.Error())

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

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{ErrorCode: code, Message: msg})
}

func writeNotFound(w http.ResponseWriter, id int64) {
	writeErr(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST",
		"git credential does not exist: "+strconv.FormatInt(id, intBase))
}

func methodNotAllowed(w http.ResponseWriter) {
	writeErr(w, http.StatusMethodNotAllowed, "INVALID_PARAMETER_VALUE", "method not allowed")
}
