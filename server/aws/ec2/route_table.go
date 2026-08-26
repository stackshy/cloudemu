package ec2

import (
	"encoding/xml"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Route target types understood by the driver; mirror the AWS route-target
// taxonomy.
const (
	targetTypeGateway    = "gateway"
	targetTypeNatGateway = "nat-gateway"
	targetTypePeering    = "peering"
	targetTypeLocal      = "local"

	// routeOriginCreateRouteTable is the origin AWS reports for the implicit
	// local route created with the table; routeOriginCreateRoute is what it
	// reports for routes a caller added afterward.
	routeOriginCreateRouteTable = "CreateRouteTable"
	routeOriginCreateRoute      = "CreateRoute"
)

type routeXML struct {
	DestinationCIDR      string `xml:"destinationCidrBlock"`
	GatewayID            string `xml:"gatewayId,omitempty"`
	NatGatewayID         string `xml:"natGatewayId,omitempty"`
	VpcPeeringConnection string `xml:"vpcPeeringConnectionId,omitempty"`
	State                string `xml:"state"`
	Origin               string `xml:"origin,omitempty"`
}

type rtAssociationStateXML struct {
	State string `xml:"state"`
}

type rtAssociationXML struct {
	AssociationID    string                `xml:"routeTableAssociationId"`
	RouteTableID     string                `xml:"routeTableId"`
	SubnetID         string                `xml:"subnetId,omitempty"`
	Main             bool                  `xml:"main"`
	AssociationState rtAssociationStateXML `xml:"associationState"`
}

type routeTableXML struct {
	RouteTableID string             `xml:"routeTableId"`
	VpcID        string             `xml:"vpcId"`
	OwnerID      string             `xml:"ownerId"`
	Routes       []routeXML         `xml:"routeSet>item,omitempty"`
	Associations []rtAssociationXML `xml:"associationSet>item,omitempty"`
	Tags         []tagItem          `xml:"tagSet>item,omitempty"`
}

type createRouteTableResponseXML struct {
	XMLName    xml.Name      `xml:"CreateRouteTableResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	RequestID  string        `xml:"requestId"`
	RouteTable routeTableXML `xml:"routeTable"`
}

type describeRouteTablesResponseXML struct {
	XMLName       xml.Name        `xml:"DescribeRouteTablesResponse"`
	Xmlns         string          `xml:"xmlns,attr"`
	RequestID     string          `xml:"requestId"`
	RouteTableSet []routeTableXML `xml:"routeTableSet>item"`
	NextToken     string          `xml:"nextToken,omitempty"`
}

type createRouteResponseXML struct {
	XMLName   xml.Name `xml:"CreateRouteResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type deleteRouteResponseXML struct {
	XMLName   xml.Name `xml:"DeleteRouteResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type replaceRouteResponseXML struct {
	XMLName   xml.Name `xml:"ReplaceRouteResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type deleteRouteTableResponseXML struct {
	XMLName   xml.Name `xml:"DeleteRouteTableResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type associateRouteTableResponseXML struct {
	XMLName          xml.Name              `xml:"AssociateRouteTableResponse"`
	Xmlns            string                `xml:"xmlns,attr"`
	RequestID        string                `xml:"requestId"`
	AssociationID    string                `xml:"associationId"`
	AssociationState rtAssociationStateXML `xml:"associationState"`
}

type disassociateRouteTableResponseXML struct {
	XMLName   xml.Name `xml:"DisassociateRouteTableResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

func (h *Handler) createRouteTable(w http.ResponseWriter, r *http.Request) {
	cfg := netdriver.RouteTableConfig{
		VPCID: r.Form.Get("VpcId"),
		Tags:  mergeTagSpecs(awsquery.TagSpecs(r.Form), "route-table"),
	}

	rt, err := h.vpc.CreateRouteTable(r.Context(), cfg)
	if err != nil {
		writeRouteTableErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createRouteTableResponseXML{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		RouteTable: toRouteTableXML(rt),
	})
}

func (h *Handler) describeRouteTables(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "RouteTableId")

	rts, err := h.vpc.DescribeRouteTables(r.Context(), ids)
	if err != nil {
		writeRouteTableErr(w, err)
		return
	}

	filters := awsquery.Filters(r.Form)
	if err := validateRouteTableFilters(filters); err != nil {
		writeRouteTableErr(w, err)
		return
	}

	out := make([]routeTableXML, 0, len(rts))
	for i := range rts {
		if !routeTableMatchesFilters(&rts[i], filters) {
			continue
		}

		out = append(out, toRouteTableXML(&rts[i]))
	}

	page, next := pageNetworkingXML(out, r, func(rt routeTableXML) string { return rt.RouteTableID })

	awsquery.WriteXMLResponse(w, describeRouteTablesResponseXML{
		Xmlns:         awsquery.Namespace,
		RequestID:     awsquery.RequestID,
		RouteTableSet: page,
		NextToken:     next,
	})
}

func (h *Handler) createRoute(w http.ResponseWriter, r *http.Request) {
	// Real EC2 accepts many target types; gateway, NAT gateway and peering are
	// wired. Anything else is a 400 rather than a silently dropped route.
	target, targetType := resolveRouteTarget(r)
	if target == "" {
		writeRouteTableErr(w, newInvalidParameterErr(
			"one of GatewayId / NatGatewayId / VpcPeeringConnectionId is required"))

		return
	}

	err := h.vpc.CreateRoute(r.Context(),
		r.Form.Get("RouteTableId"),
		r.Form.Get("DestinationCidrBlock"),
		target, targetType)
	if err != nil {
		writeCreateRouteErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createRouteResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// replaceRoute swaps the target of an existing route, keyed by its destination
// CIDR. Real EC2 requires the route to already exist (a miss is
// InvalidRoute.NotFound), so it is modeled as DeleteRoute-then-CreateRoute: the
// delete step's NotFound is exactly the "route must exist" precondition, and the
// create step re-adds it with the new target.
func (h *Handler) replaceRoute(w http.ResponseWriter, r *http.Request) {
	target, targetType := resolveRouteTarget(r)
	if target == "" {
		writeReplaceRouteErr(w, newInvalidParameterErr(
			"one of GatewayId / NatGatewayId / VpcPeeringConnectionId is required"))

		return
	}

	routeTableID := r.Form.Get("RouteTableId")
	destinationCIDR := r.Form.Get("DestinationCidrBlock")

	// A missing route TABLE is InvalidRouteTableID.NotFound; a missing ROUTE on an
	// existing table is InvalidRoute.NotFound. DeleteRoute conflates the two, so
	// resolve the table first.
	if rts, _ := h.vpc.DescribeRouteTables(r.Context(), []string{routeTableID}); len(rts) == 0 {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidRouteTableID.NotFound",
			"The route table ID '"+routeTableID+"' does not exist")

		return
	}

	if err := h.vpc.DeleteRoute(r.Context(), routeTableID, destinationCIDR); err != nil {
		writeReplaceRouteErr(w, err)
		return
	}

	if err := h.vpc.CreateRoute(r.Context(), routeTableID, destinationCIDR, target, targetType); err != nil {
		writeReplaceRouteErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, replaceRouteResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	err := h.vpc.DeleteRoute(r.Context(),
		r.Form.Get("RouteTableId"), r.Form.Get("DestinationCidrBlock"))
	if err != nil {
		writeDeleteRouteErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteRouteResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) deleteRouteTable(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.DeleteRouteTable(r.Context(), r.Form.Get("RouteTableId")); err != nil {
		writeRouteTableErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteRouteTableResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) associateRouteTable(w http.ResponseWriter, r *http.Request) {
	assoc, err := h.vpc.AssociateRouteTable(r.Context(),
		r.Form.Get("RouteTableId"), r.Form.Get("SubnetId"))
	if err != nil {
		writeRouteTableErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, associateRouteTableResponseXML{
		Xmlns:            awsquery.Namespace,
		RequestID:        awsquery.RequestID,
		AssociationID:    assoc.ID,
		AssociationState: rtAssociationStateXML{State: "associated"},
	})
}

func (h *Handler) disassociateRouteTable(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.DisassociateRouteTable(r.Context(), r.Form.Get("AssociationId")); err != nil {
		writeRouteTableErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, disassociateRouteTableResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// resolveRouteTarget picks the first non-empty target the caller supplied and
// maps it to the driver's target-type string. Transit gateways and the rarer
// target types are not modeled.
func resolveRouteTarget(r *http.Request) (target, targetType string) {
	if id := r.Form.Get("GatewayId"); id != "" {
		return id, targetTypeGateway
	}

	if id := r.Form.Get("NatGatewayId"); id != "" {
		return id, targetTypeNatGateway
	}

	if id := r.Form.Get("VpcPeeringConnectionId"); id != "" {
		return id, targetTypePeering
	}

	return "", ""
}

// validateRouteTableFilters rejects filter names this emulator does not model.
// Like validateENIFilters, an explicit InvalidParameterValue is safer than
// silently matching nothing: an unrecognized filter that returned an empty set
// could tell a caller a route table is already gone and let it proceed to a VPC
// delete that then fails with DependencyViolation.
func validateRouteTableFilters(filters []awsquery.Filter) error {
	for _, f := range filters {
		if !routeTableFilterKnown(f.Name) {
			return newInvalidParameterErr("The filter '" + f.Name + "' is invalid")
		}
	}

	return nil
}

// routeTableFilterKnown lists the filters routeTableMatchesFilter implements:
// keep the two in sync, or a "known" filter would validate and then silently
// match nothing.
func routeTableFilterKnown(name string) bool {
	switch name {
	case "route-table-id", "vpc-id",
		"association.route-table-association-id", "association.route-table-id",
		filterAssocSubnetID, "association.main":
		return true
	default:
		return false
	}
}

// routeTableMatchesFilters reports whether a route table satisfies every
// DescribeRouteTables filter. Terraform's route-table-association waiter filters
// by association.route-table-association-id and expects exactly one table back;
// without honoring the filter the handler returns every table and the provider's
// single-result assertion fails, hanging the association until it times out.
// Filter names are validated up front, so an unknown name never reaches here.
func routeTableMatchesFilters(rt *netdriver.RouteTable, filters []awsquery.Filter) bool {
	for _, f := range filters {
		if !routeTableMatchesFilter(rt, f) {
			return false
		}
	}

	return true
}

func routeTableMatchesFilter(rt *netdriver.RouteTable, f awsquery.Filter) bool {
	switch f.Name {
	case "route-table-id":
		return containsString(f.Values, rt.ID)
	case "vpc-id":
		return containsString(f.Values, rt.VPCID)
	case "association.route-table-association-id":
		return anyAssoc(rt, func(a netdriver.RouteTableAssociation) bool { return containsString(f.Values, a.ID) })
	case "association.route-table-id":
		return anyAssoc(rt, func(a netdriver.RouteTableAssociation) bool {
			return containsString(f.Values, nonEmpty(a.RouteTableID, rt.ID))
		})
	case filterAssocSubnetID:
		return anyAssoc(rt, func(a netdriver.RouteTableAssociation) bool { return containsString(f.Values, a.SubnetID) })
	case "association.main":
		return anyAssoc(rt, func(a netdriver.RouteTableAssociation) bool {
			return containsString(f.Values, boolFilterValue(a.Main))
		})
	default:
		// Unknown filter: match nothing rather than hand back tables the caller
		// did not ask for.
		return false
	}
}

func boolFilterValue(b bool) string {
	if b {
		return "true"
	}

	return "false"
}

func anyAssoc(rt *netdriver.RouteTable, pred func(netdriver.RouteTableAssociation) bool) bool {
	for _, a := range rt.Associations {
		if pred(a) {
			return true
		}
	}

	return false
}

func toRouteTableXML(rt *netdriver.RouteTable) routeTableXML {
	x := routeTableXML{
		RouteTableID: rt.ID,
		VpcID:        rt.VPCID,
		OwnerID:      ownerID,
		Tags:         toTagItems(rt.Tags),
	}

	for _, a := range rt.Associations {
		x.Associations = append(x.Associations, rtAssociationXML{
			AssociationID:    a.ID,
			RouteTableID:     nonEmpty(a.RouteTableID, rt.ID),
			SubnetID:         a.SubnetID,
			Main:             a.Main,
			AssociationState: rtAssociationStateXML{State: "associated"},
		})
	}

	for _, route := range rt.Routes {
		rx := routeXML{
			DestinationCIDR: route.DestinationCIDR,
			State:           nonEmpty(route.State, "active"),
			Origin:          routeOrigin(route.TargetType),
		}

		switch route.TargetType {
		case targetTypeGateway:
			rx.GatewayID = route.TargetID
		case targetTypeNatGateway:
			rx.NatGatewayID = route.TargetID
		case targetTypePeering:
			rx.VpcPeeringConnection = route.TargetID
		case targetTypeLocal:
			// The VPC-local route reports gatewayId "local", not the internal
			// target id the driver stores.
			rx.GatewayID = targetTypeLocal
		}

		x.Routes = append(x.Routes, rx)
	}

	return x
}

// routeOrigin reports how a route was created: the implicit local route comes
// from CreateRouteTable, every other target type is a route a caller added.
func routeOrigin(targetType string) string {
	if targetType == targetTypeLocal {
		return routeOriginCreateRouteTable
	}

	return routeOriginCreateRoute
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}

	return s
}

func writeRouteTableErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidRouteTableID.NotFound", "DependencyViolation")
}

// writeCreateRouteErr maps CreateRoute's resource-specific errors: a destination
// CIDR that already exists is RouteAlreadyExists (not the generic
// ResourceAlreadyExists), and a route pointing at a gateway / NAT / peering that
// does not exist maps to that target's not-found code. A missing route table
// still falls through to InvalidRouteTableID.NotFound.
func writeCreateRouteErr(w http.ResponseWriter, err error) {
	if cerrors.IsAlreadyExists(err) {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "RouteAlreadyExists", cerrors.Message(err))
		return
	}

	if code, ok := routeTargetNotFoundCode(err); ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, code, cerrors.Message(err))
		return
	}

	writeRouteTableErr(w, err)
}

// writeDeleteRouteErr maps a miss on an existing route table to
// InvalidRoute.NotFound (the route is what's absent), while a missing route table
// still maps to InvalidRouteTableID.NotFound. The provider's message
// disambiguates the two ("not found in route table" is the route miss).
func writeDeleteRouteErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) && strings.Contains(err.Error(), "not found in route table") {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidRoute.NotFound", cerrors.Message(err))
		return
	}

	writeRouteTableErr(w, err)
}

// writeReplaceRouteErr maps a NotFound to InvalidRoute.NotFound: ReplaceRoute's
// precondition is that the route already exists, so the delete-step miss reports
// the missing route, matching real EC2, rather than the route-table code. A
// re-create pointing at a nonexistent target still maps to that target's code.
func writeReplaceRouteErr(w http.ResponseWriter, err error) {
	if code, ok := routeTargetNotFoundCode(err); ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, code, cerrors.Message(err))
		return
	}

	writeErrWithNotFound(w, err, "InvalidRoute.NotFound", "DependencyViolation")
}

// routeTargetNotFoundCode reports the target-specific EC2 error code a CreateRoute
// carries when its gateway / NAT / peering target does not exist. The provider
// marks each with the code as a message prefix.
func routeTargetNotFoundCode(err error) (string, bool) {
	if !cerrors.IsNotFound(err) {
		return "", false
	}

	for _, code := range []string{
		"InvalidInternetGatewayID.NotFound",
		"InvalidNatGatewayID.NotFound",
		"InvalidVpcPeeringConnectionID.NotFound",
	} {
		if strings.Contains(err.Error(), code+":") {
			return code, true
		}
	}

	return "", false
}
