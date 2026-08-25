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
	subnetPurposeTag    = "cloudemu:gcpSubnetPurpose"
	subnetStackTag      = "cloudemu:gcpSubnetStack"
	subnetPGATag        = "cloudemu:gcpSubnetPGA"
	firewallNameTag     = "cloudemu:gcpFwName"
	firewallSpecTag     = "cloudemu:gcpFwSpec"
	trueValue           = "true"

	defaultSubnetPurpose = "PRIVATE"
	defaultStackType     = "IPV4_ONLY"
	internalNetCIDR      = "10.0.0.0/16"
)

// defaultListMax is GCP's default page size when maxResults is absent.
const defaultListMax = 500

// nowRFC3339 returns the current time formatted the way GCP stamps
// creationTimestamp on every resource.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// nameMatches reports whether name satisfies a GCP list filter. Only the
// common single-clause "name (=|!=|eq|ne) value" form is supported; any other
// filter (or none) matches everything.
func nameMatches(filter, name string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}

	for _, cand := range []string{"!=", "=", " ne ", " eq "} {
		idx := strings.Index(filter, cand)
		if idx < 0 {
			continue
		}

		field := strings.TrimSpace(filter[:idx])
		if field != "name" {
			return true
		}

		value := strings.Trim(strings.TrimSpace(filter[idx+len(cand):]), `"'`)
		op := strings.TrimSpace(cand)
		negate := op == "!=" || op == "ne"

		return (name == value) != negate
	}

	return true
}

// maxResultsOf parses the maxResults query param, defaulting when absent/invalid.
func maxResultsOf(raw string) int {
	if raw == "" {
		return defaultListMax
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultListMax
	}

	return n
}

// Handler serves the GCP networking REST surface.
type Handler struct {
	net       netdriver.Networking
	routers   *routerStore
	addresses *addressStore
	routes    *routeStore
}

// New returns a networks handler.
func New(n netdriver.Networking) *Handler {
	return &Handler{
		net:       n,
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

	// Aggregated-scope requests (aggregatedList) are not implemented for these
	// networking resources; leave them unmatched so the dispatcher's default
	// applies rather than mis-serving them as a scoped list.
	if rp.Scope == gcprest.ScopeAggregated {
		return false
	}

	switch rp.ResourceType {
	case resourceNetworks, resourceSubnetworks, resourceFirewalls,
		resourceRouters, resourceAddresses, resourceRoutes:
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
	case http.MethodDelete:
		h.deleteNetwork(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD route shape is duplicate-by-design across resource types
func (h *Handler) routeSubnetworks(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
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

	switch r.Method {
	case http.MethodGet:
		h.getSubnetwork(w, r, rp)
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

	// A network with an explicit IPv4Range is a (deprecated) legacy network;
	// only those carry an IPv4Range on the wire. Auto/custom-mode networks must
	// NOT report one — the driver still needs a non-empty CIDR internally, so a
	// placeholder is stored but hidden from the response unless legacy.
	cidr := internalNetCIDR
	tags := map[string]string{netNameTag: req.Name, createdAtTag: nowRFC3339()}

	if req.IPv4Range != "" {
		cidr = req.IPv4Range
		tags[legacyNetTag] = trueValue
	}

	if req.AutoCreateSubnetworks != nil && *req.AutoCreateSubnetworks {
		tags[autoSubnetTag] = trueValue
	}

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

	// Resolve the network self-link to a driver VPC ID.
	vpcID, err := resolveNetwork(r.Context(), h.net, req.Network)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

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

	cfg := netdriver.SubnetConfig{
		VPCID:            vpcID,
		CIDRBlock:        req.IPCIDRRange,
		AvailabilityZone: rp.ScopeName,
		Tags:             tags,
	}

	if _, err := h.net.CreateSubnet(r.Context(), cfg); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeRegions, rp.ScopeName,
		"subnetworks", req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
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

	if err := h.net.DeleteSubnet(r.Context(), s.ID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeRegions, rp.ScopeName,
		"subnetworks", rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
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

	// Firewalls map onto driver SecurityGroups; the driver requires a VPC ID.
	vpcID, _ := resolveNetwork(r.Context(), h.net, req.Network)

	if vpcID == "" {
		// No network supplied — use any existing or create a synthetic.
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
		AutoCreateSubnetworks: info.Tags[autoSubnetTag] == trueValue,
		CreationTimestamp:     info.Tags[createdAtTag],
		SelfLink:              gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "networks", name),
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
		Fingerprint:           fingerprintOf(name, info.CIDRBlock),
		CreationTimestamp:     info.Tags[createdAtTag],
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
		resp.TargetTags = spec.TargetTags
	}

	return resp
}

// firewallSpec is the GCP firewall rule shape persisted verbatim (as JSON in a
// reserved tag) because the driver's SecurityGroup model can't express it.
type firewallSpec struct {
	Network      string         `json:"network,omitempty"`
	Priority     int            `json:"priority,omitempty"`
	Direction    string         `json:"direction,omitempty"`
	Allowed      []firewallRule `json:"allowed,omitempty"`
	Denied       []firewallRule `json:"denied,omitempty"`
	SourceRanges []string       `json:"sourceRanges,omitempty"`
	TargetTags   []string       `json:"targetTags,omitempty"`
}

func marshalFirewallSpec(req *firewallRequest) string {
	spec := firewallSpec{
		Network:      req.Network,
		Priority:     req.Priority,
		Direction:    req.Direction,
		Allowed:      req.Allowed,
		Denied:       req.Denied,
		SourceRanges: req.SourceRanges,
		TargetTags:   req.TargetTags,
	}

	b, err := json.Marshal(spec)
	if err != nil {
		return ""
	}

	return string(b)
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
