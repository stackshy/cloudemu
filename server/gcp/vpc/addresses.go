package vpc

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Addresses are reserved IP ranges. Private services access uses a global one
// to carve out the block a managed service is peered into, so a caller
// reserves it while building a network and releases it while tearing one
// down, which is where its absence stops the work.
//
// The records live in the GCP networking provider (driver.GCPAddressStore),
// not in this handler, so they (and their labels / labelFingerprint) are in the
// emulator snapshot, survive a restore, and read the same through the Go
// library and `serve`. addressStore adapts that capability to the handler: a
// networking driver without it serves addresses as 501.
type addressStore struct {
	store netdriver.GCPAddressStore
}

func newAddressStore(n netdriver.Networking) *addressStore {
	s, _ := n.(netdriver.GCPAddressStore)

	return &addressStore{store: s}
}

// errAddressesUnsupported is returned when the networking driver does not
// implement the reserved-address capability.
var errAddressesUnsupported = cerrors.New(cerrors.Unimplemented,
	"compute addresses are not supported by this networking driver")

func (s *addressStore) insert(ctx context.Context, project, scope, name string, body json.RawMessage) error {
	if s.store == nil {
		return errAddressesUnsupported
	}

	return s.store.InsertGCPAddress(ctx, netdriver.GCPAddress{Project: project, Scope: scope, Name: name, Body: body})
}

func (s *addressStore) get(ctx context.Context, project, scope, name string) (json.RawMessage, bool) {
	if s.store == nil {
		return nil, false
	}

	a, err := s.store.GetGCPAddress(ctx, project, scope, name)
	if err != nil {
		return nil, false
	}

	return a.Body, true
}

func (s *addressStore) list(ctx context.Context, project, scope string) []json.RawMessage {
	if s.store == nil {
		return nil
	}

	all, err := s.store.ListGCPAddresses(ctx, project, scope)
	if err != nil {
		return nil
	}

	out := make([]json.RawMessage, 0, len(all))
	for i := range all {
		out = append(out, all[i].Body)
	}

	return out
}

// allByScope returns every stored address for a project grouped by the scope
// ("global" or a region name) it was reserved in. The grouping aggregatedList
// projects into per-scope buckets.
func (s *addressStore) allByScope(ctx context.Context, project string) map[string][]json.RawMessage {
	out := map[string][]json.RawMessage{}

	if s.store == nil {
		return out
	}

	all, err := s.store.ListGCPAddresses(ctx, project, "")
	if err != nil {
		return out
	}

	for i := range all {
		out[all[i].Scope] = append(out[all[i].Scope], all[i].Body)
	}

	return out
}

func (s *addressStore) delete(ctx context.Context, project, scope, name string) bool {
	return s.store != nil && s.store.DeleteGCPAddress(ctx, project, scope, name) == nil
}

// allocIP hands out the next IP from the provider's synthetic reserved range.
// Real GCP allocates an address at reservation time; a caller reading back
// status RESERVED with an actual IP is what unblocks PSA/VPC-peering range setup.
func (s *addressStore) allocIP(ctx context.Context) string {
	if s.store == nil {
		return ""
	}

	ip, _ := s.store.AllocateGCPAddressIP(ctx)

	return ip
}

