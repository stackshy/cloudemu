package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// timeLayout is the RFC3339 layout EC2 uses for reservation timestamps.
const timeLayout = time.RFC3339

// Reserved Instance describe filter names. filterAvailabilityZone (volume.go)
// and filterState (networking_common.go) are shared with the other EC2 handlers.
const (
	riFilterProductDescription = "product-description"
	riFilterInstanceType       = "instance-type"
	riFilterScope              = "scope"
	riFilterInstanceTenancy    = "instance-tenancy"
	riFilterDuration           = "duration"
	riFilterOfferingClass      = "offering-class"
	riFilterOfferingType       = "offering-type"
	riFilterMarketplace        = "marketplace"
	riFilterClientToken        = "client-token"
)

// routeReservedInstances dispatches the Reserved Instance billing actions. It is
// additive: it claims only RI actions and returns false for everything else, so
// the rest of the EC2 handler is unaffected. When the handler was constructed
// without a clock/region (the RI store is always initialized, so this is never
// nil), the ops run against the real clock.
func (h *Handler) routeReservedInstances(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "DescribeReservedInstancesOfferings":
		h.describeReservedInstancesOfferings(w, r)
	case "PurchaseReservedInstancesOffering":
		h.purchaseReservedInstancesOffering(w, r)
	case "DescribeReservedInstances":
		h.describeReservedInstances(w, r)
	case "ModifyReservedInstances":
		h.modifyReservedInstances(w, r)
	case "DescribeReservedInstancesModifications":
		h.describeReservedInstancesModifications(w, r)
	default:
		return false
	}

	return true
}

// riFilter is the parsed DescribeReservedInstances predicate: an explicit id set
// plus attribute filters. A reservation matches when it satisfies every
// populated dimension (AND across dimensions, OR within a value list).
type riFilter struct {
	ids   map[string]struct{}
	attrs []awsquery.Filter
}

// matches reports whether ri satisfies the filter. state is the reservation's
// clock-derived effective state (passed in rather than read from a stored field)
// so the state filter matches the live lifecycle.
func (f riFilter) matches(ri *reservedInstance, state string) bool {
	if len(f.ids) > 0 {
		if _, ok := f.ids[ri.id]; !ok {
			return false
		}
	}

	for _, attr := range f.attrs {
		if !riAttrMatches(ri, state, attr) {
			return false
		}
	}

	return true
}

// riAttrMatches reports whether ri satisfies one attribute filter. An unknown
// filter name is tolerated (matches), mirroring the permissive real API.
func riAttrMatches(ri *reservedInstance, state string, attr awsquery.Filter) bool {
	var got string

	switch attr.Name {
	case filterAvailabilityZone:
		got = ri.availabilityZone
	case riFilterDuration:
		got = strconv.FormatInt(ri.duration, 10)
	case riFilterProductDescription:
		got = ri.productDescription
	case filterState:
		got = state
	case riFilterInstanceType:
		got = ri.instanceType
	case riFilterScope:
		got = ri.scope
	case riFilterInstanceTenancy:
		got = ri.instanceTenancy
	case riFilterOfferingClass:
		got = ri.offeringClass
	case riFilterOfferingType:
		got = ri.offeringType
	default:
		return true
	}

	return containsString(attr.Values, got)
}

// --- DescribeReservedInstancesOfferings -----------------------------------

func (h *Handler) describeReservedInstancesOfferings(w http.ResponseWriter, r *http.Request) {
	offeringIDs := toStringSet(awsquery.ListStrings(r.Form, "ReservedInstancesOfferingId"))
	instanceType := r.Form.Get("InstanceType")
	offeringClass := r.Form.Get("OfferingClass")
	offeringType := r.Form.Get("OfferingType")
	filters := awsquery.Filters(r.Form)

	out := make([]riOfferingXML, 0, len(h.ri.offerings))

	for i := range h.ri.offerings {
		o := &h.ri.offerings[i]

		if len(offeringIDs) > 0 {
			if _, ok := offeringIDs[o.id]; !ok {
				continue
			}
		}

		if !offeringMatches(o, instanceType, offeringClass, offeringType, filters) {
			continue
		}

		out = append(out, toRIOfferingXML(o))
	}

	awsquery.WriteXMLResponse(w, describeRIOfferingsResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Offerings: out,
	})
}

// offeringMatches applies the DescribeReservedInstancesOfferings scalar
// parameters and Filter.N predicates to one offering.
func offeringMatches(
	o *riOffering, instanceType, offeringClass, offeringType string, filters []awsquery.Filter,
) bool {
	if instanceType != "" && o.instanceType != instanceType {
		return false
	}

	if offeringClass != "" && o.offeringClass != offeringClass {
		return false
	}

	if offeringType != "" && o.offeringType != offeringType {
		return false
	}

	for _, filter := range filters {
		if !offeringAttrMatches(o, filter) {
			return false
		}
	}

	return true
}

