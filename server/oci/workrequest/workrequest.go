// Package workrequest provides OCI's asynchronous work request envelope.
//
// Real OCI returns 202 with an opc-work-request-id from most mutating calls,
// and SDK waiters poll GET /{version}/workRequests/{id} until the status is
// terminal. Each service publishes that endpoint under its own API version
// prefix; CloudEmu collapses every service onto one HTTP server, so this
// handler claims any path ending in workRequests and answers uniformly.
//
// Every CloudEmu mutation completes synchronously, so an accepted work request
// is already SUCCEEDED — the envelope exists to keep SDK waiters happy and to
// carry the created resource's OCID back to them.
package workrequest

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// Work request lifecycle states.
const (
	StatusAccepted   = "ACCEPTED"
	StatusInProgress = "IN_PROGRESS"
	StatusSucceeded  = "SUCCEEDED"
	StatusFailed     = "FAILED"
)

// Action types a work request reports against an affected resource.
const (
	ActionCreated    = "CREATED"
	ActionUpdated    = "UPDATED"
	ActionDeleted    = "DELETED"
	ActionInProgress = "IN_PROGRESS"
	ActionRelated    = "RELATED"
)

const segment = "workRequests"

// Resource is a resource a work request affected.
type Resource struct {
	EntityType string `json:"entityType"`
	ActionType string `json:"actionType"`
	Identifier string `json:"identifier"`
	EntityURI  string `json:"entityUri,omitempty"`
}

// WorkRequest is OCI's asynchronous operation record.
type WorkRequest struct {
	ID              string     `json:"id"`
	OperationType   string     `json:"operationType"`
	Status          string     `json:"status"`
	CompartmentID   string     `json:"compartmentId"`
	Resources       []Resource `json:"resources"`
	PercentComplete float32    `json:"percentComplete"`
	TimeAccepted    string     `json:"timeAccepted"`
	TimeStarted     string     `json:"timeStarted,omitempty"`
	TimeFinished    string     `json:"timeFinished,omitempty"`
}

// Store records work requests so SDK waiters can poll them.
type Store struct {
	requests *memstore.Store[*WorkRequest]
	opts     *config.Options
	mu       sync.RWMutex
	order    []string
}

// New creates a work request store.
func New(opts *config.Options) *Store {
	return &Store{
		requests: memstore.New[*WorkRequest](),
		opts:     opts,
	}
}

// Accept records a completed work request and returns its OCID, which the
// caller stamps as opc-work-request-id.
func (s *Store) Accept(operationType, compartmentID string, resources ...Resource) string {
	now := s.opts.Clock.Now().UTC().Format(time.RFC3339)
	id := idgen.OCID("workrequest", s.opts.Realm, s.opts.OCIRegion())

	wr := &WorkRequest{
		ID:              id,
		OperationType:   operationType,
		Status:          StatusSucceeded,
		CompartmentID:   compartmentID,
		Resources:       resources,
		PercentComplete: 100,
		TimeAccepted:    now,
		TimeStarted:     now,
		TimeFinished:    now,
	}

	s.requests.Set(id, wr)

	s.mu.Lock()
	s.order = append(s.order, id)
	s.mu.Unlock()

	return id
}

// Get returns a recorded work request.
func (s *Store) Get(id string) (*WorkRequest, bool) {
	return s.requests.Get(id)
}

// List returns work requests in the given compartment, in creation order. An
// empty compartment returns all of them.
func (s *Store) List(compartmentID string) []*WorkRequest {
	s.mu.RLock()
	ids := make([]string, len(s.order))
	copy(ids, s.order)
	s.mu.RUnlock()

	out := make([]*WorkRequest, 0, len(ids))

	for _, id := range ids {
		wr, ok := s.requests.Get(id)
		if !ok {
			continue
		}

		if compartmentID != "" && wr.CompartmentID != compartmentID {
			continue
		}

		out = append(out, wr)
	}

	return out
}

// Handler serves work request polls for every OCI service.
type Handler struct{ store *Store }

// NewHandler returns the work request handler backed by store.
func NewHandler(store *Store) *Handler { return &Handler{store: store} }

// Matches claims GET on any path under a workRequests segment, regardless of
// the service's API version prefix.
func (*Handler) Matches(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	_, _, ok := parse(r.URL.Path)

	return ok
}

// ServeHTTP answers a single work request, its sub-collections, or a list.
//
// The malformed-path branch is unreachable through server.Server, which calls
// Matches first, but ServeHTTP is exported and stays correct when mounted on a
// plain mux.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, sub, ok := parse(r.URL.Path)
	if !ok {
		ocirest.WriteError(w, r, http.StatusBadRequest, "InvalidParameter", "malformed work request path")
		return
	}

	if id == "" {
		// ListWorkRequests requires compartmentId in real OCI.
		compartmentID, given := ocirest.RequireCompartmentID(w, r)
		if !given {
			return
		}

		ocirest.WriteJSON(w, r, http.StatusOK, h.store.List(compartmentID))

		return
	}

	wr, found := h.store.Get(id)
	if !found {
		ocirest.WriteError(w, r, http.StatusNotFound, "NotAuthorizedOrNotFound", "work request "+id+" not found")
		return
	}

	// A synchronous mutation never fails partway, so errors and logs are
	// always empty; the sub-collections exist because SDK waiters read them
	// after a terminal status.
	switch sub {
	case "errors", "logs":
		ocirest.WriteJSON(w, r, http.StatusOK, []any{})
	default:
		ocirest.WriteJSON(w, r, http.StatusOK, wr)
	}
}

// parse splits /{version}/workRequests[/{id}[/{sub}]].
func parse(urlPath string) (id, sub string, ok bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")

	for i, p := range parts {
		if p != segment {
			continue
		}

		switch rest := parts[i+1:]; {
		case len(rest) == 0:
			return "", "", true
		case len(rest) == 1:
			return rest[0], "", true
		case len(rest) == 2: //nolint:mnd // an id plus one sub-collection segment
			return rest[0], rest[1], true
		default:
			return "", "", false
		}
	}

	return "", "", false
}