// scopeOf keys an address by the scope it was reserved in, so a global
// address and a regional one of the same name stay distinct.
//
//nolint:gocritic // rp is a request-scoped value
func scopeOf(rp gcprest.ResourcePath) string {
	if rp.Scope == gcprest.ScopeGlobal {
		return gcprest.ScopeGlobal
	}

	return rp.ScopeName
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeAddresses(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.Scope == gcprest.ScopeAggregated {
		if r.Method == http.MethodGet {
			h.aggregatedListAddresses(w, r, rp)
		} else {
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertAddress(w, r, rp)
		case http.MethodGet:
			h.listAddresses(w, r, rp)
		default:
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	if rp.Action != "" {
		if rp.Action == setLabelsAction && r.Method == http.MethodPost {
			h.setAddressLabels(w, r, rp)
		} else {
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getAddress(w, r, rp)
	case http.MethodDelete:
		h.deleteAddress(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertAddress(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var raw json.RawMessage
	if !gcprest.DecodeJSON(w, r, &raw) {
		return
	}

	var named struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal(raw, &named); err != nil || named.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "name is required")
		return
	}

	if h.addresses.store == nil {
		writeAddressErr(w, errAddressesUnsupported)
		return
	}

	if _, exists := h.addresses.get(r.Context(), rp.Project, scopeOf(rp), named.Name); exists {
		gcprest.WriteError(w, http.StatusConflict, "alreadyExists",
			"address "+named.Name+" already exists")

		return
	}

	err := h.addresses.insert(r.Context(), rp.Project, scopeOf(rp), named.Name,
		h.enrichAddress(r.Context(), raw, rp, hostOf(r), named.Name))
	if err != nil {
		writeAddressErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.ops.RecordDone(hostOf(r), rp.Project,
		rp.Scope, rp.ScopeName, resourceAddresses, named.Name, "insert"))
}

// enrichAddress fills the server-assigned fields real GCP stamps on a reserved
// address: kind, id, status=RESERVED, an allocated IP, selfLink, region and
// creationTimestamp, while preserving everything the caller sent (purpose,
// prefixLength, addressType, …). Without this a Get reads back all-empty.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) enrichAddress(ctx context.Context, raw json.RawMessage, rp gcprest.ResourcePath,
	host, name string,
) json.RawMessage {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return raw
	}

	body["kind"] = "compute#address"
	body["id"] = numericID(rp.Project + "/" + scopeOf(rp) + "/" + name)
	body["status"] = "RESERVED"
	body["selfLink"] = gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, resourceAddresses, name)
	body["creationTimestamp"] = nowRFC3339()

	// labelFingerprint is stamped by the provider on insert.
	if addr, ok := body["address"].(string); !ok || addr == "" {
		body["address"] = h.addresses.allocIP(ctx)
	}

	if rp.Scope == gcprest.ScopeRegions {
		body["region"] = host + "/compute/v1/projects/" + rp.Project + "/regions/" + rp.ScopeName
	}

	enriched, err := json.Marshal(body)
	if err != nil {
		return raw
	}

	return enriched
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getAddress(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	body, ok := h.addresses.get(r.Context(), rp.Project, scopeOf(rp), rp.ResourceName)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"address "+rp.ResourceName+" not found")

		return
	}

	body = reflectAddressUsage(body, h.addressUsersByIP(r.Context(), hostOf(r), rp.Project))

	gcprest.WriteJSON(w, http.StatusOK, body)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listAddresses(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	all := h.addresses.list(r.Context(), rp.Project, scopeOf(rp))
	filter := r.URL.Query().Get("filter")
	usersByIP := h.addressUsersByIP(r.Context(), hostOf(r), rp.Project)

	items := make([]json.RawMessage, 0, len(all))

	for _, body := range all {
		if addressMatches(filter, body) {
			items = append(items, reflectAddressUsage(body, usersByIP))
		}
	}

	// Stable order for offset pagination.
	sort.SliceStable(items, func(i, j int) bool { return rawName(items[i]) < rawName(items[j]) })

	page, err := pagination.Paginate(items, r.URL.Query().Get("pageToken"),
		maxResultsOf(r.URL.Query().Get("maxResults")))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := map[string]any{
		"kind":  "compute#addressList",
		"items": page.Items,
	}
	if page.NextPageToken != "" {
		out["nextPageToken"] = page.NextPageToken
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) aggregatedListAddresses(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	byScope := h.addresses.allByScope(r.Context(), rp.Project)
	filter := r.URL.Query().Get("filter")
	host := hostOf(r)
	usersByIP := h.addressUsersByIP(r.Context(), host, rp.Project)
	items := map[string]addressesScopedList{}

	for scope, bodies := range byScope {
		key := "regions/" + scope
		if scope == gcprest.ScopeGlobal {
			key = gcprest.ScopeGlobal
		}

		list := make([]json.RawMessage, 0, len(bodies))

		for _, b := range bodies {
			if addressMatches(filter, b) {
				list = append(list, reflectAddressUsage(b, usersByIP))
			}
		}

		sort.SliceStable(list, func(i, j int) bool { return rawName(list[i]) < rawName(list[j]) })

		items[key] = addressesScopedList{Addresses: list}
	}

	// GCP always includes a global bucket, warned when it holds nothing.
	if _, ok := items[gcprest.ScopeGlobal]; !ok {
		items[gcprest.ScopeGlobal] = addressesScopedList{
			Warning: &scopedWarning{Code: noResultsOnPageStr, Message: "There are no results for scope 'global' on this page."},
		}
	}

	gcprest.WriteJSON(w, http.StatusOK, addressAggregatedListResponse{
		Kind:     "compute#addressAggregatedList",
		ID:       "projects/" + rp.Project + "/aggregated/addresses",
		Items:    items,
		SelfLink: host + "/compute/v1/projects/" + rp.Project + "/aggregated/addresses",
	})
}

// rawName extracts the "name" field from a stored address body for filtering
// and ordering.
func rawName(body json.RawMessage) string {
	var named struct {
		Name string `json:"name"`
	}

	_ = json.Unmarshal(body, &named)

	return named.Name
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteAddress(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	host := hostOf(r)

	// Real GCP refuses to delete a reserved address an instance still holds via
	// an accessConfig natIP, returning 400 resourceInUseByAnotherResource (the
	// same in-use guard the disk/subnetwork deletes carry). The address deletes
	// cleanly once the instance releasing it is gone.
	body, ok := h.addresses.get(r.Context(), rp.Project, scopeOf(rp), rp.ResourceName)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"address "+rp.ResourceName+" not found")

		return
	}

	if ip := addressIP(body); ip != "" {
		if users := h.addressUsersByIP(r.Context(), host, rp.Project)[ip]; len(users) > 0 {
			addrLink := gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, resourceAddresses, rp.ResourceName)
			gcprest.WriteError(w, http.StatusBadRequest, "resourceInUseByAnotherResource",
				"The address resource '"+addrLink+"' is already being used by '"+users[0]+"'")

			return
		}
	}

	if !h.addresses.delete(r.Context(), rp.Project, scopeOf(rp), rp.ResourceName) {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"address "+rp.ResourceName+" not found")

		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.ops.RecordDone(host, rp.Project,
		rp.Scope, rp.ScopeName, resourceAddresses, rp.ResourceName, "delete"))
}

