// Package scim emulates the Databricks workspace SCIM 2.0 identity APIs
// (the /api/2.0/preview/scim/v2/... surface served at the workspace URL) as a
// server.Handler. Point the real github.com/databricks/databricks-sdk-go
// WorkspaceClient at a server registered with this handler and the
// w.Users, w.Groups and w.ServicePrincipals operations work end-to-end against
// an in-memory backend.
//
// Covered endpoints (Users, Groups and ServicePrincipals each):
//
//	POST   /api/2.0/preview/scim/v2/{Resource}        create
//	GET    /api/2.0/preview/scim/v2/{Resource}        list (SCIM envelope)
//	GET    /api/2.0/preview/scim/v2/{Resource}/{id}   get
//	PUT    /api/2.0/preview/scim/v2/{Resource}/{id}   update (replace)
//	PATCH  /api/2.0/preview/scim/v2/{Resource}/{id}   patch
//	DELETE /api/2.0/preview/scim/v2/{Resource}/{id}   delete
//
// SCIM list pagination: the databricks-sdk-go pager keeps requesting pages
// (startIndex = startIndex + len(Resources)) until a page returns zero
// Resources. List therefore returns every matching resource on the first page
// and an empty Resources slice for any startIndex past the last item, which
// terminates the iterator after a single populated page.
package scim

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const maxBodyBytes = 5 << 20

// SCIM path segment counts after splitPath. A collection path
// (/api/2.0/preview/scim/v2/Users) has collectionSegs segments; an item path
// (/api/2.0/preview/scim/v2/Users/{id}) has itemSegs.
const (
	collectionSegs = 6
	itemSegs       = 7
	previewIdx     = 2
	resourceIdx    = 5
	idIdx          = 6
)

// firstStartIndex is the 1-based index of the first SCIM result.
const firstStartIndex = 1

// SCIM resource path segments.
const (
	resUsers             = "Users"
	resGroups            = "Groups"
	resServicePrincipals = "ServicePrincipals"
)

// SCIM list response schema URN.
const listResponseSchema = "urn:ietf:params:scim:api:messages:2.0:ListResponse"

// Databricks/SCIM error codes.
const (
	codeNotFound      = "RESOURCE_DOES_NOT_EXIST"
	codeAlreadyExists = "RESOURCE_ALREADY_EXISTS"
	codeInvalidParam  = "INVALID_PARAMETER_VALUE"
	codeMalformed     = "MALFORMED_REQUEST"
	codeNotFoundPath  = "ENDPOINT_NOT_FOUND"
)

// complexValue mirrors the SCIM multi-valued attribute shape (emails, members,
// roles, entitlements, groups).
type complexValue struct {
	Display string `json:"display,omitempty"`
	Primary bool   `json:"primary,omitempty"`
	Ref     string `json:"$ref,omitempty"`
	Type    string `json:"type,omitempty"`
	Value   string `json:"value,omitempty"`
}

