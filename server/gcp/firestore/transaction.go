package firestore

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
)

// transactionIDBytes is the length of the random token beginTransaction mints.
const transactionIDBytes = 16

// beginTransactionResponse mirrors google.firestore.v1.BeginTransactionResponse:
// the transaction id is a base64-encoded bytes token.
type beginTransactionResponse struct {
	Transaction string `json:"transaction"`
}

// beginTransaction handles POST .../documents:beginTransaction. The in-memory
// store has no MVCC, so the returned token is an opaque handle the client
// threads through its reads and the final :commit; commit applies the writes
// directly. This is enough for the SDK's RunTransaction to work end-to-end.
func (*Handler) beginTransaction(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, beginTransactionResponse{Transaction: newTransactionID()})
}

// rollback handles POST .../documents:rollback. With no pending transactional
// state to discard it simply acknowledges with an empty body, as the real API
// does.
func (*Handler) rollback(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{})
}

// newTransactionID returns a fresh base64-encoded token. A timestamp-seeded
// fallback keeps it non-empty even if the entropy source is unavailable.
func newTransactionID() string {
	buf := make([]byte, transactionIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return base64.StdEncoding.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}

	return base64.StdEncoding.EncodeToString(buf)
}

// listCollectionIDsRequest mirrors the paging fields of ListCollectionIdsRequest.
type listCollectionIDsRequest struct {
	PageSize  int    `json:"pageSize"`
	PageToken string `json:"pageToken"`
}

// listCollectionIDsResponse mirrors ListCollectionIdsResponse.
type listCollectionIDsResponse struct {
	CollectionIDs []string `json:"collectionIds"`
	NextPageToken string   `json:"nextPageToken,omitempty"`
}

// listCollectionIDs handles POST .../documents:listCollectionIds (and the
// per-document variant). Collections are created lazily on first write and
// modeled as driver tables, so the collection ids are the table names. The body
// (pageSize/pageToken) is optional; an absent body lists all ids.
func (h *Handler) listCollectionIDs(w http.ResponseWriter, r *http.Request) {
	var req listCollectionIDsRequest

	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}

	names, err := h.db.ListTables(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	sort.Strings(names)

	page, perr := pagination.Paginate(names, req.PageToken, req.PageSize)
	if perr != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid pageToken")
		return
	}

	writeJSON(w, http.StatusOK, listCollectionIDsResponse{
		CollectionIDs: page.Items,
		NextPageToken: page.NextPageToken,
	})
}
