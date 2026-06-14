// Package queryhistory implements the Databricks SQL query-history data-plane
// REST API (the /api/2.0/sql/history/queries surface) as a server.Handler.
//
// The in-memory backend does not execute SQL, so there is no query activity to
// record — the endpoint returns an empty result set. That is enough for the
// real github.com/databricks/databricks-sdk-go WorkspaceClient's
// w.QueryHistory.List to succeed (it previously failed with "no handler
// registered for this request"), which unblocks tools that probe query history
// for idle/orphaned-warehouse detection.
//
// Covered endpoint:
//
//	GET /api/2.0/sql/history/queries
package queryhistory

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Path segment positions after splitting on "/": [api, ver, sql, history, queries].
const (
	idxAPI     = 0
	idxSQL     = 2
	idxHistory = 3
	idxQueries = 4
	minSegs    = 5
)

// Handler serves the Databricks SQL query-history endpoint.
type Handler struct{}

// New returns a query-history handler.
func New() *Handler { return &Handler{} }

// Matches claims exactly /api/{ver}/sql/history/queries. Requiring the exact
// segment count keeps any future sub-resource (e.g. .../queries/{id}) from
// being swallowed and served an empty list instead of a 501.
func (*Handler) Matches(r *http.Request) bool {
	parts := splitPath(r.URL.Path)
	if len(parts) != minSegs || parts[idxAPI] != "api" {
		return false
	}

	return parts[idxSQL] == "sql" && parts[idxHistory] == "history" && parts[idxQueries] == "queries"
}

// listResponse mirrors sql.ListQueriesResponse.
type listResponse struct {
	Res           []any  `json:"res"`
	HasNextPage   bool   `json:"has_next_page"`
	NextPageToken string `json:"next_page_token,omitempty"`
}

// ServeHTTP returns an empty query-history page. Real Databricks records
// executed queries; the in-memory backend runs none, so the list is empty.
func (*Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "INVALID_PARAMETER_VALUE", "method not allowed")

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(listResponse{Res: []any{}, HasNextPage: false})
}

func splitPath(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "/")
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
