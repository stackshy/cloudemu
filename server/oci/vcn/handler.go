// Package vcn implements OCI's Core Networking REST API against a CloudEmu
// networking driver. Real github.com/oracle/oci-go-sdk core clients hit this
// handler the same way they hit iaas.<region>.oraclecloud.com.
//
// The /20160918 prefix is the Core Services API version, shared with Compute
// and Block Volume, so Matches claims only the networking collections:
//
//	POST/GET             /20160918/vcns                    — create, list
//	GET/PUT/DELETE       /20160918/vcns/{id}               — get, update, delete
//	POST                 /20160918/vcns/{id}/actions/{addVcnCidr,removeVcnCidr}
//	POST                 /20160918/{collection}/{id}/actions/changeCompartment
//	POST/GET             /20160918/subnets                 — create, list (by vcnId)
//	GET/PUT/DELETE       /20160918/subnets/{id}
//	POST/GET             /20160918/networkSecurityGroups   — create, list
//	GET/PUT/DELETE       /20160918/networkSecurityGroups/{id}
//	GET                  /20160918/networkSecurityGroups/{id}/securityRules
//	POST                 /20160918/networkSecurityGroups/{id}/securityRules/actions/{add,remove}SecurityRules
//	GET                  /20160918/networkSecurityGroups/{id}/vnics
//	POST/GET/PUT/DELETE  /20160918/securityLists[/{id}]
//	POST/GET/PUT/DELETE  /20160918/routeTables[/{id}]
//	POST/GET/PUT/DELETE  /20160918/internetGateways[/{id}]
//	POST/GET/PUT/DELETE  /20160918/natGateways[/{id}]
//	POST/GET/PUT/DELETE  /20160918/serviceGateways[/{id}]
//	POST/GET/PUT/DELETE  /20160918/dhcps[/{id}]
//	GET/PUT              /20160918/vnics/{id}
//	POST/GET/PUT/DELETE  /20160918/privateIps[/{id}]
//	POST/GET/PUT/DELETE  /20160918/publicIps[/{id}]
//	POST/GET/PUT/DELETE  /20160918/localPeeringGateways[/{id}]
//	POST                 /20160918/localPeeringGateways/{id}/actions/connect
//
// Not emulated: /drgs, /drgAttachments and /remotePeeringConnections, which
// the networking driver has no shape for — the handler claims them anyway so a
// caller gets a 501 naming the gap rather than a bare 404 — and
// /vcns/{id}/actions/modifyVcnCidr, which addVcnCidr and removeVcnCidr cover
// between them. Resources report AVAILABLE from the moment they are created:
// every CloudEmu mutation is synchronous, so the PROVISIONING and TERMINATING
// states an SDK waiter may poll for are never observable.
package vcn

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	vcnprovider "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// apiVersion is the Core Services API version every networking path carries.
const apiVersion = "20160918"

// Collections this handler claims.
const (
	segVCNs             = "vcns"
	segSubnets          = "subnets"
	segNSGs             = "networkSecurityGroups"
	segSecurityLists    = "securityLists"
	segRouteTables      = "routeTables"
	segInternetGateways = "internetGateways"
	segNATGateways      = "natGateways"
	segServiceGateways  = "serviceGateways"
	segDHCPOptions      = "dhcps"
	segVNICs            = "vnics"
	segPrivateIPs       = "privateIps"
	segPublicIPs        = "publicIps"
	segLPGs             = "localPeeringGateways"
)

// Collections this handler claims in order to report them as unemulated, so a
// caller reaching for one is told why rather than left with a bare 404.
const (
	segDRGs           = "drgs"
	segDRGAttachments = "drgAttachments"
	segRemotePeerings = "remotePeeringConnections"
)

// Sub-collections and actions.
const (
	subActions       = "actions"
	subSecurityRules = "securityRules"
	subVNICs         = "vnics"

	actionChangeCompartment = "changeCompartment"
	actionAddRules          = "addSecurityRules"
	actionRemoveRules       = "removeSecurityRules"
	actionUpdateRules       = "updateSecurityRules"
	actionAddVCNCIDR        = "addVcnCidr"
	actionRemoveVCNCIDR     = "removeVcnCidr"
	actionConnect           = "connect"
)

