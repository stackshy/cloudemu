package savingsplans

import (
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Offering filter / property names. SavingsPlanOfferingFilterAttribute and
// SavingsPlanOfferingPropertyKey are the same closed enum: region |
// instanceFamily. The region name is the shared filterRegion constant.
// https://docs.aws.amazon.com/savingsplans/latest/APIReference/API_SavingsPlanOfferingFilterElement.html
const offeringPropInstanceFamily = "instanceFamily"

// propertyName / propertyValue are the SavingsPlanOfferingProperty member names.
const (
	propertyName  = "name"
	propertyValue = "value"
)

// offeringFilter is the parsed DescribeSavingsPlansOfferings predicate. Every
// populated dimension must match (AND across dimensions, OR within a list).
// https://docs.aws.amazon.com/savingsplans/latest/APIReference/API_DescribeSavingsPlansOfferings.html
type offeringFilter struct {
	ids            map[string]struct{}
	planTypes      map[string]struct{}
	paymentOptions map[string]struct{}
	durations      map[string]struct{}
	currencies     map[string]struct{}
	descriptions   map[string]struct{}
	serviceCodes   map[string]struct{}
	usageTypes     map[string]struct{}
	operations     map[string]struct{}
	productType    string
	attrs          []wireFilter
}

// validate rejects a filters[] name outside the closed
// SavingsPlanOfferingFilterAttribute enum with ValidationException, so a typo
// (e.g. "instancefamily") fails instead of silently matching every offering.
func (f *offeringFilter) validate() error {
	for _, attr := range f.attrs {
		if attr.Name != filterRegion && attr.Name != offeringPropInstanceFamily {
			return cerrors.Newf(cerrors.InvalidArgument,
				"invalid filter name %q: must be one of [region, instanceFamily]", attr.Name)
		}
	}

	return nil
}

// matches reports whether offering o satisfies the filter.
func (f *offeringFilter) matches(o *offering) bool {
	dims := []struct {
		set map[string]struct{}
		got string
	}{
		{f.ids, o.id},
		{f.planTypes, o.planType},
		{f.paymentOptions, o.paymentOption},
		{f.durations, strconv.FormatInt(o.durationSecs, 10)},
		{f.currencies, currencyUSD},
		{f.descriptions, o.description},
		{f.serviceCodes, o.serviceCode},
		{f.usageTypes, o.usageType},
		{f.operations, o.operation},
	}

	for _, d := range dims {
		if !inSet(d.set, d.got) {
			return false
		}
	}

	if f.productType != "" && !containsValue(o.productTypes, f.productType) {
		return false
	}

	for _, attr := range f.attrs {
		if !offeringAttrMatches(o, attr) {
			return false
		}
	}

	return true
}

// offeringAttrMatches applies one filters[] element. The filter names are the
// offering property keys, so an offering matches when it carries that property
// with one of the requested values; an offering without the property (e.g. a
// region-spanning Compute offering under a region filter) does not match.
// Filter names are checked by validate before matching.
func offeringAttrMatches(o *offering, attr wireFilter) bool {
	got, ok := offeringProperties(o)[attr.Name]

	return ok && containsValue(attr.Values, got)
}

// offeringProperties returns the SavingsPlanOfferingProperty set for o: region
// and instanceFamily for a region/family-scoped EC2Instance offering, none for
// region-spanning offerings.
func offeringProperties(o *offering) map[string]string {
	props := map[string]string{}

	if o.region != "" {
		props[filterRegion] = o.region
	}

	if o.ec2Family != "" {
		props[offeringPropInstanceFamily] = o.ec2Family
	}

	return props
}

// inSet reports whether v is in set; an empty set means the dimension is
// unfiltered.
func inSet(set map[string]struct{}, v string) bool {
	if len(set) == 0 {
		return true
	}

	_, ok := set[v]

	return ok
}

// int64sToStrings renders durations so they share the string-set matcher.
func int64sToStrings(in []int64) []string {
	if len(in) == 0 {
		return nil
	}

	out := make([]string, len(in))
	for i, v := range in {
		out[i] = strconv.FormatInt(v, 10)
	}

	return out
}
