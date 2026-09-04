package savingsplans

import (
	"strconv"
	"time"
)

// createInput is the decoded, normalized CreateSavingsPlan request.
type createInput struct {
	savingsPlanOfferingID string
	commitment            string
	clientToken           string
	purchaseTime          time.Time
	upfrontPaymentAmount  string
	tags                  map[string]string
}

// wireFilter is a Savings Plans filter as it arrives on the wire
// ({"name": "...", "values": [...]}). Both DescribeSavingsPlans and the
// offering/rate describes share this element shape.
type wireFilter struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Filter names DescribeSavingsPlans understands (SavingsPlansFilterName). Others
// are tolerated and ignored, matching the permissive real API.
const (
	filterRegion        = "region"
	filterEC2Family     = "ec2-instance-family"
	filterInstanceFam   = "instance-family"
	filterCommitment    = "commitment"
	filterUpfront       = "upfront"
	filterTerm          = "term"
	filterSavingsType   = "savings-plan-type"
	filterPaymentOption = "payment-option"
)

// planFilter is the parsed DescribeSavingsPlans predicate: explicit id/ARN/state
// sets plus attribute filters. A plan matches when it satisfies every populated
// dimension (AND across dimensions, OR within a dimension's value list).
type planFilter struct {
	ids    map[string]struct{}
	arns   map[string]struct{}
	states map[string]struct{}
	attrs  []wireFilter
}

func newPlanFilter(ids, arns, states []string, filters []wireFilter) planFilter {
	return planFilter{
		ids:    toSet(ids),
		arns:   toSet(arns),
		states: toSet(states),
		attrs:  filters,
	}
}

// matches reports whether plan p satisfies the filter. state is the plan's
// clock-derived effective state (passed in rather than read from p.State) so the
// states[] dimension filters on the live lifecycle, not the value frozen at
// creation.
func (f planFilter) matches(p *savingsPlan, state string) bool {
	if len(f.ids) > 0 {
		if _, ok := f.ids[p.ID]; !ok {
			return false
		}
	}

	if len(f.arns) > 0 {
		if _, ok := f.arns[p.ARN]; !ok {
			return false
		}
	}

	if len(f.states) > 0 {
		if _, ok := f.states[state]; !ok {
			return false
		}
	}

	for _, attr := range f.attrs {
		if !attrMatches(p, attr) {
			return false
		}
	}

	return true
}

// attrMatches reports whether plan p satisfies one attribute filter (its value
// is among the filter's values). An unknown filter name is tolerated (matches).
func attrMatches(p *savingsPlan, attr wireFilter) bool {
	var got string

	switch attr.Name {
	case filterRegion:
		got = p.Region
	case filterEC2Family, filterInstanceFam:
		got = p.EC2Family
	case filterCommitment:
		got = p.Commitment
	case filterUpfront:
		got = p.Upfront
	case filterTerm:
		got = strconv.FormatInt(p.TermSeconds, 10)
	case filterSavingsType:
		got = p.PlanType
	case filterPaymentOption:
		got = p.PaymentOption
	default:
		return true
	}

	return containsValue(attr.Values, got)
}

func containsValue(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}

	return false
}

func toSet(items []string) map[string]struct{} {
	if len(items) == 0 {
		return nil
	}

	out := make(map[string]struct{}, len(items))
	for _, i := range items {
		out[i] = struct{}{}
	}

	return out
}

// planToWire renders a Savings Plan in the wire (SavingsPlan) JSON shape the
// aws-sdk-go-v2 client deserializes. Times are RFC3339 strings, matching AWS.
func planToWire(p *savingsPlan) map[string]any {
	out := map[string]any{
		"savingsPlanId":         p.ID,
		"savingsPlanArn":        p.ARN,
		"offeringId":            p.OfferingID,
		"commitment":            p.Commitment,
		"currency":              p.Currency,
		"savingsPlanType":       p.PlanType,
		"paymentOption":         p.PaymentOption,
		"productTypes":          p.ProductTypes,
		"region":                p.Region,
		"description":           p.Description,
		"termDurationInSeconds": p.TermSeconds,
		"start":                 p.Start.Format(time.RFC3339),
		"end":                   p.End.Format(time.RFC3339),
		"state":                 p.State,
	}

	if p.EC2Family != "" {
		out["ec2InstanceFamily"] = p.EC2Family
	}

	if p.Upfront != "" {
		out["upfrontPaymentAmount"] = p.Upfront
	}

	if p.Recurring != "" {
		out["recurringPaymentAmount"] = p.Recurring
	}

	if len(p.Tags) > 0 {
		out["tags"] = p.Tags
	}

	return out
}

// offeringToWire renders a catalog offering in the wire (SavingsPlanOffering)
// JSON shape.
func offeringToWire(o *offering) map[string]any {
	return map[string]any{
		"offeringId":      o.id,
		"description":     o.description,
		"durationSeconds": o.durationSecs,
		"paymentOption":   o.paymentOption,
		"planType":        o.planType,
		"productTypes":    o.productTypes,
		"currency":        currencyUSD,
		"serviceCode":     o.serviceCode,
		"usageType":       o.usageType,
		"operation":       o.operation,
	}
}
