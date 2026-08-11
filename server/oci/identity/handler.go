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
// Two operations are claimed only to disclose themselves, answering 501 rather
// than the misleading 404 an unclaimed path would get:
//
//	POST /20160918/compartments/{id}/actions/moveCompartment — MoveCompartment
//	ANY  /20181025/quotas…                                   — the Quotas API
//
// The Identity API shares the 20160918 version prefix with OCI Core, so
// Matches claims only the five identity collections and leaves the rest of
// that prefix to the compute and networking handlers. Nothing else claims
// 20181025, which OCI reserves for Limits and Quotas.
package identity

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	identityprovider "github.com/stackshy/cloudemu/v2/providers/oci/identity"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const pathPrefix = "/20160918/"

// quotasPath is the OCI Quotas surface, which CloudEmu discloses rather than
// serves: the emulator enforces no quotas or limits.
const quotasPath = "/20181025/quotas"

// The action shape /{collection}/{id}/actions/{action}.
const (
	segActions            = "actions"
	actionMoveCompartment = "moveCompartment"
)

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

// Compartments is the compartment tree, OCI's resource container. No other
// cloud in this repo has an equivalent, so the portable IAM interface does not
// model it. *providers/oci/identity.Mock satisfies it; a driver that does not
// is served 501 on the compartment paths.
type Compartments interface {
	CreateCompartment(ctx context.Context, spec identityprovider.CompartmentSpec) (
		*identityprovider.CompartmentInfo, error)
	GetCompartment(ctx context.Context, id string) (*identityprovider.CompartmentInfo, error)
	// ListCompartments returns the direct children of parentID, or every
	// descendant when inSubtree is set.
	ListCompartments(ctx context.Context, parentID string, inSubtree bool) ([]identityprovider.CompartmentInfo, error)
	UpdateCompartment(ctx context.Context, id string, upd identityprovider.Update) (
		*identityprovider.CompartmentInfo, error)
	DeleteCompartment(ctx context.Context, id string) error
}

// OCIIdentity is the OCI-shaped user and group surface. The portable interface
// keys users and groups by name and lists them unscoped; OCI addresses them by
// OCID, scopes them to a compartment, and makes group membership a resource in
// its own right.
type OCIIdentity interface {
	CreateOCIUser(ctx context.Context, spec identityprovider.PrincipalSpec) (*identityprovider.PrincipalInfo, error)
	GetOCIUser(ctx context.Context, id string) (*identityprovider.PrincipalInfo, error)
	ListOCIUsers(ctx context.Context, compartmentID string) ([]identityprovider.PrincipalInfo, error)
	UpdateOCIUser(ctx context.Context, id string, upd identityprovider.Update) (*identityprovider.PrincipalInfo, error)
	DeleteOCIUser(ctx context.Context, id string) error

	CreateOCIGroup(ctx context.Context, spec identityprovider.PrincipalSpec) (*identityprovider.PrincipalInfo, error)
	GetOCIGroup(ctx context.Context, id string) (*identityprovider.PrincipalInfo, error)
	ListOCIGroups(ctx context.Context, compartmentID string) ([]identityprovider.PrincipalInfo, error)
	UpdateOCIGroup(ctx context.Context, id string, upd identityprovider.Update) (*identityprovider.PrincipalInfo, error)
	DeleteOCIGroup(ctx context.Context, id string) error

	CreateOCIGroupMembership(ctx context.Context, userID, groupID string) (*identityprovider.MembershipInfo, error)
	GetOCIGroupMembership(ctx context.Context, id string) (*identityprovider.MembershipInfo, error)
	// ListOCIGroupMemberships filters by compartment, and further by userID or
	// groupID when either is non-empty.
	ListOCIGroupMemberships(ctx context.Context, compartmentID, userID, groupID string) (
		[]identityprovider.MembershipInfo, error)
	DeleteOCIGroupMembership(ctx context.Context, id string) error
}

