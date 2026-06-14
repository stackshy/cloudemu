// Package repos implements the Databricks Repos (Git folders) data-plane REST
// API (the /api/2.0/repos surface served at the workspace URL) as a
// server.Handler. Point the real github.com/databricks/databricks-sdk-go
// WorkspaceClient at a server registered with this handler and w.Repos
// Create/Get/List/Update/Delete work end-to-end against an in-memory backend.
//
// Covered endpoints:
//
//	POST   /api/2.0/repos              create
//	GET    /api/2.0/repos             list
//	GET    /api/2.0/repos/{repo_id}    get
//	PATCH  /api/2.0/repos/{repo_id}    update
//	DELETE /api/2.0/repos/{repo_id}    delete
package repos

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const (
	maxBodyBytes = 5 << 20
	resRepos     = "repos"
	// reposSegs is the [api, ver, repos] segment count; an item path adds
	// the {repo_id} segment for itemSegs.
	reposSegs = 3
	itemSegs  = 4
	// defaultBranch and stubCommit are the values reported for a freshly
	// created Git folder.
	defaultBranch = "main"
	stubCommit    = "0000000000000000000000000000000000000000"
)

// repo is the in-memory state for a single Git folder (repo).
type repo struct {
	id       int64
	url      string
	provider string
	path     string
	branch   string
	tag      string
	headSHA  string
}

// Handler serves the Databricks Repos data-plane REST API backed by an
// in-memory map keyed by repo id.
type Handler struct {
	mu     sync.RWMutex
	repos  map[int64]*repo
	nextID int64
}

// New returns a Repos handler with an empty in-memory backend.
func New() *Handler {
	return &Handler{
		repos:  make(map[int64]*repo),
		nextID: 1,
	}
}

// Matches claims /api/{ver}/repos and /api/{ver}/repos/{repo_id} paths.
func (*Handler) Matches(r *http.Request) bool {
	parts := split(r.URL.Path)

	return len(parts) >= reposSegs && parts[0] == "api" && parts[2] == resRepos
}

// ServeHTTP routes by method and by the presence of a {repo_id} segment.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := split(r.URL.Path)
	if len(parts) >= itemSegs {
		h.serveItem(w, r, parts[3])

		return
	}

	h.serveCollection(w, r)
}

// serveCollection handles the /api/2.0/repos collection (create and list).
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

// serveItem handles /api/2.0/repos/{repo_id} (get, update, delete).
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, idSeg string) {
	id, err := strconv.ParseInt(idSeg, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "invalid repo_id: "+idSeg)

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

// createRequest is the POST /repos body.
type createRequest struct {
	URL      string `json:"url"`
	Provider string `json:"provider"`
	Path     string `json:"path"`
}

// repoView is the JSON shape returned for a single repo.
type repoView struct {
	ID           int64  `json:"id"`
	URL          string `json:"url"`
	Provider     string `json:"provider"`
	Path         string `json:"path"`
	Branch       string `json:"branch"`
	Tag          string `json:"tag,omitempty"`
	HeadCommitID string `json:"head_commit_id"`
}

// listResponse is the GET /repos body.
type listResponse struct {
	Repos         []repoView `json:"repos"`
	NextPageToken string     `json:"next_page_token,omitempty"`
}

// updateRequest is the PATCH /repos/{repo_id} body.
type updateRequest struct {
	Branch string `json:"branch"`
	Tag    string `json:"tag"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !decode(w, r, &req) {
		return
	}

	if req.URL == "" {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "url is required")

		return
	}

	if req.Provider == "" {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "provider is required")

		return
	}

	h.mu.Lock()
	id := h.nextID
	h.nextID++
	rp := &repo{
		id:       id,
		url:      req.URL,
		provider: req.Provider,
		path:     defaultPath(req.Path, req.URL, id),
		branch:   defaultBranch,
		headSHA:  stubCommit,
	}
	h.repos[id] = rp
	h.mu.Unlock()

	writeJSON(w, view(rp))
}

func (h *Handler) get(w http.ResponseWriter, id int64) {
	h.mu.RLock()
	rp, ok := h.repos[id]
	h.mu.RUnlock()

	if !ok {
		notFound(w, id)

		return
	}

	writeJSON(w, view(rp))
}

func (h *Handler) list(w http.ResponseWriter) {
	h.mu.RLock()

	views := make([]repoView, 0, len(h.repos))
	for _, rp := range h.repos {
		views = append(views, view(rp))
	}
	h.mu.RUnlock()

	writeJSON(w, listResponse{Repos: views})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request, id int64) {
	var req updateRequest
	if !decode(w, r, &req) {
		return
	}

	h.mu.Lock()

	rp, ok := h.repos[id]
	if !ok {
		h.mu.Unlock()
		notFound(w, id)

		return
	}

	// branch and tag are mutually exclusive checkout refs: setting one detaches
	// from the other (a tag checkout clears the branch, and vice versa).
	if req.Branch != "" {
		rp.branch = req.Branch
		rp.tag = ""
	}

	if req.Tag != "" {
		rp.tag = req.Tag
		rp.branch = ""
	}

	out := view(rp)
	h.mu.Unlock()

	writeJSON(w, out)
}

func (h *Handler) delete(w http.ResponseWriter, id int64) {
	h.mu.Lock()

	_, ok := h.repos[id]
	if ok {
		delete(h.repos, id)
	}

	h.mu.Unlock()

	if !ok {
		notFound(w, id)

		return
	}

	writeJSON(w, struct{}{})
}

// defaultPath derives a workspace path for a repo when none was supplied,
// using the trailing path segment of the Git URL or the repo id as a fallback.
func defaultPath(path, url string, id int64) string {
	if path != "" {
		return path
	}

	name := strings.TrimSuffix(lastSegment(url), ".git")
	if name == "" {
		name = "repo-" + strconv.FormatInt(id, 10)
	}

	return "/Repos/cloudemu/" + name
}

func lastSegment(url string) string {
	trimmed := strings.TrimRight(url, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		return trimmed[idx+1:]
	}

	return trimmed
}

func view(rp *repo) repoView {
	return repoView{
		ID:           rp.id,
		URL:          rp.url,
		Provider:     rp.provider,
		Path:         rp.path,
		Branch:       rp.branch,
		Tag:          rp.tag,
		HeadCommitID: rp.headSHA,
	}
}

// split strips the leading "/api/" prefix slashes and returns the path
// segments, keeping "api" at index 0 for the Matches/ServeHTTP guards.
func split(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "/")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "MALFORMED_REQUEST", "invalid JSON: "+err.Error())

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

func notFound(w http.ResponseWriter, id int64) {
	writeErr(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST",
		"repo "+strconv.FormatInt(id, 10)+" does not exist")
}

func methodNotAllowed(w http.ResponseWriter) {
	writeErr(w, http.StatusMethodNotAllowed, "INVALID_PARAMETER_VALUE", "method not allowed")
}
