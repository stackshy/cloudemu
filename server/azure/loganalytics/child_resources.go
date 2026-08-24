package loganalytics

import (
	"net/http"
	"sort"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// childResource is a stored workspace child (savedSearch / table / dataExport).
// The properties are echoed verbatim; only the ARM envelope (id/name/type/etag)
// is synthesized, so a client's CreateOrUpdate round-trips through Get/List.
type childResource struct {
	Name       string
	Etag       string
	Properties map[string]any
}

// childStore holds workspace child resources keyed by workspace, kind and name.
type childStore struct {
	mu sync.RWMutex
	m  map[string]map[string]map[string]*childResource // workspace -> kind -> name
}

func newChildStore() *childStore {
	return &childStore{m: make(map[string]map[string]map[string]*childResource)}
}

func (s *childStore) set(workspace, kind string, res *childResource) {
	s.mu.Lock()
	defer s.mu.Unlock()

	byKind := s.m[workspace]
	if byKind == nil {
		byKind = make(map[string]map[string]*childResource)
		s.m[workspace] = byKind
	}

	byName := byKind[kind]
	if byName == nil {
		byName = make(map[string]*childResource)
		byKind[kind] = byName
	}

	byName[res.Name] = res
}

func (s *childStore) get(workspace, kind, name string) (*childResource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res, ok := s.m[workspace][kind][name]

	return res, ok
}

func (s *childStore) delete(workspace, kind, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	byName := s.m[workspace][kind]
	if _, ok := byName[name]; !ok {
		return false
	}

	delete(byName, name)

	return true
}

// list returns the children of a workspace/kind sorted by name for a stable
// paging order.
func (s *childStore) list(workspace, kind string) []*childResource {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byName := s.m[workspace][kind]
	out := make([]*childResource, 0, len(byName))

	for _, res := range byName {
		out = append(out, res)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

func (s *childStore) deleteWorkspace(workspace string) {
	s.mu.Lock()
	delete(s.m, workspace)
	s.mu.Unlock()
}

// childRequest is the inbound CreateOrUpdate body for a workspace child.
type childRequest struct {
	Etag       string         `json:"etag,omitempty"`
	Properties map[string]any `json:"properties"`
}

// childJSON is the ARM child-resource envelope.
type childJSON struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Etag       string         `json:"etag,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

// childListResult is the paged list envelope for a workspace child collection.
type childListResult struct {
	Value []childJSON `json:"value"`
}

// serveChild routes CRUD for a workspace child collection (savedSearches /
// tables / dataExports). A missing workspace name segment lists the collection.
func (h *Handler) serveChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.logs.GetLogGroup(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listChildren(w, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putChild(w, r, rp)
	case http.MethodGet:
		h.getChild(w, rp)
	case http.MethodDelete:
		h.deleteChild(w, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) putChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req childRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	res := &childResource{Name: rp.SubResourceName, Etag: req.Etag, Properties: req.Properties}
	if rp.SubResource == subTables {
		res.Properties = withProvisioningState(res.Properties)
	}

	h.children.set(rp.ResourceName, rp.SubResource, res)
	azurearm.WriteJSON(w, http.StatusOK, h.childToJSON(rp, res))
}

func (h *Handler) getChild(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	res, ok := h.children.get(rp.ResourceName, rp.SubResource, rp.SubResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", rp.SubResource+" "+rp.SubResourceName+" not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.childToJSON(rp, res))
}

func (h *Handler) deleteChild(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	if !h.children.delete(rp.ResourceName, rp.SubResource, rp.SubResourceName) {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", rp.SubResource+" "+rp.SubResourceName+" not found")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listChildren(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	items := h.children.list(rp.ResourceName, rp.SubResource)
	out := childListResult{Value: make([]childJSON, 0, len(items))}

	for _, res := range items {
		out.Value = append(out.Value, h.childToJSON(rp, res))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// childToJSON renders a stored child as its ARM envelope. The id nests the child
// under the workspace ARM id; the type is the parent/child dotted form real ARM
// returns.
func (*Handler) childToJSON(rp *azurearm.ResourcePath, res *childResource) childJSON {
	wsID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeWorkspaces, rp.ResourceName)

	return childJSON{
		ID:         wsID + "/" + rp.SubResource + "/" + res.Name,
		Name:       res.Name,
		Type:       providerName + "/" + typeWorkspaces + "/" + rp.SubResource,
		Etag:       res.Etag,
		Properties: res.Properties,
	}
}

// withProvisioningState ensures a table's echoed properties carry a terminal
// provisioningState so the TablesClient LRO poller completes on the first
// response.
func withProvisioningState(props map[string]any) map[string]any {
	if props == nil {
		props = map[string]any{}
	}

	if _, ok := props["provisioningState"]; !ok {
		props["provisioningState"] = provisioningSucceeded
	}

	return props
}
