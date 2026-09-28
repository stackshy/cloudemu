package frontdoor

import (
	"maps"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// Addressing shared by the two grandchild types: origins
// (.../profiles/{p}/originGroups/{og}/origins/{o}) and routes
// (.../profiles/{p}/afdEndpoints/{ep}/routes/{r}). azurearm.ParsePath stops at
// the fourth segment past the resource type ({p}/{child}/{childName}/{kind}), so
// the grandchild name is read from the raw path here.

// Segment counts past .../providers/Microsoft.Cdn/profiles/ for a grandchild
// collection ({p}/{child}/{childName}/{kind}) and a single grandchild (+ {name}).
const (
	nestedListSegments = 4
	nestedItemSegments = 5
)

// nestedPath is a parsed grandchild address. parent is the origin group (for an
// origin) or the endpoint (for a route); name is empty for a collection.
type nestedPath struct {
	sub     string
	rg      string
	profile string
	parent  string
	name    string
}

// parseNestedPath reads the grandchild address from the request path. ok is
// false when the path has more segments than a grandchild item (a deeper,
// unmodeled surface).
func parseNestedPath(urlPath string, rp *azurearm.ResourcePath) (nestedPath, bool) {
	np := nestedPath{
		sub:     rp.Subscription,
		rg:      rp.ResourceGroup,
		profile: rp.ResourceName,
		parent:  rp.SubResourceName,
	}

	rest, ok := segmentsAfterProfiles(urlPath)
	if !ok {
		return np, false
	}

	switch len(rest) {
	case nestedListSegments:
		return np, true
	case nestedItemSegments:
		np.name = rest[nestedItemSegments-1]
		return np, np.name != ""
	default:
		return np, false
	}
}

// segmentsAfterProfiles returns the path segments that follow
// providers/Microsoft.Cdn/profiles.
func segmentsAfterProfiles(urlPath string) ([]string, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")

	for i := 1; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i-1], providerName) && strings.EqualFold(parts[i], typeProfiles) {
			return parts[i+1:], true
		}
	}

	return nil, false
}

// nestedJSON is the ARM wire body shared by origins and routes: neither has a
// location or tags, and every property lives under "properties".
type nestedJSON struct {
	ID         string         `json:"id,omitempty"`
	Name       string         `json:"name,omitempty"`
	Type       string         `json:"type,omitempty"`
	Etag       string         `json:"etag,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

// nestedListResult is the ARM list envelope for origins and routes.
type nestedListResult struct {
	Value []nestedJSON `json:"value"`
}

// dispatchNested routes a grandchild request on method and path shape to the
// given operation set.
func dispatchNested(w http.ResponseWriter, r *http.Request, np *nestedPath, ops *nestedOps) {
	if np.name == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		ops.list(w, r, np)

		return
	}

	switch r.Method {
	case http.MethodPut:
		ops.put(w, r, np)
	case http.MethodGet:
		ops.get(w, r, np)
	case http.MethodPatch:
		ops.patch(w, r, np)
	case http.MethodDelete:
		ops.del(w, r, np)
	default:
		writeMethodNotAllowed(w)
	}
}

// nestedOps is one grandchild type's operation set.
type nestedOps struct {
	put, get, patch, del, list func(http.ResponseWriter, *http.Request, *nestedPath)
}

// writeCreated answers a PUT with 201 for a new resource and 200 for a replace.
func writeCreated(w http.ResponseWriter, created bool, body nestedJSON) {
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, body)
}

// withComputed returns a copy of props with the computed read-only stamps set.
func withComputed(props, computed map[string]any) map[string]any {
	out := make(map[string]any, len(props)+len(computed))
	maps.Copy(out, props)
	maps.Copy(out, computed)

	return out
}

// grandchildID is the ARM id of a grandchild: the profile id, then
// childType/parent/kind/name.
func grandchildID(sub, rg, profile, childType, parent, kind, name string) string {
	return azurearm.BuildResourceID(sub, rg, providerName, typeProfiles, profile) +
		"/" + childType + "/" + parent + "/" + kind + "/" + name
}

// storedETag renders the ETag the store rotates on every write in ARM's weak
// form, falling back to one derived from id for a value restored from a snapshot
// taken before ETags were stored.
func storedETag(etag, id string) string {
	if etag == "" {
		return azurearm.WeakETag(id)
	}

	return `W/"` + etag + `"`
}

// writeErr is the one place Front Door maps provider errors onto ARM responses.
// FailedPrecondition carries Azure's dependency refusals (an origin group still
// used by a route, the last enabled origin of a routed group, a route
// domain/protocol/path conflict), which Azure answers 400 BadRequest rather than
// the 409 azurearm.WriteCErr uses; everything else maps as usual.
func writeErr(w http.ResponseWriter, err error) {
	if cerrors.IsFailedPrecondition(err) {
		azurearm.WriteError(w, http.StatusBadRequest, "BadRequest", cerrors.Message(err))
		return
	}

	azurearm.WriteCErr(w, err)
}

// writePutErr maps a grandchild PUT error. The only NotFound a create raises is
// a missing parent (origin group or endpoint), which ARM reports as 404
// ParentResourceNotFound.
func writePutErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		azurearm.WriteParentNotFound(w, err)
		return
	}

	writeErr(w, err)
}

// writeDeleteResult answers a grandchild DELETE: 200 when it was removed, 204
// when there was nothing to remove (AFDOrigins_Delete and Routes_Delete list
// 204), and the mapped error otherwise.
func writeDeleteResult(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case cerrors.IsNotFound(err):
		w.WriteHeader(http.StatusNoContent)
	default:
		writeErr(w, err)
	}
}
