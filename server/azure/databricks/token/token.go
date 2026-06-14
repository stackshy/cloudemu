// Package token implements the Databricks personal-access-token data-plane REST
// API (the /api/2.0/token surface served at the workspace URL) as a
// server.Handler. Point the real github.com/databricks/databricks-sdk-go
// WorkspaceClient at a server registered with this handler and w.Tokens.Create,
// w.Tokens.ListAll, and w.Tokens.Delete work end-to-end against an in-memory
// backend.
//
// Covered endpoints:
//
//	POST /api/2.0/token/create
//	GET  /api/2.0/token/list
//	POST /api/2.0/token/delete
package token

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxBodyBytes = 1 << 20

// Segment indices and counts for /api/{ver}/token/{action} paths.
const (
	segAPI      = 0
	segResource = 2
	segAction   = 3
	minSegs     = 4
)

const resToken = "token"

// Action path segments.
const (
	actCreate = "create"
	actList   = "list"
	actDelete = "delete"
)

// Databricks error codes.
const (
	codeNotFound  = "RESOURCE_DOES_NOT_EXIST"
	codeInvalid   = "INVALID_PARAMETER_VALUE"
	codeEndpoint  = "ENDPOINT_NOT_FOUND"
	codeMalformed = "MALFORMED_REQUEST"
)

const millisPerSecond = 1000

// tokenInfo is the stored metadata for a single personal access token.
type tokenInfo struct {
	tokenID      string
	comment      string
	creationTime int64
	expiryTime   int64
}

// Handler serves the Databricks token data-plane REST API backed by an
// in-memory store.
type Handler struct {
	mu     sync.RWMutex
	tokens map[string]tokenInfo
	nextID int64
}

// New returns a token handler with an empty in-memory store.
func New() *Handler {
	return &Handler{tokens: make(map[string]tokenInfo)}
}

// Matches claims /api/{ver}/token/... paths.
func (*Handler) Matches(r *http.Request) bool {
	parts := split(r.URL.Path)
	if len(parts) < minSegs || parts[segAPI] != "api" {
		return false
	}

	return parts[segResource] == resToken
}

// ServeHTTP routes by action.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := split(r.URL.Path)
	if len(parts) < minSegs {
		writeError(w, http.StatusNotFound, codeEndpoint, "unsupported path")

		return
	}

	switch action := parts[segAction]; action {
	case actCreate:
		h.create(w, r)
	case actList:
		h.list(w, r)
	case actDelete:
		h.deleteToken(w, r)
	default:
		writeError(w, http.StatusNotFound, codeEndpoint, "unknown action: "+action)
	}
}

// --- wire types ---

type tokenInfoJSON struct {
	TokenID      string `json:"token_id"`
	Comment      string `json:"comment,omitempty"`
	CreationTime int64  `json:"creation_time"`
	ExpiryTime   int64  `json:"expiry_time"`
}

type createRequest struct {
	Comment         string `json:"comment"`
	LifetimeSeconds int64  `json:"lifetime_seconds"`
}

type createResponse struct {
	TokenValue string        `json:"token_value"`
	TokenInfo  tokenInfoJSON `json:"token_info"`
}

type listResponse struct {
	TokenInfos []tokenInfoJSON `json:"token_infos"`
}

type deleteRequest struct {
	TokenID string `json:"token_id"`
}

// --- ops ---

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	var in createRequest
	if !decode(w, r, &in) {
		return
	}

	now := time.Now().UnixMilli()

	var expiry int64
	if in.LifetimeSeconds > 0 {
		expiry = now + in.LifetimeSeconds*millisPerSecond
	}

	h.mu.Lock()
	h.nextID++
	id := strconv.FormatInt(h.nextID, 10)
	info := tokenInfo{tokenID: id, comment: in.Comment, creationTime: now, expiryTime: expiry}
	h.tokens[id] = info
	h.mu.Unlock()

	writeJSON(w, createResponse{
		TokenValue: "dapi" + id,
		TokenInfo:  toJSON(info),
	})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	h.mu.RLock()
	out := make([]tokenInfoJSON, 0, len(h.tokens))

	for _, info := range h.tokens {
		out = append(out, toJSON(info))
	}
	h.mu.RUnlock()

	writeJSON(w, listResponse{TokenInfos: out})
}

func (h *Handler) deleteToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	var in deleteRequest
	if !decode(w, r, &in) {
		return
	}

	if in.TokenID == "" {
		writeError(w, http.StatusBadRequest, codeInvalid, "token_id is required")

		return
	}

	h.mu.Lock()

	_, ok := h.tokens[in.TokenID]
	if ok {
		delete(h.tokens, in.TokenID)
	}

	h.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "token "+in.TokenID+" does not exist")

		return
	}

	writeJSON(w, struct{}{})
}

// --- helpers ---

func toJSON(info tokenInfo) tokenInfoJSON {
	return tokenInfoJSON{
		TokenID:      info.tokenID,
		Comment:      info.comment,
		CreationTime: info.creationTime,
		ExpiryTime:   info.expiryTime,
	}
}

// split strips the leading/trailing slashes and returns the path segments.
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

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, codeInvalid, "method not allowed")
}
