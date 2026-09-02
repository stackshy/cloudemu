package firestore

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sort"
	"strings"
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

// listCollectionIDs handles POST .../documents:listCollectionIds (root) and the
// per-document variant .../documents/{coll}/{doc}:listCollectionIds. Collections
// are created lazily on first write and modeled as driver tables keyed by their
// full parent path, so the ids returned must be scoped to the request's parent:
// the root call returns only immediate top-level collections and a per-document
// call returns only that document's direct subcollections — each a single id
// segment, never a full nested path. base is the resource path before the
// ":listCollectionIds" action. The body (pageSize/pageToken) is optional; an
// absent body lists all matching ids.
func (h *Handler) listCollectionIDs(w http.ResponseWriter, r *http.Request, base string) {
	var req listCollectionIDsRequest

	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}

	parent, perr := parseFirestorePath(base)
	if perr != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", perr.Error())
		return
	}

	names, err := h.db.ListTables(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	ids := immediateCollectionIDs(names, parent.namespacePrefix(), parent.parentPath())
	sort.Strings(ids)

	page, pgerr := pagination.Paginate(ids, req.PageToken, req.PageSize)
	if pgerr != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid pageToken")
		return
	}

	writeJSON(w, http.StatusOK, listCollectionIDsResponse{
		CollectionIDs: page.Items,
		NextPageToken: page.NextPageToken,
	})
}

// immediateCollectionIDs filters the driver table names (each a namespaced key
// "{project}\x00{database}\x00{parentPath}" where parentPath is a full path like
// "cities" or "cities/SF/landmarks") to the collection ids that are immediate
// children of parent within the given project/database namespace, returning only
// each match's final path segment. Tables outside nsPrefix (a different project
// or database) are skipped, so a query never leaks another tenant's collections.
// A parent of "" selects the namespace's top-level collections.
func immediateCollectionIDs(tables []string, nsPrefix, parent string) []string {
	seen := make(map[string]struct{}, len(tables))

	ids := make([]string, 0, len(tables))

	for _, t := range tables {
		if !strings.HasPrefix(t, nsPrefix) {
			continue
		}

		rest := t[len(nsPrefix):]

		if parent != "" {
			prefix := parent + "/"
			if !strings.HasPrefix(rest, prefix) {
				continue
			}

			rest = rest[len(prefix):]
		}

		// An immediate child collection is a single segment under the parent;
		// skip a deeper subcollection path (it has another "/") or an empty rest.
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}

		if _, ok := seen[rest]; ok {
			continue
		}

		seen[rest] = struct{}{}

		ids = append(ids, rest)
	}

	return ids
}
