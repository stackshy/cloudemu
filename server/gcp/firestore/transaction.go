package firestore

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sort"
	"strings"
	"sync"
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

// beginTransaction handles POST .../documents:beginTransaction. The returned
// token is an opaque handle the client threads through its reads (batchGet /
// runQuery) and the final :commit. commit uses the registry populated by those
// reads to enforce optimistic concurrency: see transactionRegistry.
func (h *Handler) beginTransaction(w http.ResponseWriter, _ *http.Request) {
	id := newTransactionID()
	h.txns.begin(id)
	writeJSON(w, http.StatusOK, beginTransactionResponse{Transaction: id})
}

// rollbackRequest mirrors the subset of google.firestore.v1.RollbackRequest we
// need: the transaction id whose read-set should be discarded.
type rollbackRequest struct {
	Transaction string `json:"transaction,omitempty"`
}

// rollback handles POST .../documents:rollback. There is no pending write
// state to discard (writes are only staged, never applied, until :commit), but
// the transaction's tracked read-set is discarded so it cannot leak.
func (h *Handler) rollback(w http.ResponseWriter, r *http.Request) {
	var req rollbackRequest

	if r.ContentLength != 0 {
		_ = decodeJSON(w, r, &req) // best-effort: an empty/malformed body is harmless
	}

	h.txns.end(req.Transaction)
	writeJSON(w, http.StatusOK, map[string]any{})
}

// txnTTL bounds how long an abandoned transaction's read-set is kept before
// being swept, so a client that begins a transaction but never commits or
// rolls back (crash, timeout, a RunTransaction retry that abandons a prior
// attempt's token) cannot leak memory indefinitely.
const txnTTL = 5 * time.Minute

// txnRead is a document's state as observed by a read within a transaction:
// whether it existed and, if so, its stored commit time. commit re-checks this
// against the document's current state (optimistic concurrency), matching real
// Firestore: a transaction that raced a conflicting write is aborted rather
// than silently applying a write computed from a stale read.
type txnRead struct {
	existed    bool
	updateTime time.Time
}

// txnState is one in-flight transaction's read-set plus a creation stamp for
// TTL sweeping.
type txnState struct {
	reads   map[string]txnRead // keyed by full document resource name
	started time.Time
}

// transactionRegistry tracks the documents read within each open transaction
// so commit can detect a conflicting write that landed after the read and
// abort — without this, concurrent read-modify-write transactions (e.g. two
// clients both incrementing a counter) silently lose updates instead of one
// being retried, since the in-memory store otherwise applies every commit's
// writes unconditionally.
type transactionRegistry struct {
	mu    sync.Mutex
	state map[string]*txnState
}

func newTransactionRegistry() *transactionRegistry {
	return &transactionRegistry{state: make(map[string]*txnState)}
}

// begin opens a fresh, empty read-set for id, sweeping any entries that have
// outlived txnTTL.
func (tr *transactionRegistry) begin(id string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	now := time.Now()

	for k, s := range tr.state {
		if now.Sub(s.started) > txnTTL {
			delete(tr.state, k)
		}
	}

	tr.state[id] = &txnState{reads: make(map[string]txnRead), started: now}
}

// recordRead notes that docName was observed (existed, updateTime) within
// transaction id. A blank id is a no-op (the read was not transactional). An id
// the registry has not seen — e.g. its begin() entry was swept, or a
// non-SDK caller skipped beginTransaction — gets a read-set lazily created so
// commit can still validate what it reads.
func (tr *transactionRegistry) recordRead(id, docName string, existed bool, updateTime time.Time) {
	if id == "" {
		return
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()

	s, ok := tr.state[id]
	if !ok {
		s = &txnState{reads: make(map[string]txnRead), started: time.Now()}
		tr.state[id] = s
	}

	s.reads[docName] = txnRead{existed: existed, updateTime: updateTime}
}

// reads returns a snapshot copy of id's recorded read-set (nil if id is blank
// or unknown to the registry).
func (tr *transactionRegistry) reads(id string) map[string]txnRead {
	if id == "" {
		return nil
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()

	s, ok := tr.state[id]
	if !ok {
		return nil
	}

	out := make(map[string]txnRead, len(s.reads))
	for k, v := range s.reads {
		out[k] = v
	}

	return out
}

// end discards id's read-set. Called once a transaction resolves — commit
// (successful or aborted) or rollback — so the registry never grows past the
// set of currently in-flight transactions (plus stragglers up to txnTTL).
func (tr *transactionRegistry) end(id string) {
	if id == "" {
		return
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()

	delete(tr.state, id)
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
