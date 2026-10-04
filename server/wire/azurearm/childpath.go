package azurearm

import (
	"fmt"
	"net/http"
	"strings"
)

// providersSegment marks an extension resource when it sits at a type
// position, e.g. .../vaults/v/providers/Microsoft.Authorization/locks/l.
const providersSegment = "providers"

// tail returns the segments after {type}. A parsed path keeps exactly Depth
// segments; a hand-built literal (Depth 0) keeps its set fields.
func (rp *ResourcePath) tail() []string {
	segs := []string{rp.ResourceName, rp.SubResource, rp.SubResourceName, rp.SubResourceAction}
	if rp.Rest != "" {
		segs = append(segs, strings.Split(rp.Rest, "/")...)
	}

	n := rp.Depth
	if n == 0 {
		n = len(segs)
		for n > 0 && segs[n-1] == "" {
			n--
		}
	}

	return segs[:min(n, len(segs))]
}

// NestedType is the ARM nested type the path addresses. Type segments sit at
// odd offsets after {type}, so servers/s/databases/d/backupShortTermRetention
// Policies/default gives "servers/databases/backupShortTermRetentionPolicies".
func (rp *ResourcePath) NestedType() string {
	parts := []string{rp.ResourceType}

	for i, seg := range rp.tail() {
		if i%2 == 1 {
			parts = append(parts, seg)
		}
	}

	return strings.Join(parts, "/")
}

// IsExtension reports whether the path addresses an extension resource, i.e.
// "providers" appears at a type position after {type}/{name}. ARM routes these
// to the extension resource provider, not to the parent's.
func (rp *ResourcePath) IsExtension() bool {
	_, _, ok := rp.extension()

	return ok
}

// extension returns the namespace and type of an extension resource path.
func (rp *ResourcePath) extension() (ns, typ string, ok bool) {
	segs := rp.tail()

	for i := 1; i < len(segs); i += 2 {
		if !strings.EqualFold(segs[i], providersSegment) {
			continue
		}

		if i+1 < len(segs) {
			ns = segs[i+1]
		}

		if i+2 < len(segs) {
			typ = segs[i+2]
		}

		return ns, typ, true
	}

	return "", "", false
}

// WriteUnknownType writes the 404 InvalidResourceType response real ARM
// returns for a nested type the resource provider does not have.
func WriteUnknownType(w http.ResponseWriter, r *http.Request, rp *ResourcePath) {
	WriteError(w, http.StatusNotFound, "InvalidResourceType", fmt.Sprintf(
		"The resource type '%s' could not be found in the namespace '%s' for api version '%s'.",
		rp.NestedType(), rp.Provider, r.URL.Query().Get("api-version")))
}

// WriteExtensionNotImplemented writes 501 for an extension resource path that
// no extension handler claimed.
func WriteExtensionNotImplemented(w http.ResponseWriter, rp *ResourcePath) {
	ns, typ, _ := rp.extension()

	WriteError(w, http.StatusNotImplemented, "NotImplemented",
		fmt.Sprintf("cloudemu does not implement extension resource %s/%s", ns, typ))
}

// WriteChildNotImplemented writes 501 for a nested type real Azure has but
// cloudemu does not model yet.
func WriteChildNotImplemented(w http.ResponseWriter, rp *ResourcePath) {
	WriteError(w, http.StatusNotImplemented, "NotImplemented",
		fmt.Sprintf("cloudemu does not implement %s/%s", rp.Provider, rp.NestedType()))
}

// RejectChild answers a child path the handler does not route: 501 for an
// extension resource, otherwise 404 InvalidResourceType.
func RejectChild(w http.ResponseWriter, r *http.Request, rp *ResourcePath) {
	if rp.IsExtension() {
		WriteExtensionNotImplemented(w, rp)
		return
	}

	WriteUnknownType(w, r, rp)
}

// TooDeep rejects an extension path or a path deeper than maxDepth segments
// after {type}, and reports whether it wrote a response.
func TooDeep(w http.ResponseWriter, r *http.Request, rp *ResourcePath, maxDepth int) bool {
	if !rp.IsExtension() && rp.Depth <= maxDepth {
		return false
	}

	RejectChild(w, r, rp)

	return true
}

// DeferredKind is the shape of a nested type cloudemu does not model.
type DeferredKind int

// Deferred shapes.
const (
	// DeferredSingleton is an always-present child such as .../default.
	DeferredSingleton DeferredKind = iota
	// DeferredCollection is a child list.
	DeferredCollection
	// DeferredItem is one named child that can never exist.
	DeferredItem
)

// ServeDeferred answers a nested type real Azure has but cloudemu does not
// model. Reads stay truthful: a singleton returns its documented default, a
// collection is empty and an item is not found. Every write is 501.
func ServeDeferred(w http.ResponseWriter, r *http.Request, rp *ResourcePath, kind DeferredKind,
	defaultBody func() any,
) {
	if r.Method != http.MethodGet {
		WriteChildNotImplemented(w, rp)
		return
	}

	switch kind {
	case DeferredSingleton:
		WriteJSON(w, http.StatusOK, defaultBody())
	case DeferredCollection:
		WriteJSON(w, http.StatusOK, map[string]any{"value": []any{}})
	case DeferredItem:
		WriteError(w, http.StatusNotFound, "ResourceNotFound",
			fmt.Sprintf("The Resource '%s' was not found.", rp.NestedType()))
	}
}

// ServeDeferredChild is ServeDeferred for a .../{child}[/{name}] path: the
// bare child is a collection and a named child is an item.
func ServeDeferredChild(w http.ResponseWriter, r *http.Request, rp *ResourcePath) {
	kind := DeferredCollection
	if rp.SubResourceName != "" {
		kind = DeferredItem
	}

	ServeDeferred(w, r, rp, kind, nil)
}

// deferredChildMaxDepth is the deepest path GuardLeaf serves as deferred:
// .../{child}/{name}.
const deferredChildMaxDepth = 3

// GuardLeaf protects a {type}/{name} leaf that routes no children itself. It
// reports false when rp addresses the resource, so the caller serves it.
// Otherwise it answers the child path and reports true: deferred names child
// types real Azure has (case-insensitive) and gets ServeDeferredChild;
// anything else is rejected. The parent is never read or written.
func GuardLeaf(w http.ResponseWriter, r *http.Request, rp *ResourcePath, deferred ...string) bool {
	if rp.SubResource == "" && rp.Depth <= 1 {
		return false
	}

	if TooDeep(w, r, rp, deferredChildMaxDepth) {
		return true
	}

	for _, d := range deferred {
		if strings.EqualFold(rp.SubResource, d) {
			ServeDeferredChild(w, r, rp)
			return true
		}
	}

	WriteUnknownType(w, r, rp)

	return true
}