// StatementPolicies is OCI's policy surface: English-like statement strings
// evaluated against a compartment subtree, not the JSON documents the portable
// policy operations assume, so neither the document model nor policy
// attachment applies.
type StatementPolicies interface {
	CreateStatementPolicy(ctx context.Context, spec *identityprovider.PolicySpec) (
		*identityprovider.StatementPolicyInfo, error)
	GetStatementPolicy(ctx context.Context, id string) (*identityprovider.StatementPolicyInfo, error)
	ListStatementPolicies(ctx context.Context, compartmentID string) ([]identityprovider.StatementPolicyInfo, error)
	UpdateStatementPolicy(ctx context.Context, id string, upd identityprovider.PolicyUpdate) (
		*identityprovider.StatementPolicyInfo, error)
	DeleteStatementPolicy(ctx context.Context, id string) error
	// Evaluate reports whether any statement grants the request. OCI policies
	// are allow-only, so there is nothing for a deny to override. A statement
	// the implementation cannot resolve returns Unimplemented rather than a
	// grant, so a restriction is never silently dropped.
	Evaluate(ctx context.Context, req *identityprovider.AccessRequest) (bool, error)
}

// Handler serves OCI Identity requests against the IAM driver.
//
// The portable IAM interface keys resources by name, lists them unscoped and
// models policies as JSON documents, so every endpoint here is served through
// the OCI capabilities the driver may implement; a driver that implements none
// answers 501 rather than a wrong shape.
type Handler struct {
	identity     OCIIdentity
	compartments Compartments
	policies     StatementPolicies
	work         *workrequest.Store
}

// New returns an Identity handler backed by drv, discovering the OCI-shaped
// capabilities by type assertion.
func New(drv iamdriver.IAM, work *workrequest.Store) *Handler {
	h := &Handler{work: work}
	h.identity, _ = drv.(OCIIdentity)
	h.compartments, _ = drv.(Compartments)
	h.policies, _ = drv.(StatementPolicies)

	return h
}

// Matches claims the five identity collections under /20160918/, plus the two
// surfaces it serves only to disclose.
func (*Handler) Matches(r *http.Request) bool {
	if isQuotasPath(r.URL.Path) {
		return true
	}

	collection, _, _, ok := parseRoute(r.URL.Path)
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
	if isQuotasPath(r.URL.Path) {
		ocirest.WriteDriverError(w, r, notEmulated("the Quotas API",
			"CloudEmu emulates control surfaces and enforces no quotas or limits"))

		return
	}

	collection, id, action, ok := parseRoute(r.URL.Path)
	if !ok {
		ocirest.WriteError(w, r, http.StatusBadRequest, "InvalidParameter", "malformed identity path")
		return
	}

	if action != "" {
		routeAction(w, r, collection, action)
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

// parseRoute splits /20160918/{collection}[/{id}[/actions/{action}]].
func parseRoute(urlPath string) (collection, id, action string, ok bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return "", "", "", false
	}

	parts := strings.Split(strings.Trim(strings.TrimPrefix(urlPath, pathPrefix), "/"), "/")

	switch len(parts) {
	case 1:
		return parts[0], "", "", parts[0] != ""
	case 2: //nolint:mnd // a collection plus one resource id
		return parts[0], parts[1], "", parts[1] != ""
	case 4: //nolint:mnd // a collection, a resource id and an action
		return parts[0], parts[1], parts[3], parts[2] == segActions && parts[1] != "" && parts[3] != ""
	default:
		return "", "", "", false
	}
}

// isQuotasPath reports whether the request is for the OCI Quotas API.
func isQuotasPath(urlPath string) bool {
	return urlPath == quotasPath || strings.HasPrefix(urlPath, quotasPath+"/")
}

// routeAction answers the /{id}/actions/{action} shapes. CloudEmu serves none
// of them; moveCompartment discloses itself rather than reading as a 404.
func routeAction(w http.ResponseWriter, r *http.Request, collection, action string) {
	if collection == segCompartments && action == actionMoveCompartment {
		ocirest.WriteDriverError(w, r, notEmulated("MoveCompartment",
			"CloudEmu never reparents a compartment; create it under the parent it belongs to"))

		return
	}

	ocirest.WriteError(w, r, http.StatusNotFound, "NotFound", "unknown identity action: "+action)
}

// notEmulated reports an OCI operation this emulator deliberately does not
// serve, the way the driver's unsupported operations report themselves.
func notEmulated(operation, instead string) error {
	return cerrors.Newf(cerrors.Unimplemented, "%s is not emulated: %s", operation, instead)
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
