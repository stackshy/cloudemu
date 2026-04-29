// Package networks implements the GCP Compute Engine networking REST API
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
package networks

import (
	"context"
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/errors"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

const (
	resourceNetworks    = "networks"
	resourceSubnetworks = "subnetworks"
	resourceFirewalls   = "firewalls"
	netNameTag          = "cloudemu:gcpNetName"
	subnetNameTag       = "cloudemu:gcpSubnetName"
	firewallNameTag     = "cloudemu:gcpFwName"
)

// Handler serves the GCP networking REST surface.
type Handler struct {
	net netdriver.Networking
}

// New returns a networks handler.
func New(n netdriver.Networking) *Handler {
	return &Handler{net: n}
}

// Matches returns true for /compute/v1/.../networks|subnetworks|firewalls URLs.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := gcprest.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	switch rp.ResourceType {
	case resourceNetworks, resourceSubnetworks, resourceFirewalls:
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

	cidr := "10.0.0.0/16"
	if req.IPv4Range != "" {
		cidr = req.IPv4Range
	}

	cfg := netdriver.VPCConfig{
		CIDRBlock: cidr,
		Tags:      map[string]string{netNameTag: req.Name},
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
	out := networkListResponse{
		Kind:     "compute#networkList",
		ID:       "projects/" + rp.Project + "/global/networks",
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "networks", ""),
	}

	for i := range infos {
		scope := rp
		scope.ResourceName = tagOr(infos[i].Tags, netNameTag, infos[i].ID)
		out.Items = append(out.Items, toNetworkResponse(&infos[i], scope, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteNetwork(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	v, err := findNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
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

	cfg := netdriver.SubnetConfig{
		VPCID:            vpcID,
		CIDRBlock:        req.IPCIDRRange,
		AvailabilityZone: rp.ScopeName,
		Tags:             map[string]string{subnetNameTag: req.Name},
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
	s, err := findSubnetByName(r.Context(), h.net, rp.ResourceName)
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
	out := subnetworkListResponse{
		Kind:     "compute#subnetworkList",
		ID:       "projects/" + rp.Project + "/regions/" + rp.ScopeName + "/subnetworks",
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeRegions, rp.ScopeName, "subnetworks", ""),
	}

	for i := range infos {
		scope := rp
		scope.ResourceName = tagOr(infos[i].Tags, subnetNameTag, infos[i].ID)
		out.Items = append(out.Items, toSubnetworkResponse(&infos[i], scope, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteSubnetwork(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	s, err := findSubnetByName(r.Context(), h.net, rp.ResourceName)
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

	cfg := netdriver.SecurityGroupConfig{
		Name:        req.Name,
		Description: req.Description,
		VPCID:       vpcID,
		Tags:        map[string]string{firewallNameTag: req.Name},
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
	out := firewallListResponse{
		Kind:     "compute#firewallList",
		ID:       "projects/" + rp.Project + "/global/firewalls",
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "firewalls", ""),
	}

	for i := range infos {
		scope := rp
		scope.ResourceName = tagOr(infos[i].Tags, firewallNameTag, infos[i].ID)
		out.Items = append(out.Items, toFirewallResponse(&infos[i], scope, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
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

func findSubnetByName(ctx context.Context, n netdriver.Networking, name string) (*netdriver.SubnetInfo, error) {
	infos, err := n.DescribeSubnets(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, subnetNameTag, "") == name {
			return &infos[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "subnetwork %s not found", name)
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

	return networkResponse{
		Kind:                  "compute#network",
		ID:                    numericID(info.ID),
		Name:                  name,
		IPv4Range:             info.CIDRBlock,
		AutoCreateSubnetworks: false,
		SelfLink:              gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "networks", name),
	}
}

//nolint:gocritic // rp is a request-scoped value
func toSubnetworkResponse(info *netdriver.SubnetInfo, rp gcprest.ResourcePath, host string) subnetworkResponse {
	name := tagOr(info.Tags, subnetNameTag, info.ID)
	region := rp.ScopeName

	if region == "" {
		region = info.AvailabilityZone
	}

	return subnetworkResponse{
		Kind:        "compute#subnetwork",
		ID:          numericID(info.ID),
		Name:        name,
		IPCIDRRange: info.CIDRBlock,
		Region:      host + "/compute/v1/projects/" + rp.Project + "/regions/" + region,
		SelfLink:    gcprest.SelfLink(host, rp.Project, gcprest.ScopeRegions, region, "subnetworks", name),
	}
}

//nolint:gocritic // rp is a request-scoped value
func toFirewallResponse(info *netdriver.SecurityGroupInfo, rp gcprest.ResourcePath, host string) firewallResponse {
	name := tagOr(info.Tags, firewallNameTag, info.ID)

	return firewallResponse{
		Kind:        "compute#firewall",
		ID:          numericID(info.ID),
		Name:        name,
		Description: info.Description,
		SelfLink:    gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "firewalls", name),
	}
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