// Error codes the handler raises itself.
const (
	codeInvalidParameter = "InvalidParameter"
	codeMethodNotAllowed = "MethodNotAllowed"
	codeNotImplemented   = "NotImplemented"
	codeNotFound         = "NotAuthorizedOrNotFound"
)

// maxPathSegments is /{version}/{collection}/{id}/{sub}/{action}.
const maxPathSegments = 5

// Extras is the OCI-only surface the portable networking driver cannot
// express: compartments, creation times, a VCN's default resources, DHCP
// options, private IPs and OCI's replace-style updates.
// *providers/oci/vcn.Mock satisfies it; any driver that does not is served
// 501 for every path this handler claims.
type Extras interface {
	Scope(id string) scope.Scope
	SetScope(id string, s scope.Scope)
	Created(id string) string
	Defaults(vcnID string) vcnprovider.DefaultResources
	SetTags(id string, tags map[string]string) error

	VCNCIDRs(vcnID string) []string
	AddVCNCIDR(ctx context.Context, vcnID, cidr string) error
	RemoveVCNCIDR(ctx context.Context, vcnID, cidr string) error

	ReplaceRoutes(ctx context.Context, routeTableID string, routes []netdriver.Route) error
	ReplaceNetworkACLRules(ctx context.Context, aclID string, rules []netdriver.NetworkACLRule) error

	CreateDHCPOptions(
		ctx context.Context, vcnID, name, serverType string, customDNS, searchDomains []string,
	) (*vcnprovider.DHCPOptions, error)
	DeleteDHCPOptions(ctx context.Context, id string) error
	DescribeDHCPOptions(ctx context.Context, ids []string) ([]vcnprovider.DHCPOptions, error)
	UpdateDHCPOptions(
		ctx context.Context, id string, name *string, serverType string, customDNS, searchDomains []string,
	) (*vcnprovider.DHCPOptions, error)

	CreateLocalPeeringGateway(
		ctx context.Context, vcnID string, tags map[string]string,
	) (*vcnprovider.LocalPeeringGateway, error)
	DeleteLocalPeeringGateway(ctx context.Context, id string) error
	DescribeLocalPeeringGateways(ctx context.Context, ids []string) ([]vcnprovider.LocalPeeringGateway, error)
	ConnectLocalPeeringGateways(ctx context.Context, id, peerID string) error

	DescribeVNICs(ctx context.Context, ids []string) ([]vcnprovider.VNIC, error)
	UpdateVNIC(ctx context.Context, id string, name, hostname *string, nsgIDs []string) (*vcnprovider.VNIC, error)
	VNICsInNSG(ctx context.Context, nsgID string) ([]vcnprovider.VNIC, error)

	CreatePrivateIP(ctx context.Context, vnicID, address, name, hostname string) (*vcnprovider.PrivateIP, error)
	DeletePrivateIP(ctx context.Context, id string) error
	DescribePrivateIPs(ctx context.Context, ids []string) ([]vcnprovider.PrivateIP, error)
	UpdatePrivateIP(ctx context.Context, id string, name, hostname *string) (*vcnprovider.PrivateIP, error)
}

// Handler serves OCI Core Networking against a networking driver.
type Handler struct {
	net    netdriver.Networking
	extras Extras
	work   *workrequest.Store
}

// New returns a VCN handler. work records the asynchronous compartment moves;
// a nil store leaves those paths unserved.
func New(n netdriver.Networking, work *workrequest.Store) *Handler {
	extras, _ := n.(Extras)

	return &Handler{net: n, extras: extras, work: work}
}

// route is a parsed Core Networking path.
type route struct {
	Collection string
	ID         string
	Sub        string
	Action     string
}