func offeringAttrMatches(o *riOffering, filter awsquery.Filter) bool {
	var got string

	switch filter.Name {
	case riFilterInstanceType:
		got = o.instanceType
	case riFilterProductDescription:
		got = o.productDescription
	case filterAvailabilityZone:
		got = o.availabilityZone
	case riFilterScope:
		got = o.scope
	case riFilterMarketplace:
		got = strconv.FormatBool(o.marketplace)
	default:
		return true
	}

	return containsString(filter.Values, got)
}

// --- PurchaseReservedInstancesOffering ------------------------------------

func (h *Handler) purchaseReservedInstancesOffering(w http.ResponseWriter, r *http.Request) {
	in := &purchaseInput{
		offeringID:    r.Form.Get("ReservedInstancesOfferingId"),
		instanceCount: parseInt32(r.Form.Get("InstanceCount")),
	}

	if raw := r.Form.Get("LimitPrice.Amount"); raw != "" {
		if amt, err := strconv.ParseFloat(raw, 64); err == nil {
			in.limitPrice = &amt
		}
	}

	if raw := r.Form.Get("PurchaseTime"); raw != "" {
		if t, err := time.Parse(timeLayout, raw); err == nil {
			in.purchaseTime = t
		}
	}

	id, err := h.ri.purchase(in)
	if err != nil {
		writeRIErr(w, err)

		return
	}

	awsquery.WriteXMLResponse(w, purchaseRIResponse{
		Xmlns:               awsquery.Namespace,
		RequestID:           awsquery.RequestID,
		ReservedInstancesID: id,
	})
}

// --- DescribeReservedInstances --------------------------------------------

func (h *Handler) describeReservedInstances(w http.ResponseWriter, r *http.Request) {
	now := h.ri.clock.Now().UTC()
	f := riFilter{
		ids:   toStringSet(awsquery.ListStrings(r.Form, "ReservedInstancesId")),
		attrs: withScalarFilters(awsquery.Filters(r.Form), r.Form.Get("OfferingClass"), r.Form.Get("OfferingType")),
	}

	reservations := h.ri.describe(f, now)

	out := make([]reservedInstanceXML, 0, len(reservations))
	for _, ri := range reservations {
		out = append(out, toReservedInstanceXML(ri, ri.effectiveState(now)))
	}

	awsquery.WriteXMLResponse(w, describeRIResponse{
		Xmlns:             awsquery.Namespace,
		RequestID:         awsquery.RequestID,
		ReservedInstances: out,
	})
}

// withScalarFilters folds the OfferingClass/OfferingType scalar parameters into
// the Filter.N set as offering-class/offering-type predicates when supplied, so
// they filter through the one attribute-matching path.
func withScalarFilters(filters []awsquery.Filter, offeringClass, offeringType string) []awsquery.Filter {
	if offeringClass != "" {
		filters = append(filters, awsquery.Filter{Name: riFilterOfferingClass, Values: []string{offeringClass}})
	}

	if offeringType != "" {
		filters = append(filters, awsquery.Filter{Name: riFilterOfferingType, Values: []string{offeringType}})
	}

	return filters
}

// --- ModifyReservedInstances ----------------------------------------------

func (h *Handler) modifyReservedInstances(w http.ResponseWriter, r *http.Request) {
	in := &modifyInput{
		clientToken: r.Form.Get("ClientToken"),
		reservedIDs: awsquery.ListStrings(r.Form, "ReservedInstancesId"),
	}

	for _, idx := range awsquery.CollectIndices(r.Form, "ReservedInstancesConfigurationSetItemType") {
		in.targetConfigs = append(in.targetConfigs, parseTargetConfig(r, idx))
	}

	id, err := h.ri.modify(in)
	if err != nil {
		writeRIErr(w, err)

		return
	}

	awsquery.WriteXMLResponse(w, modifyRIResponse{
		Xmlns:                           awsquery.Namespace,
		RequestID:                       awsquery.RequestID,
		ReservedInstancesModificationID: id,
	})
}

// parseTargetConfig reads one ReservedInstancesConfigurationSetItemType.N group.
func parseTargetConfig(r *http.Request, idx int) targetConfig {
	base := "ReservedInstancesConfigurationSetItemType." + strconv.Itoa(idx)

	return targetConfig{
		availabilityZone: r.Form.Get(base + ".AvailabilityZone"),
		instanceType:     r.Form.Get(base + ".InstanceType"),
		platform:         r.Form.Get(base + ".Platform"),
		scope:            r.Form.Get(base + ".Scope"),
		instanceCount:    parseInt32(r.Form.Get(base + ".InstanceCount")),
	}
}