// name mirrors the SCIM name sub-object.
type name struct {
	FamilyName string `json:"familyName,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
}

// userJSON is the wire shape for a SCIM User.
type userJSON struct {
	Schemas      []string       `json:"schemas,omitempty"`
	ID           string         `json:"id,omitempty"`
	UserName     string         `json:"userName,omitempty"`
	DisplayName  string         `json:"displayName,omitempty"`
	Active       bool           `json:"active,omitempty"`
	ExternalID   string         `json:"externalId,omitempty"`
	Name         *name          `json:"name,omitempty"`
	Emails       []complexValue `json:"emails,omitempty"`
	Entitlements []complexValue `json:"entitlements,omitempty"`
	Roles        []complexValue `json:"roles,omitempty"`
	Groups       []complexValue `json:"groups,omitempty"`
}

// groupJSON is the wire shape for a SCIM Group.
type groupJSON struct {
	Schemas      []string       `json:"schemas,omitempty"`
	ID           string         `json:"id,omitempty"`
	DisplayName  string         `json:"displayName,omitempty"`
	ExternalID   string         `json:"externalId,omitempty"`
	Members      []complexValue `json:"members,omitempty"`
	Entitlements []complexValue `json:"entitlements,omitempty"`
	Roles        []complexValue `json:"roles,omitempty"`
	Groups       []complexValue `json:"groups,omitempty"`
}

// servicePrincipalJSON is the wire shape for a SCIM ServicePrincipal.
type servicePrincipalJSON struct {
	Schemas       []string       `json:"schemas,omitempty"`
	ID            string         `json:"id,omitempty"`
	ApplicationID string         `json:"applicationId,omitempty"`
	DisplayName   string         `json:"displayName,omitempty"`
	Active        bool           `json:"active,omitempty"`
	ExternalID    string         `json:"externalId,omitempty"`
	Entitlements  []complexValue `json:"entitlements,omitempty"`
	Roles         []complexValue `json:"roles,omitempty"`
	Groups        []complexValue `json:"groups,omitempty"`
}

// Handler serves the Databricks SCIM identity APIs from in-memory state. It is
// safe for concurrent use.
type Handler struct {
	mu                sync.RWMutex
	users             map[string]*userJSON
	groups            map[string]*groupJSON
	servicePrincipals map[string]*servicePrincipalJSON
	nextID            int64
}

// New returns a ready-to-use SCIM handler with empty state.
func New() *Handler {
	return &Handler{
		users:             make(map[string]*userJSON),
		groups:            make(map[string]*groupJSON),
		servicePrincipals: make(map[string]*servicePrincipalJSON),
		nextID:            1,
	}
}

// Matches claims /api/{ver}/preview/scim/v2/... paths.
func (*Handler) Matches(r *http.Request) bool {
	parts := splitPath(r.URL.Path)

	return len(parts) >= collectionSegs && parts[0] == "api" && parts[previewIdx] == "preview"
}

// allocID returns the next monotonically increasing id as a string. Callers
// must hold the write lock.
func (h *Handler) allocID() string {
	id := h.nextID
	h.nextID++

	return strconv.FormatInt(id, 10)
}

// ServeHTTP routes by resource and by the presence of an {id} segment.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	if len(parts) < collectionSegs {
		writeError(w, http.StatusNotFound, codeNotFoundPath, "unsupported path")

		return
	}

	resource := parts[resourceIdx]

	var id string
	if len(parts) >= itemSegs {
		id = parts[idIdx]
	}

	switch resource {
	case resUsers:
		h.serveUsers(w, r, id)
	case resGroups:
		h.serveGroups(w, r, id)
	case resServicePrincipals:
		h.serveServicePrincipals(w, r, id)
	default:
		writeError(w, http.StatusNotFound, codeNotFoundPath, "unknown resource: "+resource)
	}
}

func (h *Handler) serveUsers(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		h.serveUserCollection(w, r)

		return
	}

	h.serveUserItem(w, r, id)
}

func (h *Handler) serveUserCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createUser(w, r)
	case http.MethodGet:
		h.listUsers(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveUserItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getUser(w, id)
	case http.MethodPut, http.MethodPatch:
		h.updateUser(w, r, id)
	case http.MethodDelete:
		h.deleteUser(w, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveGroups(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		h.serveGroupCollection(w, r)

		return
	}

	h.serveGroupItem(w, r, id)
}

func (h *Handler) serveGroupCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createGroup(w, r)
	case http.MethodGet:
		h.listGroups(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveGroupItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getGroup(w, id)
	case http.MethodPut, http.MethodPatch:
		h.updateGroup(w, r, id)
	case http.MethodDelete:
		h.deleteGroup(w, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveServicePrincipals(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		h.serveSPCollection(w, r)

		return
	}

	h.serveSPItem(w, r, id)
}

func (h *Handler) serveSPCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createServicePrincipal(w, r)
	case http.MethodGet:
		h.listServicePrincipals(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveSPItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getServicePrincipal(w, id)
	case http.MethodPut, http.MethodPatch:
		h.updateServicePrincipal(w, r, id)
	case http.MethodDelete:
		h.deleteServicePrincipal(w, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	createKeyed(w, r, &h.mu, createParams[userJSON]{
		store:    h.users,
		alloc:    h.allocID,
		kind:     "user",
		keyField: "userName",
		key:      func(u *userJSON) string { return u.UserName },
		setID:    func(u *userJSON, id string) { u.ID = id },
	})
}

func (h *Handler) getUser(w http.ResponseWriter, id string) {
	h.mu.RLock()
	u, ok := h.users[id]
	h.mu.RUnlock()

	if !ok {
		notFound(w, "user", id)

		return
	}

	writeJSON(w, u)
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	listPage(w, r, &h.mu, h.users)
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request, id string) {
	var in userJSON
	if !decode(w, r, &in) {
		return
	}

	out, ok := replace(w, &h.mu, h.users, id, "user", &in, func(u *userJSON) { u.ID = id })
	if !ok {
		return
	}

	writeJSON(w, &out)
}

func (h *Handler) deleteUser(w http.ResponseWriter, id string) {
	removeByID(w, &h.mu, h.users, id, "user")
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	createKeyed(w, r, &h.mu, createParams[groupJSON]{
		store:    h.groups,
		alloc:    h.allocID,
		kind:     "group",
		keyField: "displayName",
		key:      func(g *groupJSON) string { return g.DisplayName },
		setID:    func(g *groupJSON, id string) { g.ID = id },
	})
}

func (h *Handler) getGroup(w http.ResponseWriter, id string) {
	h.mu.RLock()
	g, ok := h.groups[id]
	h.mu.RUnlock()

	if !ok {
		notFound(w, "group", id)

		return
	}

	writeJSON(w, g)
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	listPage(w, r, &h.mu, h.groups)
}

func (h *Handler) updateGroup(w http.ResponseWriter, r *http.Request, id string) {
	var in groupJSON
	if !decode(w, r, &in) {
		return
	}

	out, ok := replace(w, &h.mu, h.groups, id, "group", &in, func(g *groupJSON) { g.ID = id })
	if !ok {
		return
	}

	writeJSON(w, &out)
}

func (h *Handler) deleteGroup(w http.ResponseWriter, id string) {
	removeByID(w, &h.mu, h.groups, id, "group")
}

func (h *Handler) createServicePrincipal(w http.ResponseWriter, r *http.Request) {
	var in servicePrincipalJSON
	if !decode(w, r, &in) {
		return
	}

	h.mu.Lock()

	in.ID = h.allocID()
	if in.ApplicationID == "" {
		in.ApplicationID = "app-" + in.ID
	}

	stored := in
	h.servicePrincipals[stored.ID] = &stored
	out := stored
	h.mu.Unlock()

	writeJSON(w, &out)
}

func (h *Handler) getServicePrincipal(w http.ResponseWriter, id string) {
	h.mu.RLock()
	sp, ok := h.servicePrincipals[id]
	h.mu.RUnlock()

	if !ok {
		notFound(w, "service principal", id)

		return
	}

	writeJSON(w, sp)
}

func (h *Handler) listServicePrincipals(w http.ResponseWriter, r *http.Request) {
	listPage(w, r, &h.mu, h.servicePrincipals)
}

func (h *Handler) updateServicePrincipal(w http.ResponseWriter, r *http.Request, id string) {
	var in servicePrincipalJSON
	if !decode(w, r, &in) {
		return
	}

	out, ok := replace(w, &h.mu, h.servicePrincipals, id, "service principal", &in, func(sp *servicePrincipalJSON) {
		sp.ID = id
		if sp.ApplicationID == "" {
			sp.ApplicationID = h.servicePrincipals[id].ApplicationID
		}
	})
	if !ok {
		return
	}

	writeJSON(w, &out)
}

func (h *Handler) deleteServicePrincipal(w http.ResponseWriter, id string) {
	removeByID(w, &h.mu, h.servicePrincipals, id, "service principal")
}

// listResponse is the SCIM list envelope. Resources is always returned on the
// first page in full, with totalResults and itemsPerPage equal to its length,
// so the SDK pager stops after a single page.
type listResponse[T any] struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	StartIndex   int      `json:"startIndex"`
	ItemsPerPage int      `json:"itemsPerPage"`
	Resources    []T      `json:"Resources"`
}

// newListResponse wraps items in a SCIM list envelope sized to the slice.
func newListResponse[T any](items []T) listResponse[T] {
	return listResponse[T]{
		Schemas:      []string{listResponseSchema},
		TotalResults: len(items),
		StartIndex:   firstStartIndex,
		ItemsPerPage: len(items),
		Resources:    items,
	}
}

// pastLastItem reports whether the request's startIndex points past the last
// stored item, in which case an empty Resources slice must be returned to stop
// the SDK pager. A missing or first-page startIndex is never past the end.
func pastLastItem(r *http.Request, total int) bool {
	raw := r.URL.Query().Get("startIndex")
	if raw == "" {
		return false
	}

	start, err := strconv.Atoi(raw)
	if err != nil || start <= firstStartIndex {
		return false
	}

	return start > total
}

// createParams configures a keyed create: the store and its kind label, the
// required-key field name, and accessors for the uniqueness key and id.
type createParams[T any] struct {
	store    map[string]*T
	alloc    func() string
	kind     string
	keyField string
	key      func(*T) string
	setID    func(*T, string)
}

// createKeyed decodes a create body, enforces a non-empty unique key, rejects
// duplicates, and stores the value under a fresh id. It serves both Users
// (keyed by userName) and Groups (keyed by displayName).
func createKeyed[T any](w http.ResponseWriter, r *http.Request, mu *sync.RWMutex, p createParams[T]) {
	var in T
	if !decode(w, r, &in) {
		return
	}

	if p.key(&in) == "" {
		writeError(w, http.StatusBadRequest, codeInvalidParam, p.keyField+" is required")

		return
	}

	mu.Lock()
	defer mu.Unlock()

	for _, existing := range p.store {
		if p.key(existing) == p.key(&in) {
			writeError(w, http.StatusConflict, codeAlreadyExists, p.kind+" already exists: "+p.key(&in))

			return
		}
	}

	id := p.alloc()
	p.setID(&in, id)
	stored := in
	p.store[id] = &stored

	writeJSON(w, &stored)
}

// listPage writes the SCIM list envelope for store, returning every value on
// the first page and an empty page for any startIndex past the last item.
func listPage[T any](w http.ResponseWriter, r *http.Request, mu *sync.RWMutex, store map[string]*T) {
	mu.RLock()
	defer mu.RUnlock()

	items := make([]T, 0, len(store))

	if !pastLastItem(r, len(store)) {
		for _, v := range store {
			items = append(items, *v)
		}
	}

	writeJSON(w, newListResponse(items))
}

// replace overwrites the entry at id (preserving the id) and returns the stored
// copy, writing a not-found error and reporting false when id is absent.
func replace[T any](w http.ResponseWriter, mu *sync.RWMutex, store map[string]*T, id, kind string, in *T, setID func(*T)) (T, bool) {
	mu.Lock()
	defer mu.Unlock()

	if _, ok := store[id]; !ok {
		notFound(w, kind, id)

		return *in, false
	}

	setID(in)
	stored := *in
	store[id] = &stored

	return stored, true
}

// removeByID deletes id from store, writing a not-found error when absent and
// an empty success body otherwise.
func removeByID[T any](w http.ResponseWriter, mu *sync.RWMutex, store map[string]*T, id, kind string) {
	mu.Lock()
	_, ok := store[id]

	if ok {
		delete(store, id)
	}

	mu.Unlock()

	if !ok {
		notFound(w, kind, id)

		return
	}

	writeJSON(w, struct{}{})
}

// splitPath strips surrounding slashes and returns the path segments.
// Path /api/2.0/preview/scim/v2/Users → [api, 2.0, preview, scim, v2, Users].
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

func notFound(w http.ResponseWriter, kind, id string) {
	writeError(w, http.StatusNotFound, codeNotFound, kind+" "+id+" does not exist")
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, codeInvalidParam, "method not allowed")
}
