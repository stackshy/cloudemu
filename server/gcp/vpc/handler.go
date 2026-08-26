// Package vpc implements the GCP Compute Engine networking REST API
// (networks, subnetworks, firewalls) against a CloudEmu networking driver.
// Real cloud.google.com/go/compute clients hit this handler the same way
// they hit compute.googleapis.com.
//
// Supported operations (parity with AWS EC2 VPC):
//
//	POST   /compute/v1/projects/{p}/global/networks                       — insert network
//	GET    /compute/v1/projects/{p}/global/networks/{name}                — get
//	GET    /compute/v1/projects/{p}/global/networks                       — list
//	DELETE /compute/v1/projects/{p}/global/networks/{name}                — delete
//
//	POST   /compute/v1/projects/{p}/regions/{r}/subnetworks               — insert subnet
//	GET    /compute/v1/projects/{p}/regions/{r}/subnetworks/{name}        — get
//	GET    /compute/v1/projects/{p}/regions/{r}/subnetworks               — list
//	DELETE /compute/v1/projects/{p}/regions/{r}/subnetworks/{name}        — delete
//
//	POST   /compute/v1/projects/{p}/global/firewalls                      — insert firewall
//	GET    /compute/v1/projects/{p}/global/firewalls/{name}               — get
//	GET    /compute/v1/projects/{p}/global/firewalls                      — list
//	DELETE /compute/v1/projects/{p}/global/firewalls/{name}               — delete
package vpc

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/fnv"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const (
	resourceNetworks    = "networks"
	resourceSubnetworks = "subnetworks"
	resourceFirewalls   = "firewalls"
	resourceRouters     = "routers"
	resourceAddresses   = "addresses"
	resourceRoutes      = "routes"
	netNameTag          = "cloudemu:gcpNetName"
	subnetNameTag       = "cloudemu:gcpSubnetName"
	subnetNetworkTag    = "cloudemu:gcpSubnetNet"
	autoSubnetTag       = "cloudemu:gcpAutoSubnet"
	legacyNetTag        = "cloudemu:gcpLegacyNet"
	createdAtTag        = "cloudemu:gcpCreatedAt"
	netDescTag          = "cloudemu:gcpNetDesc"
	netRoutingModeTag   = "cloudemu:gcpNetRoutingMode"
	netMtuTag           = "cloudemu:gcpNetMtu"
	subnetPurposeTag    = "cloudemu:gcpSubnetPurpose"
	subnetStackTag      = "cloudemu:gcpSubnetStack"
	subnetPGATag        = "cloudemu:gcpSubnetPGA"
	subnetDescTag       = "cloudemu:gcpSubnetDesc"
	subnetSecondaryTag  = "cloudemu:gcpSubnetSecondary"
	firewallNameTag     = "cloudemu:gcpFwName"
	firewallSpecTag     = "cloudemu:gcpFwSpec"
	trueValue           = "true"

	expandAction       = "expandIpCidrRange"
	noResultsOnPageStr = "NO_RESULTS_ON_PAGE"

	defaultSubnetPurpose = "PRIVATE"
	defaultStackType     = "IPV4_ONLY"
	internalNetCIDR      = "10.0.0.0/16"

	defaultFirewallDirection = "INGRESS"
	defaultFirewallPriority  = 1000
)

// nowRFC3339 returns the current time formatted the way GCP stamps
// creationTimestamp on every resource.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// nameMatches reports whether name satisfies a GCP list filter, delegating to
// the shared gcprest codec so every GCP handler applies filters identically.
func nameMatches(filter, name string) bool { return gcprest.NameMatches(filter, name) }

// maxResultsOf parses the maxResults query param, defaulting when absent/invalid.
func maxResultsOf(raw string) int { return gcprest.MaxResults(raw) }

// instanceLister is the minimal, optional compute-side lookup the subnetwork
// delete guard needs: it lists instances so the handler can reject deleting a
// subnet that still has instances attached. Kept as a local interface (satisfied
// by the shared compute driver) so the VPC handler gains no hard dependency on
// the compute package and stays usable when no compute driver is wired.
type instanceLister interface {
	DescribeInstances(
		ctx context.Context, instanceIDs []string, filters []computedriver.DescribeFilter,
		opts ...computedriver.DescribeInstancesOptions,
	) ([]computedriver.Instance, error)
}

// Handler serves the GCP networking REST surface.
type Handler struct {
	net       netdriver.Networking
	compute   instanceLister
	routers   *routerStore
	addresses *addressStore
	routes    *routeStore
}

// New returns a networks handler. compute is optional (may be nil): when
// provided, deleteSubnetwork rejects deleting a subnet that still has instances.
func New(n netdriver.Networking, compute instanceLister) *Handler {
	return &Handler{
		net:       n,
		compute:   compute,
		routers:   newRouterStore(),
		addresses: newAddressStore(),
		routes:    newRouteStore(),
	}
}

// Matches returns true for /compute/v1/.../networks|subnetworks|firewalls URLs.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := gcprest.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	switch rp.ResourceType {
	case resourceNetworks, resourceSubnetworks, resourceFirewalls,
		resourceRouters, resourceAddresses, resourceRoutes:
		// aggregatedList is a real endpoint only for subnetworks and addresses;
		// the other collections here are global (or list-only) and have no
		// aggregated form, so leave those unmatched for the dispatcher default.
		if rp.Scope == gcprest.ScopeAggregated {
			return rp.ResourceType == resourceSubnetworks || rp.ResourceType == resourceAddresses
		}

		return true
	}

	return false
}