// parseInt32 parses a decimal string to int32, returning 0 on any parse error
// or out-of-range value (the caller treats 0 as "unset").
func parseInt32(s string) int32 {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0
	}

	return int32(n)
}

// --- DescribeReservedInstancesModifications --------------------------------

func (h *Handler) describeReservedInstancesModifications(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "ReservedInstancesModificationId")
	clientTokens := filterValues(awsquery.Filters(r.Form), riFilterClientToken)

	mods := h.ri.describeModifications(ids, clientTokens)

	out := make([]riModificationXML, 0, len(mods))
	for _, m := range mods {
		out = append(out, toRIModificationXML(m))
	}

	awsquery.WriteXMLResponse(w, describeRIModificationsResponse{
		Xmlns:         awsquery.Namespace,
		RequestID:     awsquery.RequestID,
		Modifications: out,
	})
}

// filterValues returns the values of the named filter across the filter set.
func filterValues(filters []awsquery.Filter, name string) []string {
	var out []string

	for _, f := range filters {
		if f.Name == name {
			out = append(out, f.Values...)
		}
	}

	return out
}

// writeRIErr maps a store/validation error to the closest EC2 RI error code.
func writeRIErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidReservedInstancesId.NotFound", "IncorrectReservedInstancesState")
}

// --- XML shapes ------------------------------------------------------------

type recurringChargeXML struct {
	Amount    float64 `xml:"amount"`
	Frequency string  `xml:"frequency"`
}

type pricingDetailXML struct {
	Count int32   `xml:"count"`
	Price float64 `xml:"price"`
}

type riOfferingXML struct {
	ReservedInstancesOfferingID string               `xml:"reservedInstancesOfferingId"`
	InstanceType                string               `xml:"instanceType"`
	AvailabilityZone            string               `xml:"availabilityZone,omitempty"`
	Duration                    int64                `xml:"duration"`
	FixedPrice                  float64              `xml:"fixedPrice"`
	UsagePrice                  float64              `xml:"usagePrice"`
	ProductDescription          string               `xml:"productDescription"`
	InstanceTenancy             string               `xml:"instanceTenancy"`
	CurrencyCode                string               `xml:"currencyCode"`
	OfferingType                string               `xml:"offeringType"`
	OfferingClass               string               `xml:"offeringClass"`
	Scope                       string               `xml:"scope"`
	Marketplace                 bool                 `xml:"marketplace"`
	RecurringCharges            []recurringChargeXML `xml:"recurringCharges>item,omitempty"`
	PricingDetails              []pricingDetailXML   `xml:"pricingDetailsSet>item,omitempty"`
}

type describeRIOfferingsResponse struct {
	XMLName   xml.Name        `xml:"DescribeReservedInstancesOfferingsResponse"`
	Xmlns     string          `xml:"xmlns,attr"`
	RequestID string          `xml:"requestId"`
	Offerings []riOfferingXML `xml:"reservedInstancesOfferingsSet>item"`
}

type purchaseRIResponse struct {
	XMLName             xml.Name `xml:"PurchaseReservedInstancesOfferingResponse"`
	Xmlns               string   `xml:"xmlns,attr"`
	RequestID           string   `xml:"requestId"`
	ReservedInstancesID string   `xml:"reservedInstancesId"`
}

type reservedInstanceXML struct {
	ReservedInstancesID string               `xml:"reservedInstancesId"`
	InstanceType        string               `xml:"instanceType"`
	AvailabilityZone    string               `xml:"availabilityZone,omitempty"`
	Start               string               `xml:"start"`
	End                 string               `xml:"end"`
	Duration            int64                `xml:"duration"`
	UsagePrice          float64              `xml:"usagePrice"`
	FixedPrice          float64              `xml:"fixedPrice"`
	InstanceCount       int32                `xml:"instanceCount"`
	ProductDescription  string               `xml:"productDescription"`
	State               string               `xml:"state"`
	InstanceTenancy     string               `xml:"instanceTenancy"`
	CurrencyCode        string               `xml:"currencyCode"`
	OfferingType        string               `xml:"offeringType"`
	OfferingClass       string               `xml:"offeringClass"`
	Scope               string               `xml:"scope"`
	RecurringCharges    []recurringChargeXML `xml:"recurringCharges>item,omitempty"`
	Tags                []tagItem            `xml:"tagSet>item,omitempty"`
}

type describeRIResponse struct {
	XMLName           xml.Name              `xml:"DescribeReservedInstancesResponse"`
	Xmlns             string                `xml:"xmlns,attr"`
	RequestID         string                `xml:"requestId"`
	ReservedInstances []reservedInstanceXML `xml:"reservedInstancesSet>item"`
}

