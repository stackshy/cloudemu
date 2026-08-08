// Package identity implements OCI's Identity and Access Management REST API
// as a server.Handler. It serves the /20160918 identity surface: users,
// groups, group memberships, statement policies and the compartment tree.
//
// Coverage:
//
//	POST   /20160918/users                                — CreateUser
//	GET    /20160918/users?compartmentId=…                — ListUsers
//	GET    /20160918/users/{userId}                       — GetUser
//	PUT    /20160918/users/{userId}                       — UpdateUser
//	DELETE /20160918/users/{userId}                       — DeleteUser
//	POST   /20160918/groups                               — CreateGroup
//	GET    /20160918/groups?compartmentId=…               — ListGroups
//	GET/PUT/DELETE /20160918/groups/{groupId}             — Get/Update/DeleteGroup
//	POST   /20160918/userGroupMemberships                 — AddUserToGroup
//	GET    /20160918/userGroupMemberships?compartmentId=… — ListUserGroupMemberships
//	GET    /20160918/userGroupMemberships/{id}            — GetUserGroupMembership
//	DELETE /20160918/userGroupMemberships/{id}            — RemoveUserFromGroup
//	POST   /20160918/policies                             — CreatePolicy
//	GET    /20160918/policies?compartmentId=…             — ListPolicies
//	GET/PUT/DELETE /20160918/policies/{policyId}          — Get/Update/DeletePolicy
//	POST   /20160918/compartments                         — CreateCompartment
//	GET    /20160918/compartments?compartmentId=…         — ListCompartments
//	GET/PUT/DELETE /20160918/compartments/{id}            — Get/Update/DeleteCompartment
//
// The Identity API shares the 20160918 version prefix with OCI Core, so
// Matches claims only the five identity collections and leaves the rest of
// that prefix to the compute and networking handlers.
package identity

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const pathPrefix = "/20160918/"

// The identity collections this handler owns.
const (
	segUsers        = "users"
	segGroups       = "groups"
	segMemberships  = "userGroupMemberships"
	segPolicies     = "policies"
	segCompartments = "compartments"
)

// Resource kinds, as they appear in error messages and work requests.
const (
	kindUser        = "user"
	kindGroup       = "group"
	kindMembership  = "user group membership"
	kindPolicy      = "policy"
	kindCompartment = "compartment"
)

// Handler serves OCI Identity requests against the IAM driver.
//
// The portable IAM interface keys resources by name, lists them unscoped and
// models policies as JSON documents, so every endpoint here is served through
// the OCI capabilities the driver may implement; a driver that implements none
// answers 501 rather than a wrong shape.
type Handler struct {
	identity     iamdriver.OCIIdentity
	compartments iamdriver.Compartments
	policies     iamdriver.StatementPolicies
	work         *workrequest.Store
}

// New returns an Identity handler backed by drv, discovering the OCI-shaped
// capabilities by type assertion.
func New(drv iamdriver.IAM, work *workrequest.Store) *Handler {
	h := &Handler{work: work}
	h.identity, _ = drv.(iamdriver.OCIIdentity)
	h.compartments, _ = drv.(iamdriver.Compartments)
	h.policies, _ = drv.(iamdriver.StatementPolicies)

	return h
}

// Matches claims the five identity collections under /20160918/.
func (*Handler) Matches(r *http.Request) bool {
	collection, _, ok := parseRoute(r.URL.Path)
	if !ok {
		return false
	}

	switch collection {
	case segUsers, segGroups, segMemberships, segPolicies, segCompartments:
		return true
	default:
		return false
	}
}

// ServeHTTP routes by collection, then by URL shape and HTTP verb.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	collection, id, ok := parseRoute(r.URL.Path)
	if !ok {
		ocirest.WriteError(w, r, http.StatusBadRequest, "InvalidParameter", "malformed identity path")
		return
	}

	switch collection {
	case segUsers:
		h.routePrincipal(w, r, kindUser, id)
	case segGroups:
		h.routePrincipal(w, r, kindGroup, id)
	case segMemberships:
		h.routeMembership(w, r, id)
	case segPolicies:
		h.routePolicy(w, r, id)
	case segCompartments:
		h.routeCompartment(w, r, id)
	default:
		ocirest.WriteError(w, r, http.StatusNotFound, "NotFound", "unknown identity resource: "+collection)
	}
}

// parseRoute splits /20160918/{collection}[/{id}].
func parseRoute(urlPath string) (collection, id string, ok bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return "", "", false
	}

	parts := strings.Split(strings.Trim(strings.TrimPrefix(urlPath, pathPrefix), "/"), "/")

	switch len(parts) {
	case 1:
		return parts[0], "", parts[0] != ""
	case 2: //nolint:mnd // a collection plus one resource id
		return parts[0], parts[1], parts[1] != ""
	default:
		return "", "", false
	}
}

// codeInvalidParameter is OCI's error code for a bad request body or query.
const codeInvalidParameter = "InvalidParameter"

// collectionOps is what one identity collection serves. A nil update means the
// collection has no PUT.
type collectionOps struct {
	kind   string
	create func(http.ResponseWriter, *http.Request)
	list   func(http.ResponseWriter, *http.Request)
	get    func(http.ResponseWriter, *http.Request, string)
	update func(http.ResponseWriter, *http.Request, string)
	remove func(http.ResponseWriter, *http.Request, string)
}

// route dispatches a collection by URL shape and HTTP verb.
func (o *collectionOps) route(w http.ResponseWriter, r *http.Request, id string) {
	switch {
	case id == "" && r.Method == http.MethodPost:
		o.create(w, r)
	case id == "" && r.Method == http.MethodGet:
		o.list(w, r)
	case id == "":
		methodNotAllowed(w, r, o.kind)
	case r.Method == http.MethodGet:
		o.get(w, r, id)
	case r.Method == http.MethodPut && o.update != nil:
		o.update(w, r, id)
	case r.Method == http.MethodDelete:
		o.remove(w, r, id)
	default:
		methodNotAllowed(w, r, o.kind)
	}
}

// requireBodyCompartment rejects a create body that names no compartment, the
// way real OCI does.
func requireBodyCompartment(w http.ResponseWriter, r *http.Request, id string) bool {
	if id == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return false
	}

	return true
}

// methodNotAllowed answers a verb the collection does not serve.
func methodNotAllowed(w http.ResponseWriter, r *http.Request, kind string) {
	ocirest.WriteError(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed",
		"unsupported method "+r.Method+" on "+kind)
}

// capabilityMissing answers a driver that does not model the OCI resource.
func capabilityMissing(w http.ResponseWriter, r *http.Request, kind string) {
	ocirest.WriteError(w, r, http.StatusNotImplemented, "NotImplemented",
		"the configured IAM driver does not model the OCI "+kind)
}

// paginate applies OCI's page/limit window and returns the token for the next
// page, which is empty on the last one.
func paginate[T any](items []T, r *http.Request) (window []T, next string) {
	offset := 0
	if n, err := strconv.Atoi(ocirest.Page(r)); err == nil && n > 0 {
		offset = n
	}

	if offset >= len(items) {
		return nil, ""
	}

	end := min(offset+ocirest.Limit(r), len(items))
	if end < len(items) {
		next = strconv.Itoa(end)
	}

	return items[offset:end], next
}

// writeList writes one page of a list response.
func writeList[T any](w http.ResponseWriter, r *http.Request, items []T) {
	window, next := paginate(items, r)

	ocirest.SetNextPage(w, next)
	ocirest.WriteJSON(w, r, http.StatusOK, window)
}