// Matches claims the networking collections under /20160918, and nothing else
// sharing that prefix.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		return false
	}

	switch rt.Collection {
	case segVCNs, segSubnets, segNSGs, segSecurityLists, segRouteTables,
		segInternetGateways, segNATGateways, segServiceGateways,
		segDHCPOptions, segVNICs, segPrivateIPs, segPublicIPs, segLPGs,
		segDRGs, segDRGAttachments, segRemotePeerings:
		return true
	}

	return false
}

// ServeHTTP routes on collection, then on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "malformed networking path")
		return
	}

	if h.extras == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"the wired networking driver does not implement OCI compartments")

		return
	}

	if rt.Sub == subActions && rt.Action == actionChangeCompartment {
		h.changeCompartment(w, r, rt.ID)
		return
	}

	h.serveCollection(w, r, rt)
}

// serveCollection dispatches to the handler family for rt's collection.
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch rt.Collection {
	case segVCNs:
		h.serveVCN(w, r, rt)
	case segSubnets:
		serveCRUD(w, r, rt, h.subnetOps())
	case segNSGs:
		h.serveNSG(w, r, rt)
	case segSecurityLists:
		serveCRUD(w, r, rt, h.securityListOps())
	case segRouteTables:
		serveCRUD(w, r, rt, h.routeTableOps())
	case segDHCPOptions:
		serveCRUD(w, r, rt, h.dhcpOps())
	case segVNICs:
		h.serveVNIC(w, r, rt)
	case segPrivateIPs:
		serveCRUD(w, r, rt, h.privateIPOps())
	case segPublicIPs:
		serveCRUD(w, r, rt, h.publicIPOps())
	default:
		h.serveGatewayCollection(w, r, rt)
	}
}

// serveGatewayCollection dispatches the gateway collections, and reports the
// ones CloudEmu does not emulate.
func (h *Handler) serveGatewayCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch rt.Collection {
	case segInternetGateways:
		serveCRUD(w, r, rt, h.internetGatewayOps())
	case segNATGateways:
		serveCRUD(w, r, rt, h.natGatewayOps())
	case segServiceGateways:
		serveCRUD(w, r, rt, h.serviceGatewayOps())
	case segLPGs:
		h.serveLPG(w, r, rt)
	case segDRGs, segDRGAttachments, segRemotePeerings:
		unemulated(w, r, rt.Collection)
	default:
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown collection "+rt.Collection)
	}
}

// unemulated reports a collection the handler claims but cannot serve. The
// networking driver models no dynamic routing gateway, so a DRG, its
// attachments and the remote peering connections riding on it would be shapes
// with nothing behind them; local peering gateways carry the VCN-to-VCN
// traffic CloudEmu does model.
func unemulated(w http.ResponseWriter, r *http.Request, collection string) {
	ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
		collection+" is not emulated; use localPeeringGateways for VCN-to-VCN connectivity")
}

// crud is one collection's five operations.
type crud struct {
	create func(w http.ResponseWriter, r *http.Request)
	list   func(w http.ResponseWriter, r *http.Request)
	get    func(w http.ResponseWriter, r *http.Request, id string)
	update func(w http.ResponseWriter, r *http.Request, id string)
	remove func(w http.ResponseWriter, r *http.Request, id string)
}