// ServeHTTP routes the request based on resource type and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := gcprest.ParsePath(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed path")
		return
	}

	switch rp.ResourceType {
	case resourceNetworks:
		h.routeNetworks(w, r, rp)
	case resourceSubnetworks:
		h.routeSubnetworks(w, r, rp)
	case resourceFirewalls:
		h.routeFirewalls(w, r, rp)
	case resourceRouters:
		h.routeRouters(w, r, rp)
	case resourceAddresses:
		h.routeAddresses(w, r, rp)
	case resourceRoutes:
		h.routeRoutes(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unknown resource type")
	}
}

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD route shape is duplicate-by-design across resource types
func (h *Handler) routeNetworks(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertNetwork(w, r, rp)
		case http.MethodGet:
			h.listNetworks(w, r, rp)
		default:
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getNetwork(w, r, rp)
	case http.MethodPatch, http.MethodPut:
		h.patchNetwork(w, r, rp)
	case http.MethodDelete:
		h.deleteNetwork(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeSubnetworks(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.Scope == gcprest.ScopeAggregated {
		if r.Method == http.MethodGet {
			h.aggregatedListSubnetworks(w, r, rp)
		} else {
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertSubnetwork(w, r, rp)
		case http.MethodGet:
			h.listSubnetworks(w, r, rp)
		default:
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	// POST .../subnetworks/{name}/expandIpCidrRange widens the primary range.
	if rp.Action == expandAction {
		if r.Method == http.MethodPost {
			h.expandSubnetIPCIDR(w, r, rp)
		} else {
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getSubnetwork(w, r, rp)
	case http.MethodPatch, http.MethodPut:
		h.patchSubnetwork(w, r, rp)
	case http.MethodDelete:
		h.deleteSubnetwork(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD route shape is duplicate-by-design across resource types
func (h *Handler) routeFirewalls(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertFirewall(w, r, rp)
		case http.MethodGet:
			h.listFirewalls(w, r, rp)
		default:
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getFirewall(w, r, rp)
	case http.MethodPatch, http.MethodPut:
		h.patchFirewall(w, r, rp)
	case http.MethodDelete:
		h.deleteFirewall(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// Network operations.

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertNetwork(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.Scope != gcprest.ScopeGlobal {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "networks are global")
		return
	}

	var req networkRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "name required")
		return
	}

	if _, err := findNetByName(r.Context(), h.net, req.Name); err == nil {
		gcprest.WriteError(w, http.StatusConflict, "alreadyExists",
			"network "+req.Name+" already exists")

		return
	}

	cidr, tags := networkStorage(&req)

	cfg := netdriver.VPCConfig{
		CIDRBlock: cidr,
		Tags:      tags,
	}

	if _, err := h.net.CreateVPC(r.Context(), cfg); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		"networks", req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// networkStorage derives the internal CIDR and the tag set persisted for a
// network insert. A network with an explicit IPv4Range is a (deprecated) legacy
// network; only those carry an IPv4Range on the wire. Auto/custom-mode networks
// must NOT report one — the driver still needs a non-empty CIDR internally, so a
// placeholder is stored but hidden from the response unless legacy.
// description/routingMode/mtu have no first-class VPC slots, so they ride in
// tags and are reconstructed on read (else Terraform shows a perpetual diff).
func networkStorage(req *networkRequest) (cidr string, tags map[string]string) {
	cidr = internalNetCIDR
	tags = map[string]string{netNameTag: req.Name, createdAtTag: nowRFC3339()}

	if req.IPv4Range != "" {
		cidr = req.IPv4Range
		tags[legacyNetTag] = trueValue
	}

	if req.AutoCreateSubnetworks != nil && *req.AutoCreateSubnetworks {
		tags[autoSubnetTag] = trueValue
	}

	if req.Description != "" {
		tags[netDescTag] = req.Description
	}

	if req.RoutingConfig != nil && req.RoutingConfig.RoutingMode != "" {
		tags[netRoutingModeTag] = req.RoutingConfig.RoutingMode
	}

	if req.Mtu > 0 {
		tags[netMtuTag] = strconv.Itoa(int(req.Mtu))
	}

	return cidr, tags
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getNetwork(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	v, err := findNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toNetworkResponse(v, rp, hostOf(r)))
}

//nolint:gocritic,dupl // rp is a request-scoped value; list-shape duplicates by-design across resources
func (h *Handler) listNetworks(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	infos, err := h.net.DescribeVPCs(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostOf(r)
	filter := r.URL.Query().Get("filter")

	items := make([]networkResponse, 0, len(infos))

	for i := range infos {
		scope := rp
		scope.ResourceName = tagOr(infos[i].Tags, netNameTag, infos[i].ID)

		resp := toNetworkResponse(&infos[i], scope, host)
		if nameMatches(filter, resp.Name) {
			items = append(items, resp)
		}
	}

	page, err := pagination.PaginateSorted(items,
		func(a, b networkResponse) bool { return a.Name < b.Name },
		r.URL.Query().Get("pageToken"), maxResultsOf(r.URL.Query().Get("maxResults")))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, networkListResponse{
		Kind:          "compute#networkList",
		ID:            "projects/" + rp.Project + "/global/networks",
		Items:         page.Items,
		NextPageToken: page.NextPageToken,
		SelfLink:      gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "networks", ""),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteNetwork(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	v, err := findNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// Real GCP refuses to delete a network that still has subnetworks; the
	// deletion would otherwise orphan them. Scan for live subnets on this
	// network and reject with resourceInUseByAnotherResource.
	if sub, scanErr := h.subnetOnNetwork(r.Context(), v.ID, rp.ResourceName); scanErr != nil {
		gcprest.WriteCErr(w, scanErr)
		return
	} else if sub != "" {
		gcprest.WriteError(w, http.StatusBadRequest, "resourceInUseByAnotherResource",
			"The network resource '"+rp.ResourceName+"' is already being used by '"+sub+"'")

		return
	}

	if err := h.net.DeleteVPC(r.Context(), v.ID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		"networks", rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// patchNetwork applies GCP merge-patch semantics to the mutable network fields
// (description, routingConfig.routingMode, mtu). Only fields present in the
// patch body are updated; the rest ride unchanged in the network's tag set.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) patchNetwork(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	v, err := findNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	var req networkRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	tags := map[string]string{}

	if req.Description != "" {
		tags[netDescTag] = req.Description
	}

	if req.RoutingConfig != nil && req.RoutingConfig.RoutingMode != "" {
		tags[netRoutingModeTag] = req.RoutingConfig.RoutingMode
	}

	if req.Mtu > 0 {
		tags[netMtuTag] = strconv.Itoa(int(req.Mtu))
	}

	if len(tags) > 0 {
		if err := h.net.UpdateVPCTags(r.Context(), v.ID, tags); err != nil {
			gcprest.WriteCErr(w, err)
			return
		}
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		"networks", rp.ResourceName, "patch")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// Subnetwork operations.

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertSubnetwork(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.Scope != gcprest.ScopeRegions {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "subnetworks are regional")
		return
	}

	var req subnetworkRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "name required")
		return
	}

	// Subnetwork names are unique per region; a duplicate insert in the same
	// region must 409. Without this the shadow subnet leaks — get/delete resolve
	// only the first match, so the second one can never be reached or removed.
	if _, err := findSubnetByName(r.Context(), h.net, req.Name, rp.ScopeName); err == nil {
		gcprest.WriteError(w, http.StatusConflict, "alreadyExists",
			"subnetwork "+req.Name+" already exists")

		return
	}

	// Resolve the network self-link to a driver VPC ID.
	vpcID, err := resolveNetwork(r.Context(), h.net, req.Network)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	cfg := netdriver.SubnetConfig{
		VPCID:            vpcID,
		CIDRBlock:        req.IPCIDRRange,
		AvailabilityZone: rp.ScopeName,
		Tags:             subnetTags(&req),
	}

	if _, err := h.net.CreateSubnet(r.Context(), cfg); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeRegions, rp.ScopeName,
		"subnetworks", req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// subnetTags builds the tag set persisted for a subnetwork insert. purpose/
// stackType/privateIpGoogleAccess/description have no first-class driver slots,
// and secondaryIpRanges (GKE VPC-native / Terraform secondary_ip_range) is
// stored verbatim as JSON — mirroring the firewall spec — so alias ranges
// survive the round-trip.
func subnetTags(req *subnetworkRequest) map[string]string {
	tags := map[string]string{
		subnetNameTag:    req.Name,
		subnetNetworkTag: lastSegment(req.Network),
		createdAtTag:     nowRFC3339(),
	}

	if req.Purpose != "" {
		tags[subnetPurposeTag] = req.Purpose
	}

	if req.StackType != "" {
		tags[subnetStackTag] = req.StackType
	}

	if req.PrivateIPGoogleAccess != nil && *req.PrivateIPGoogleAccess {
		tags[subnetPGATag] = trueValue
	}

	if req.Description != "" {
		tags[subnetDescTag] = req.Description
	}

	if len(req.SecondaryIPRanges) > 0 {
		if b, err := json.Marshal(req.SecondaryIPRanges); err == nil {
			tags[subnetSecondaryTag] = string(b)
		}
	}

	return tags
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getSubnetwork(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	s, err := findSubnetByName(r.Context(), h.net, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toSubnetworkResponse(s, rp, hostOf(r)))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listSubnetworks(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	infos, err := h.net.DescribeSubnets(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostOf(r)
	filter := r.URL.Query().Get("filter")

	items := make([]subnetworkResponse, 0, len(infos))

	for i := range infos {
		// subnetworks.list is regional — only return subnets in this region.
		if rp.ScopeName != "" && infos[i].AvailabilityZone != rp.ScopeName {
			continue
		}

		scope := rp
		scope.ResourceName = tagOr(infos[i].Tags, subnetNameTag, infos[i].ID)

		resp := toSubnetworkResponse(&infos[i], scope, host)
		if nameMatches(filter, resp.Name) {
			items = append(items, resp)
		}
	}

	page, err := pagination.PaginateSorted(items,
		func(a, b subnetworkResponse) bool { return a.Name < b.Name },
		r.URL.Query().Get("pageToken"), maxResultsOf(r.URL.Query().Get("maxResults")))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, subnetworkListResponse{
		Kind:          "compute#subnetworkList",
		ID:            "projects/" + rp.Project + "/regions/" + rp.ScopeName + "/subnetworks",
		Items:         page.Items,
		NextPageToken: page.NextPageToken,
		SelfLink:      gcprest.SelfLink(host, rp.Project, gcprest.ScopeRegions, rp.ScopeName, "subnetworks", ""),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteSubnetwork(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	s, err := findSubnetByName(r.Context(), h.net, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// Real GCP refuses to delete a subnetwork that still has instances in it,
	// returning 400 resourceInUseByAnotherResource (mirrors the network delete
	// guard against live subnets above). Scan instances whose networkInterfaces
	// subnet references this subnet and reject; delete succeeds once empty.
	host := hostOf(r)
	if inst, scanErr := h.instanceInSubnet(r.Context(), host, rp.Project, rp.ResourceName, rp.ScopeName); scanErr != nil {
		gcprest.WriteCErr(w, scanErr)
		return
	} else if inst != "" {
		subnetLink := gcprest.SelfLink(host, rp.Project, gcprest.ScopeRegions, rp.ScopeName, "subnetworks", rp.ResourceName)
		gcprest.WriteError(w, http.StatusBadRequest, "resourceInUseByAnotherResource",
			"The subnetwork resource '"+subnetLink+"' is already being used by '"+inst+"'")

		return
	}

	if err := h.net.DeleteSubnet(r.Context(), s.ID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeRegions, rp.ScopeName,
		"subnetworks", rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// patchSubnetwork applies merge-patch semantics to the mutable subnetwork
// fields (privateIpGoogleAccess, secondaryIpRanges, description). GCP guards
// the patch with the resource fingerprint: a caller echoes the fingerprint it
// read, and a stale one (the subnet changed underneath it) is rejected 412.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) patchSubnetwork(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	s, err := findSubnetByName(r.Context(), h.net, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	var req subnetworkRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	name := tagOr(s.Tags, subnetNameTag, s.ID)
	if req.Fingerprint != "" && req.Fingerprint != fingerprintOf(name, s.CIDRBlock) {
		gcprest.WriteError(w, http.StatusPreconditionFailed, "conditionNotMet",
			"subnetwork fingerprint does not match the current resource")

		return
	}

	set, remove := subnetPatchTags(&req)

	if len(set) > 0 {
		if err := h.net.UpdateSubnetTags(r.Context(), s.ID, set); err != nil {
			gcprest.WriteCErr(w, err)
			return
		}
	}

	if len(remove) > 0 {
		if err := h.net.RemoveSubnetTags(r.Context(), s.ID, remove); err != nil {
			gcprest.WriteCErr(w, err)
			return
		}
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeRegions, rp.ScopeName,
		"subnetworks", rp.ResourceName, "patch")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// subnetPatchTags translates a subnetwork patch into the tag keys to set and
// the keys to remove. privateIpGoogleAccess=false clears its tag rather than
// storing a falsey marker, so the read path stays a simple presence check.
func subnetPatchTags(req *subnetworkRequest) (set map[string]string, remove []string) {
	set = map[string]string{}

	if req.PrivateIPGoogleAccess != nil {
		if *req.PrivateIPGoogleAccess {
			set[subnetPGATag] = trueValue
		} else {
			remove = append(remove, subnetPGATag)
		}
	}

	if len(req.SecondaryIPRanges) > 0 {
		if b, err := json.Marshal(req.SecondaryIPRanges); err == nil {
			set[subnetSecondaryTag] = string(b)
		}
	}

	if req.Description != "" {
		set[subnetDescTag] = req.Description
	}

	return set, remove
}

// subnetCIDRExpander is the optional provider capability for widening a
// subnetwork's primary IP range. Discovered by type assertion (mirroring
// VPCAttributes/SubnetAttributes); GCP's expandIpCidrRange has no cross-cloud
// analog, so it stays out of the portable Networking interface.
type subnetCIDRExpander interface {
	ExpandSubnetCIDR(ctx context.Context, id, cidr string) error
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) expandSubnetIPCIDR(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	s, err := findSubnetByName(r.Context(), h.net, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	var req expandIPCIDRRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.IPCIDRRange == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "ipCidrRange required")
		return
	}

	// expandIpCidrRange only grows the range: the new range must strictly
	// contain the current one. A subset (or equal) range is rejected.
	if err := validateSuperset(s.CIDRBlock, req.IPCIDRRange); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	expander, ok := h.net.(subnetCIDRExpander)
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented",
			"subnetwork range expansion not supported")

		return
	}

	if err := expander.ExpandSubnetCIDR(r.Context(), s.ID, req.IPCIDRRange); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeRegions, rp.ScopeName,
		"subnetworks", rp.ResourceName, expandAction)

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// validateSuperset reports whether expanded strictly contains current — the
// same-base, broader-prefix relationship GCP requires of expandIpCidrRange.
func validateSuperset(current, expanded string) error {
	_, curNet, err := net.ParseCIDR(current)
	if err != nil {
		return cerrors.Newf(cerrors.InvalidArgument, "current range %q is not a valid CIDR", current)
	}

	_, newNet, err := net.ParseCIDR(expanded)
	if err != nil {
		return cerrors.Newf(cerrors.InvalidArgument, "range %q is not a valid CIDR", expanded)
	}

	curOnes, _ := curNet.Mask.Size()
	newOnes, _ := newNet.Mask.Size()

	if newOnes >= curOnes || !newNet.Contains(curNet.IP) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"range %q must be a superset of the current range %q", expanded, current)
	}

	return nil
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) aggregatedListSubnetworks(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	infos, err := h.net.DescribeSubnets(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostOf(r)
	filter := r.URL.Query().Get("filter")
	items := map[string]subnetworksScopedList{}

	for i := range infos {
		region := infos[i].AvailabilityZone

		scope := rp
		scope.Scope = gcprest.ScopeRegions
		scope.ScopeName = region
		scope.ResourceName = tagOr(infos[i].Tags, subnetNameTag, infos[i].ID)

		resp := toSubnetworkResponse(&infos[i], scope, host)
		if !nameMatches(filter, resp.Name) {
			continue
		}

		key := "regions/" + region
		bucket := items[key]
		bucket.Subnetworks = append(bucket.Subnetworks, resp)
		items[key] = bucket
	}

	// Real GCP always includes a global bucket; subnetworks are regional, so it
	// carries a NO_RESULTS_ON_PAGE warning rather than any items.
	if _, ok := items[gcprest.ScopeGlobal]; !ok {
		items[gcprest.ScopeGlobal] = subnetworksScopedList{
			Warning: &scopedWarning{Code: noResultsOnPageStr, Message: "There are no results for scope 'global' on this page."},
		}
	}

	gcprest.WriteJSON(w, http.StatusOK, subnetworkAggregatedListResponse{
		Kind:     "compute#subnetworkAggregatedList",
		ID:       "projects/" + rp.Project + "/aggregated/subnetworks",
		Items:    items,
		SelfLink: host + "/compute/v1/projects/" + rp.Project + "/aggregated/subnetworks",
	})
}

// Firewall operations.

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertFirewall(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.Scope != gcprest.ScopeGlobal {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "firewalls are global")
		return
	}

	var req firewallRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "name required")
		return
	}

	// A firewall name is unique per project; a duplicate insert must 409 rather
	// than silently shadow the existing rule (get/delete would only ever resolve
	// the first match, leaking the shadow).
	if _, err := findFirewallByName(r.Context(), h.net, req.Name); err == nil {
		gcprest.WriteError(w, http.StatusConflict, "alreadyExists",
			"firewall "+req.Name+" already exists")

		return
	}

	// GCP stamps defaults a minimal firewall omits; populate them at insert so
	// the resource reads back with a concrete direction/priority.
	if req.Direction == "" {
		req.Direction = defaultFirewallDirection
	}

	if req.Priority == 0 {
		req.Priority = defaultFirewallPriority
	}

	// Firewalls map onto driver SecurityGroups; the driver requires a VPC ID.
	// A supplied network MUST exist — real GCP rejects a firewall insert that
	// references an unknown network. Propagate the resolve error (404) rather
	// than fabricating a phantom VPC that would then leak into networks.list.
	vpcID, err := resolveNetwork(r.Context(), h.net, req.Network)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if vpcID == "" {
		// No network supplied — GCP defaults to the project's network; use any
		// existing one or create the default.
		vpcs, _ := h.net.DescribeVPCs(r.Context(), nil)
		if len(vpcs) > 0 {
			vpcID = vpcs[0].ID
		} else {
			v, vErr := h.net.CreateVPC(r.Context(), netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
			if vErr != nil {
				gcprest.WriteCErr(w, vErr)
				return
			}

			vpcID = v.ID
		}
	}

	// The driver's SecurityGroup model can't express GCP's firewall shape
	// (allowed/denied/direction/priority/targetTags), so persist the rule spec
	// verbatim in a reserved tag and reconstruct it on read. Without this a
	// created firewall reads back with no rules.
	tags := map[string]string{firewallNameTag: req.Name, createdAtTag: nowRFC3339()}
	if spec := marshalFirewallSpec(&req); spec != "" {
		tags[firewallSpecTag] = spec
	}

	cfg := netdriver.SecurityGroupConfig{
		Name:        req.Name,
		Description: req.Description,
		VPCID:       vpcID,
		Tags:        tags,
	}

	if _, err := h.net.CreateSecurityGroup(r.Context(), cfg); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		"firewalls", req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getFirewall(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	f, err := findFirewallByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toFirewallResponse(f, rp, hostOf(r)))
}

//nolint:gocritic,dupl // rp is a request-scoped value; list-shape duplicates by-design across resources
func (h *Handler) listFirewalls(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	infos, err := h.net.DescribeSecurityGroups(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostOf(r)
	filter := r.URL.Query().Get("filter")

	items := make([]firewallResponse, 0, len(infos))

	for i := range infos {
		scope := rp
		scope.ResourceName = tagOr(infos[i].Tags, firewallNameTag, infos[i].ID)

		resp := toFirewallResponse(&infos[i], scope, host)
		if nameMatches(filter, resp.Name) {
			items = append(items, resp)
		}
	}

	page, err := pagination.PaginateSorted(items,
		func(a, b firewallResponse) bool { return a.Name < b.Name },
		r.URL.Query().Get("pageToken"), maxResultsOf(r.URL.Query().Get("maxResults")))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, firewallListResponse{
		Kind:          "compute#firewallList",
		ID:            "projects/" + rp.Project + "/global/firewalls",
		Items:         page.Items,
		NextPageToken: page.NextPageToken,
		SelfLink:      gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "firewalls", ""),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteFirewall(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	f, err := findFirewallByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := h.net.DeleteSecurityGroup(r.Context(), f.ID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		"firewalls", rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// patchFirewall applies GCP merge-patch semantics to the stored firewall spec:
// only fields present in the patch body overwrite the existing rule, so a
// caller adjusting one field (e.g. allowed) keeps everything else intact.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) patchFirewall(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	f, err := findFirewallByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	var req firewallRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	spec, _ := unmarshalFirewallSpec(f.Tags[firewallSpecTag])
	mergeFirewallPatch(&spec, &req)

	b, mErr := json.Marshal(spec)
	if mErr != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed firewall body")
		return
	}

	if err := h.net.UpdateSecurityGroupTags(r.Context(), f.ID,
		map[string]string{firewallSpecTag: string(b)}); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		"firewalls", rp.ResourceName, "patch")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// mergeFirewallPatch overwrites only the spec fields the patch actually
// carries. Scalars merge when non-empty; slices and pointers merge when
// present (non-nil), matching GCP's JSON merge-patch handling.
func mergeFirewallPatch(spec *firewallSpec, req *firewallRequest) {
	mergeFirewallScalars(spec, req)
	mergeFirewallCollections(spec, req)
}

// mergeFirewallScalars merges the scalar and single-value firewall fields.
func mergeFirewallScalars(spec *firewallSpec, req *firewallRequest) {
	if req.Network != "" {
		spec.Network = req.Network
	}

	if req.Direction != "" {
		spec.Direction = req.Direction
	}

	if req.Priority != 0 {
		spec.Priority = req.Priority
	}

	if req.LogConfig != nil {
		spec.LogConfig = req.LogConfig
	}

	if req.Disabled != nil {
		spec.Disabled = req.Disabled
	}
}

// mergeFirewallCollections merges the rule and list-valued firewall fields.
func mergeFirewallCollections(spec *firewallSpec, req *firewallRequest) {
	if req.Allowed != nil {
		spec.Allowed = req.Allowed
	}

	if req.Denied != nil {
		spec.Denied = req.Denied
	}

	if req.SourceRanges != nil {
		spec.SourceRanges = req.SourceRanges
	}

	if req.DestinationRanges != nil {
		spec.DestinationRanges = req.DestinationRanges
	}

	if req.SourceTags != nil {
		spec.SourceTags = req.SourceTags
	}

	if req.TargetTags != nil {
		spec.TargetTags = req.TargetTags
	}

	if req.SourceServiceAccounts != nil {
		spec.SourceServiceAccounts = req.SourceServiceAccounts
	}

	if req.TargetServiceAccounts != nil {
		spec.TargetServiceAccounts = req.TargetServiceAccounts
	}
}

// Lookup helpers.

func findNetByName(ctx context.Context, n netdriver.Networking, name string) (*netdriver.VPCInfo, error) {
	infos, err := n.DescribeVPCs(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, netNameTag, "") == name {
			return &infos[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "network %s not found", name)
}

// findSubnetByName resolves a subnetwork by name scoped to a region. Two
// subnetworks in different regions may share a name, so region must
// disambiguate (real GCP scopes subnetwork get/delete by region). An empty
// region matches on name alone.
func findSubnetByName(ctx context.Context, n netdriver.Networking, name, region string) (*netdriver.SubnetInfo, error) {
	infos, err := n.DescribeSubnets(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, subnetNameTag, "") != name {
			continue
		}

		if region != "" && infos[i].AvailabilityZone != region {
			continue
		}

		return &infos[i], nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "subnetwork %s not found", name)
}

// subnetOnNetwork returns the name of the first subnetwork attached to the
// given network (by driver VPC ID or by the stored network-name tag), or "" if
// none. It underpins the delete-in-use guard for networks.
func (h *Handler) subnetOnNetwork(ctx context.Context, vpcID, netName string) (string, error) {
	infos, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return "", err
	}

	for i := range infos {
		if infos[i].VPCID == vpcID || tagOr(infos[i].Tags, subnetNetworkTag, "") == netName {
			return tagOr(infos[i].Tags, subnetNameTag, infos[i].ID), nil
		}
	}

	return "", nil
}

// These mirror the tag keys the compute wire handler stamps on each instance
// (server/gcp/compute) so this handler can name an in-subnet instance in the
// delete-in-use error without importing the compute server package.
const (
	instNameTag = "cloudemu:gcpName"
	instZoneTag = "cloudemu:gcp:zone"
)

// instanceInSubnet returns the self-link of the first instance whose
// networkInterfaces subnet references the given subnet (by name, scoped to the
// subnet's region), or "" when none. It underpins the delete-in-use guard for
// subnetworks. A nil compute driver (compute not wired) reports no users.
func (h *Handler) instanceInSubnet(ctx context.Context, host, project, subnetName, region string) (string, error) {
	if h.compute == nil {
		return "", nil
	}

	instances, err := h.compute.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return "", err
	}

	for i := range instances {
		if !subnetRefMatches(instances[i].SubnetID, subnetName, region) {
			continue
		}

		name := tagOr(instances[i].Tags, instNameTag, instances[i].ID)
		zone := tagOr(instances[i].Tags, instZoneTag, "")

		return gcprest.SelfLink(host, project, gcprest.ScopeZones, zone, "instances", name), nil
	}

	return "", nil
}

// subnetRefMatches reports whether a raw instance subnet reference (a bare name,
// a relative path, or a full self-link of the form ".../regions/{r}/subnetworks/{n}")
// points at the subnet identified by name and region. When the ref carries a
// region segment it must match; a bare-name ref matches on name alone.
func subnetRefMatches(ref, name, region string) bool {
	if ref == "" || lastSegment(ref) != name {
		return false
	}

	if idx := strings.Index(ref, "/regions/"); idx >= 0 {
		rest := ref[idx+len("/regions/"):]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			return rest[:slash] == region
		}
	}

	return true
}

func findFirewallByName(ctx context.Context, n netdriver.Networking, name string) (*netdriver.SecurityGroupInfo, error) {
	infos, err := n.DescribeSecurityGroups(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, firewallNameTag, "") == name {
			return &infos[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "firewall %s not found", name)
}

// resolveNetwork translates a GCP network self-link or relative path into the
// driver-internal VPC ID. The SDK passes paths like
// "projects/{p}/global/networks/{name}" or full URLs.
func resolveNetwork(ctx context.Context, n netdriver.Networking, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}

	// Extract the trailing /networks/{name} segment if present.
	const marker = "/networks/"

	idx := -1

	for i := len(ref) - len(marker); i >= 0; i-- {
		if ref[i:i+len(marker)] == marker {
			idx = i
			break
		}
	}

	name := ref
	if idx >= 0 {
		name = ref[idx+len(marker):]
	}

	v, err := findNetByName(ctx, n, name)
	if err != nil {
		return "", err
	}

	return v.ID, nil
}

// Response shaping.

//nolint:gocritic // rp is a request-scoped value
func toNetworkResponse(info *netdriver.VPCInfo, rp gcprest.ResourcePath, host string) networkResponse {
	name := tagOr(info.Tags, netNameTag, info.ID)

	resp := networkResponse{
		Kind:                  "compute#network",
		ID:                    numericID(info.ID),
		Name:                  name,
		Description:           info.Tags[netDescTag],
		AutoCreateSubnetworks: info.Tags[autoSubnetTag] == trueValue,
		CreationTimestamp:     info.Tags[createdAtTag],
		SelfLink:              gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "networks", name),
	}

	if rm := info.Tags[netRoutingModeTag]; rm != "" {
		resp.RoutingConfig = &networkRoutingConfig{RoutingMode: rm}
	}

	if m := info.Tags[netMtuTag]; m != "" {
		if v, err := strconv.ParseInt(m, 10, 32); err == nil {
			resp.Mtu = int32(v)
		}
	}

	// IPv4Range belongs only to a legacy network; a modern auto/custom network
	// omits it (emitting it would wrongly read as legacy and conflict with
	// autoCreateSubnetworks).
	if info.Tags[legacyNetTag] == trueValue {
		resp.IPv4Range = info.CIDRBlock
	}

	return resp
}

// lastSegment returns the final path/URL segment (e.g. a network self-link or
// partial ref reduced to its bare name).
func lastSegment(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}

	return ref
}

//nolint:gocritic // rp is a request-scoped value
func toSubnetworkResponse(info *netdriver.SubnetInfo, rp gcprest.ResourcePath, host string) subnetworkResponse {
	name := tagOr(info.Tags, subnetNameTag, info.ID)
	region := rp.ScopeName

	if region == "" {
		region = info.AvailabilityZone
	}

	resp := subnetworkResponse{
		Kind:                  "compute#subnetwork",
		ID:                    numericID(info.ID),
		Name:                  name,
		IPCIDRRange:           info.CIDRBlock,
		Region:                host + "/compute/v1/projects/" + rp.Project + "/regions/" + region,
		SelfLink:              gcprest.SelfLink(host, rp.Project, gcprest.ScopeRegions, region, "subnetworks", name),
		GatewayAddress:        gatewayAddress(info.CIDRBlock),
		Purpose:               tagOr(info.Tags, subnetPurposeTag, defaultSubnetPurpose),
		StackType:             tagOr(info.Tags, subnetStackTag, defaultStackType),
		PrivateIPGoogleAccess: info.Tags[subnetPGATag] == trueValue,
		Description:           info.Tags[subnetDescTag],
		Fingerprint:           fingerprintOf(name, info.CIDRBlock),
		CreationTimestamp:     info.Tags[createdAtTag],
	}

	if s := info.Tags[subnetSecondaryTag]; s != "" {
		var ranges []secondaryRange
		if err := json.Unmarshal([]byte(s), &ranges); err == nil {
			resp.SecondaryIPRanges = ranges
		}
	}

	// Echo the parent network self-link so clients can discover a subnet's
	// network (real GCP always returns it).
	if net := info.Tags[subnetNetworkTag]; net != "" {
		resp.Network = gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "networks", net)
	}

	return resp
}

// gatewayAddress returns the first usable host of a CIDR — GCP assigns it as
// the subnet's default gateway. Returns "" for a malformed range.
func gatewayAddress(cidr string) string {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}

	base := ipNet.IP.To4()
	if base == nil {
		base = ipNet.IP.To16()
		if base == nil {
			return ""
		}
	}

	gw := append(net.IP(nil), base...)
	gw[len(gw)-1]++

	return gw.String()
}

// fingerprintOf returns a stable, non-empty base64 fingerprint. GCP requires
// the current fingerprint on patch/update calls, so a resource missing one
// cannot be modified.
func fingerprintOf(parts ...string) string {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}

	var b [8]byte

	binary.BigEndian.PutUint64(b[:], h.Sum64())

	return base64.StdEncoding.EncodeToString(b[:])
}

//nolint:gocritic // rp is a request-scoped value
func toFirewallResponse(info *netdriver.SecurityGroupInfo, rp gcprest.ResourcePath, host string) firewallResponse {
	name := tagOr(info.Tags, firewallNameTag, info.ID)

	resp := firewallResponse{
		Kind:              "compute#firewall",
		ID:                numericID(info.ID),
		Name:              name,
		Description:       info.Description,
		CreationTimestamp: info.Tags[createdAtTag],
		SelfLink:          gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "firewalls", name),
	}

	if spec, ok := unmarshalFirewallSpec(info.Tags[firewallSpecTag]); ok {
		resp.Network = spec.Network
		resp.Priority = spec.Priority
		resp.Direction = spec.Direction
		resp.Allowed = spec.Allowed
		resp.Denied = spec.Denied
		resp.SourceRanges = spec.SourceRanges
		resp.DestinationRanges = spec.DestinationRanges
		resp.SourceTags = spec.SourceTags
		resp.TargetTags = spec.TargetTags
		resp.SourceServiceAccounts = spec.SourceServiceAccounts
		resp.TargetServiceAccounts = spec.TargetServiceAccounts
		resp.LogConfig = spec.LogConfig
		resp.Disabled = spec.Disabled
	}

	return resp
}

// firewallSpec is the GCP firewall rule shape persisted verbatim (as JSON in a
// reserved tag) because the driver's SecurityGroup model can't express it.
type firewallSpec struct {
	Network               string             `json:"network,omitempty"`
	Priority              int                `json:"priority,omitempty"`
	Direction             string             `json:"direction,omitempty"`
	Allowed               []firewallRule     `json:"allowed,omitempty"`
	Denied                []firewallRule     `json:"denied,omitempty"`
	SourceRanges          []string           `json:"sourceRanges,omitempty"`
	DestinationRanges     []string           `json:"destinationRanges,omitempty"`
	SourceTags            []string           `json:"sourceTags,omitempty"`
	TargetTags            []string           `json:"targetTags,omitempty"`
	SourceServiceAccounts []string           `json:"sourceServiceAccounts,omitempty"`
	TargetServiceAccounts []string           `json:"targetServiceAccounts,omitempty"`
	LogConfig             *firewallLogConfig `json:"logConfig,omitempty"`
	Disabled              *bool              `json:"disabled,omitempty"`
}

func marshalFirewallSpec(req *firewallRequest) string {
	spec := specFromFirewallRequest(req)

	b, err := json.Marshal(spec)
	if err != nil {
		return ""
	}

	return string(b)
}

// specFromFirewallRequest projects a wire request onto the stored spec shape.
func specFromFirewallRequest(req *firewallRequest) firewallSpec {
	return firewallSpec{
		Network:               req.Network,
		Priority:              req.Priority,
		Direction:             req.Direction,
		Allowed:               req.Allowed,
		Denied:                req.Denied,
		SourceRanges:          req.SourceRanges,
		DestinationRanges:     req.DestinationRanges,
		SourceTags:            req.SourceTags,
		TargetTags:            req.TargetTags,
		SourceServiceAccounts: req.SourceServiceAccounts,
		TargetServiceAccounts: req.TargetServiceAccounts,
		LogConfig:             req.LogConfig,
		Disabled:              req.Disabled,
	}
}

func unmarshalFirewallSpec(s string) (firewallSpec, bool) {
	if s == "" {
		return firewallSpec{}, false
	}

	var spec firewallSpec
	if err := json.Unmarshal([]byte(s), &spec); err != nil {
		return firewallSpec{}, false
	}

	return spec, true
}

func tagOr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}

	return fallback
}

// numericID returns a stable uint64-shaped string derived from a driver ID.
// GCP wire IDs are uint64 and proto JSON unmarshalling rejects anything else.
func numericID(driverID string) string {
	const fnvOffset uint64 = 14695981039346656037

	const fnvPrime uint64 = 1099511628211

	h := fnvOffset
	for i := 0; i < len(driverID); i++ {
		h ^= uint64(driverID[i])
		h *= fnvPrime
	}

	return strconv.FormatUint(h, 10)
}

func hostOf(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}
