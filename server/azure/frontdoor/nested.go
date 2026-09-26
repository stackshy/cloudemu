package frontdoor

import (
	"net/http"
	"strings"

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

// profileID is the ARM id of the profile a grandchild lives in.
func (np *nestedPath) profileID() string {
	return azurearm.BuildResourceID(np.sub, np.rg, providerName, typeProfiles, np.profile)
}

// id is the ARM id of the grandchild named name under childType/parent.
func (np *nestedPath) id(childType, kind, name string) string {
	return np.profileID() + "/" + childType + "/" + np.parent + "/" + kind + "/" + name
}

// withName returns a copy of np addressing the grandchild named name.
func (np *nestedPath) withName(name string) *nestedPath {
	out := *np
	out.name = name

	return &out
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

// copyProps returns a copy of props with room for extra computed keys.
func copyProps(props map[string]any, extra int) map[string]any {
	out := make(map[string]any, len(props)+extra)
	for k, v := range props {
		out[k] = v
	}

	return out
}

// setDefault stores v under key when the key is absent.
func setDefault(props map[string]any, key string, v any) {
	if _, ok := props[key]; !ok {
		props[key] = v
	}
}