// serveCRUD maps method and path shape onto a collection's operations.
func serveCRUD(w http.ResponseWriter, r *http.Request, rt route, c crud) {
	if rt.ID == "" {
		switch r.Method {
		case http.MethodPost:
			dispatch(w, r, c.create)
		case http.MethodGet:
			dispatch(w, r, c.list)
		default:
			methodNotAllowed(w, r)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		dispatchID(w, r, rt.ID, c.get)
	case http.MethodPut:
		dispatchID(w, r, rt.ID, c.update)
	case http.MethodDelete:
		dispatchID(w, r, rt.ID, c.remove)
	default:
		methodNotAllowed(w, r)
	}
}

// dispatch calls op, or reports the operation as unsupported for the collection.
func dispatch(w http.ResponseWriter, r *http.Request, op func(http.ResponseWriter, *http.Request)) {
	if op == nil {
		methodNotAllowed(w, r)
		return
	}

	op(w, r)
}

// dispatchID is dispatch for the operations addressing a single resource.
func dispatchID(
	w http.ResponseWriter, r *http.Request, id string, op func(http.ResponseWriter, *http.Request, string),
) {
	if op == nil {
		methodNotAllowed(w, r)
		return
	}

	op(w, r, id)
}

// changeCompartment moves a resource between compartments, the one Core
// Networking mutation real OCI runs asynchronously.
func (h *Handler) changeCompartment(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	if h.work == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented, "work requests are not configured")
		return
	}

	var req changeCompartmentRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if h.extras.Created(id) == "" {
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "resource "+id+" not found")
		return
	}

	h.extras.SetScope(id, scope.Scope{Compartment: req.CompartmentID})

	entity := ocidType(id)
	wrID := h.work.Accept("CHANGE_"+strings.ToUpper(entity)+"_COMPARTMENT", req.CompartmentID, workrequest.Resource{
		EntityType: entity,
		ActionType: workrequest.ActionUpdated,
		Identifier: id,
	})

	ocirest.SetWorkRequestID(w, wrID)
	ocirest.WriteJSON(w, r, http.StatusAccepted, nil)
}

// place records the compartment a create call named and returns it.
func (h *Handler) place(id, compartmentID string) {
	h.extras.SetScope(id, scope.Scope{Compartment: compartmentID})
}

// inCompartment reports whether a resource is visible under a compartment filter.
func (h *Handler) inCompartment(id, compartmentID string) bool {
	return h.extras.Scope(id).Matches(scope.Scope{Compartment: compartmentID})
}

// compartmentOf returns the compartment a resource lives in.
func (h *Handler) compartmentOf(id string) string {
	return h.extras.Scope(id).Compartment
}

// parsePath splits /{version}/{collection}[/{id}[/{sub}[/{action}]]].
func parsePath(urlPath string) (route, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 2 || len(parts) > maxPathSegments || parts[0] != apiVersion {
		return route{}, false
	}

	rt := route{Collection: parts[1]}

	if len(parts) > 2 { //nolint:mnd // the id follows the collection
		rt.ID = parts[2]
	}

	if len(parts) > 3 { //nolint:mnd // then the sub-collection
		rt.Sub = parts[3]
	}

	if len(parts) > 4 { //nolint:mnd // then the action on it
		rt.Action = parts[4]
	}

	return rt, true
}

// paginate applies OCI's limit and opaque page cursor, stamping the cursor for
// the next page. The cursor is the offset the next page starts at.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) []T {
	start := 0

	if token := ocirest.Page(r); token != "" {
		if n, err := strconv.Atoi(token); err == nil && n > 0 {
			start = n
		}
	}

	// items[:0] rather than nil: an empty page is [] on the wire, not null.
	if start >= len(items) {
		return items[:0]
	}

	end := min(start+ocirest.Limit(r), len(items))
	if end < len(items) {
		ocirest.SetNextPage(w, strconv.Itoa(end))
	}

	return items[start:end]
}

// methodNotAllowed is the response for a verb a collection does not serve.
func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	ocirest.WriteError(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed, "method not allowed")
}

// scopedList filters a driver listing to the caller's compartment and, when
// the query names one, to a single VCN, then writes the page. key returns a
// resource's own OCID and the VCN owning it; an empty owner opts out of the
// vcnId filter, which the VCN collection itself has no use for.
func scopedList[T, R any](
	h *Handler, w http.ResponseWriter, r *http.Request, compartmentID string,
	items []T, key func(*T) (id, owner string), render func(*T) R,
) {
	vcnID := r.URL.Query().Get("vcnId")
	out := make([]R, 0, len(items))

	for i := range items {
		id, owner := key(&items[i])
		if !h.inCompartment(id, compartmentID) {
			continue
		}

		if vcnID != "" && owner != "" && owner != vcnID {
			continue
		}

		out = append(out, render(&items[i]))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, paginate(w, r, out))
}