type modifyRIResponse struct {
	XMLName                         xml.Name `xml:"ModifyReservedInstancesResponse"`
	Xmlns                           string   `xml:"xmlns,attr"`
	RequestID                       string   `xml:"requestId"`
	ReservedInstancesModificationID string   `xml:"reservedInstancesModificationId"`
}

type modificationResultXML struct {
	ReservedInstancesID string `xml:"reservedInstancesId,omitempty"`
	AvailabilityZone    string `xml:"targetConfiguration>availabilityZone,omitempty"`
	InstanceCount       int32  `xml:"targetConfiguration>instanceCount"`
	InstanceType        string `xml:"targetConfiguration>instanceType,omitempty"`
	Platform            string `xml:"targetConfiguration>platform,omitempty"`
	Scope               string `xml:"targetConfiguration>scope,omitempty"`
}

type riModificationXML struct {
	ReservedInstancesModificationID string                  `xml:"reservedInstancesModificationId"`
	ClientToken                     string                  `xml:"clientToken,omitempty"`
	Status                          string                  `xml:"status"`
	CreateDate                      string                  `xml:"createDate"`
	UpdateDate                      string                  `xml:"updateDate"`
	EffectiveDate                   string                  `xml:"effectiveDate"`
	ReservedInstancesIDs            []string                `xml:"reservedInstancesSet>item>reservedInstancesId"`
	ModificationResults             []modificationResultXML `xml:"modificationResultSet>item"`
}

type describeRIModificationsResponse struct {
	XMLName       xml.Name            `xml:"DescribeReservedInstancesModificationsResponse"`
	Xmlns         string              `xml:"xmlns,attr"`
	RequestID     string              `xml:"requestId"`
	Modifications []riModificationXML `xml:"reservedInstancesModificationsSet>item"`
}

// --- converters ------------------------------------------------------------

func toRIOfferingXML(o *riOffering) riOfferingXML {
	out := riOfferingXML{
		ReservedInstancesOfferingID: o.id,
		InstanceType:                o.instanceType,
		AvailabilityZone:            o.availabilityZone,
		Duration:                    o.duration,
		FixedPrice:                  o.fixedPrice,
		UsagePrice:                  o.usagePrice,
		ProductDescription:          o.productDescription,
		InstanceTenancy:             o.instanceTenancy,
		CurrencyCode:                riCurrencyUSD,
		OfferingType:                o.offeringType,
		OfferingClass:               o.offeringClass,
		Scope:                       o.scope,
		Marketplace:                 o.marketplace,
	}

	if o.recurringHourly > 0 {
		out.RecurringCharges = []recurringChargeXML{{Amount: o.recurringHourly, Frequency: recurringFrequencyHourly}}
	}

	for _, t := range o.pricingTiers {
		out.PricingDetails = append(out.PricingDetails, pricingDetailXML{Count: t.count, Price: t.price})
	}

	return out
}

func toReservedInstanceXML(ri *reservedInstance, state string) reservedInstanceXML {
	out := reservedInstanceXML{
		ReservedInstancesID: ri.id,
		InstanceType:        ri.instanceType,
		AvailabilityZone:    ri.availabilityZone,
		Start:               ri.start.Format(timeLayout),
		End:                 ri.end.Format(timeLayout),
		Duration:            ri.duration,
		UsagePrice:          ri.usagePrice,
		FixedPrice:          ri.fixedPrice,
		InstanceCount:       ri.instanceCount,
		ProductDescription:  ri.productDescription,
		State:               state,
		InstanceTenancy:     ri.instanceTenancy,
		CurrencyCode:        ri.currencyCode,
		OfferingType:        ri.offeringType,
		OfferingClass:       ri.offeringClass,
		Scope:               ri.scope,
	}

	if ri.recurringHourly > 0 {
		out.RecurringCharges = []recurringChargeXML{{Amount: ri.recurringHourly, Frequency: recurringFrequencyHourly}}
	}

	for k, v := range ri.tags {
		out.Tags = append(out.Tags, tagItem{Key: k, Value: v})
	}

	return out
}

func toRIModificationXML(m *riModification) riModificationXML {
	out := riModificationXML{
		ReservedInstancesModificationID: m.id,
		ClientToken:                     m.clientToken,
		Status:                          m.status,
		CreateDate:                      m.createDate.Format(timeLayout),
		UpdateDate:                      m.updateDate.Format(timeLayout),
		EffectiveDate:                   m.effectiveDate.Format(timeLayout),
		ReservedInstancesIDs:            m.sourceIDs,
	}

	for _, res := range m.results {
		out.ModificationResults = append(out.ModificationResults, modificationResultXML{
			ReservedInstancesID: res.reservedInstancesID,
			AvailabilityZone:    res.availabilityZone,
			InstanceCount:       res.instanceCount,
			InstanceType:        res.instanceType,
			Platform:            res.platform,
			Scope:               res.scope,
		})
	}

	return out
}
