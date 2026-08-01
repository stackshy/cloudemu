// Package bigtable implements the Google Cloud Bigtable Admin REST API
// (bigtableadmin.googleapis.com/v2) as a server.Handler. Real
// google.golang.org/api/bigtableadmin/v2 clients configured with a custom
// endpoint (option.WithEndpoint) hit this handler unchanged.
//
// Long-running operations complete synchronously: mutating RPCs that return an
// Operation return one with done=true and the resulting resource in Response,
// and operations.get returns a done Operation, so SDK LRO waits terminate.
package bigtable

import (
	"net/http"
	"strings"

	bt "google.golang.org/api/bigtableadmin/v2"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

const basePrefix = "/v2/"

// Handler serves Bigtable Admin requests against a bigtable driver.
type Handler struct {
	db btdriver.Admin
}

// New returns a Bigtable Admin handler backed by db.
func New(db btdriver.Admin) *Handler {
	return &Handler{db: db}
}

// Matches claims /v2 Bigtable Admin paths (instances and their sub-resources,
// plus the operations collection).
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path
	if !strings.HasPrefix(p, basePrefix) {
		return false
	}

	return strings.Contains(p, "/instances") || strings.HasPrefix(p, basePrefix+"operations/")
}

// route is a parsed Bigtable Admin request path.
type route struct {
	name       string // full resource name (no trailing verb)
	parent     string // parent name for collection endpoints
	collection string // instances | clusters | tables | appProfiles | backups
	id         string // resource id (empty for collection endpoints)
	isResource bool
	verb       string // trailing :verb, or ""
}

func parseRoute(path string) route {
	p := strings.TrimPrefix(path, basePrefix)

	verb := ""

	if slash := strings.LastIndex(p, "/"); slash >= 0 {
		if colon := strings.Index(p[slash:], ":"); colon >= 0 {
			verb = p[slash+colon+1:]
			p = p[:slash+colon]
		}
	}

	parts := strings.Split(p, "/")
	rt := route{name: p, verb: verb}

	if len(parts)%2 == 0 {
		rt.isResource = true
		rt.collection = parts[len(parts)-2]
		rt.id = parts[len(parts)-1]
		rt.parent = strings.Join(parts[:len(parts)-2], "/")
	} else {
		rt.collection = parts[len(parts)-1]
		rt.parent = strings.Join(parts[:len(parts)-1], "/")
	}

	return rt
}

// ServeHTTP dispatches on the parsed route's collection + method + verb.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt := parseRoute(r.URL.Path)

	switch rt.collection {
	case "operations":
		h.getOperation(w, r, &rt)
	case "instances":
		h.serveInstances(w, r, &rt)
	case "clusters":
		h.serveClusters(w, r, &rt)
	case "tables":
		h.serveTables(w, r, &rt)
	case "appProfiles":
		h.serveAppProfiles(w, r, &rt)
	case "backups":
		h.serveBackups(w, r, &rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported Bigtable path: "+r.URL.Path)
	}
}

func (h *Handler) getOperation(w http.ResponseWriter, r *http.Request, rt *route) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	op, err := h.db.GetOperation(r.Context(), operationName(rt.name))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, &bt.Operation{Name: op.Name, Done: op.Done})
}

// serveIamVerb handles the :getIamPolicy / :setIamPolicy / :testIamPermissions
// custom verbs shared across instances, tables, and backups. It returns true
// when it handled the verb.
func (h *Handler) serveIamVerb(w http.ResponseWriter, r *http.Request, resource, verb string) bool {
	switch verb {
	case "getIamPolicy":
		p, err := h.db.GetIamPolicy(r.Context(), resource)
		if err != nil {
			gcprest.WriteCErr(w, err)
			return true
		}

		gcprest.WriteJSON(w, http.StatusOK, toWirePolicy(p))
	case "setIamPolicy":
		var in bt.SetIamPolicyRequest
		if !gcprest.DecodeJSON(w, r, &in) {
			return true
		}

		var pol btdriver.Policy
		if in.Policy != nil {
			pol = fromWirePolicy(in.Policy)
		}

		p, err := h.db.SetIamPolicy(r.Context(), resource, pol)
		if err != nil {
			gcprest.WriteCErr(w, err)
			return true
		}

		gcprest.WriteJSON(w, http.StatusOK, toWirePolicy(p))
	case "testIamPermissions":
		var in bt.TestIamPermissionsRequest
		if !gcprest.DecodeJSON(w, r, &in) {
			return true
		}

		perms, err := h.db.TestIamPermissions(r.Context(), resource, in.Permissions)
		if err != nil {
			gcprest.WriteCErr(w, err)
			return true
		}

		gcprest.WriteJSON(w, http.StatusOK, &bt.TestIamPermissionsResponse{Permissions: perms})
	default:
		return false
	}

	return true
}

func methodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