// instAccessConfigsTag mirrors the compute wire handler's internal tag key
// (server/gcp/compute) that round-trips an instance's external-IP accessConfigs,
// so this handler can tell whether an instance is holding a reserved address
// without importing the compute server package.
const instAccessConfigsTag = "cloudemu:gcp:accessconfigs"

// addressUsersByIP scans instances and maps each external IP an instance holds
// through its accessConfigs[].natIP to the self-links of the instances using it.
// A reserved address whose IP appears here reads back IN_USE with users[]
// pointing at that instance (mirrors the compute-side disk users[] scan). A nil
// compute driver (compute not wired) reports no users.
func (h *Handler) addressUsersByIP(ctx context.Context, host, project string) map[string][]string {
	if h.compute == nil {
		return nil
	}

	instances, err := h.compute.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return nil
	}

	out := make(map[string][]string)

	for i := range instances {
		name := tagOr(instances[i].Tags, instNameTag, instances[i].ID)
		zone := tagOr(instances[i].Tags, instZoneTag, "")
		link := gcprest.SelfLink(host, project, gcprest.ScopeZones, zone, "instances", name)

		for _, ip := range natIPsOf(instances[i].Tags) {
			if ip != "" {
				out[ip] = append(out[ip], link)
			}
		}
	}

	return out
}

// natIPsOf decodes the external IPs an instance's accessConfigs reference from
// its round-trip tag entry.
func natIPsOf(tags map[string]string) []string {
	raw := tags[instAccessConfigsTag]
	if raw == "" {
		return nil
	}

	var acs []struct {
		NatIP string `json:"natIP"`
	}

	if err := json.Unmarshal([]byte(raw), &acs); err != nil {
		return nil
	}

	out := make([]string, 0, len(acs))
	for _, ac := range acs {
		out = append(out, ac.NatIP)
	}

	return out
}

// reflectAddressUsage overlays the live IN_USE status and users[] onto a stored
// address body when an instance holds its IP. Real GCP flips a reserved address
// RESERVED->IN_USE while an instance's accessConfig references it, then back to
// RESERVED once the instance is gone; deriving it from the instance scan keeps
// the two consistent without cross-handler mutation.
func reflectAddressUsage(body json.RawMessage, usersByIP map[string][]string) json.RawMessage {
	if len(usersByIP) == 0 {
		return body
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil || m == nil {
		return body
	}

	ip, _ := m["address"].(string)

	users := usersByIP[ip]
	if len(users) == 0 {
		return body
	}

	m["status"] = "IN_USE"
	m["users"] = users

	out, err := json.Marshal(m)
	if err != nil {
		return body
	}

	return out
}

// addressIP extracts the allocated IP from a stored address body.
func addressIP(body json.RawMessage) string {
	var a struct {
		Address string `json:"address"`
	}

	_ = json.Unmarshal(body, &a)

	return a.Address
}

// writeAddressErr writes a provider error for an address call, answering a
// driver without the address capability with 501.
func writeAddressErr(w http.ResponseWriter, err error) {
	if cerrors.GetCode(err) == cerrors.Unimplemented {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", cerrors.Message(err))
		return
	}

	gcprest.WriteCErr(w, err)
}
