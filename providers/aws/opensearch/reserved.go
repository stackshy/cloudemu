package opensearch

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

const (
	riDurationSeconds = 31536000 // one year
	riFixedPrice      = 100.0
	riUsagePrice      = 0.0
)

func copyReserved(r *driver.ReservedInstance) driver.ReservedInstance { return *r }

// reservedOfferings is the synthesized catalog of reserved-instance offerings.
func reservedOfferings() []map[string]json.RawMessage {
	types := []string{"m6g.large.search", "r6g.large.search", "c6g.large.search"}

	out := make([]map[string]json.RawMessage, 0, len(types))
	for _, t := range types {
		out = append(out, map[string]json.RawMessage{
			"ReservedInstanceOfferingId": rawString(idgen.GenerateID("ri-offering-")),
			"InstanceType":               rawString(t),
			"Duration":                   rawInt(riDurationSeconds),
			"FixedPrice":                 rawFloat(riFixedPrice),
			"UsagePrice":                 rawFloat(riUsagePrice),
			"CurrencyCode":               rawString("USD"),
			"PaymentOption":              rawString("ALL_UPFRONT"),
			"RecurringCharges":           json.RawMessage("[]"),
		})
	}

	return out
}

// PurchaseReservedInstanceOffering purchases a reserved instance offering,
// returning the reservation ID and name.
func (m *Mock) PurchaseReservedInstanceOffering(
	_ context.Context, offeringID, reservationName string, instanceCount int32,
) (reservationID, reservationName2 string, err error) {
	if offeringID == "" || reservationName == "" {
		return "", "", validation("ReservedInstanceOfferingId and ReservationName are required")
	}

	if instanceCount <= 0 {
		instanceCount = 1
	}

	id := idgen.GenerateID("ri-")
	ri := &driver.ReservedInstance{
		ReservationName:            reservationName,
		ReservedInstanceID:         id,
		ReservedInstanceOfferingID: offeringID,
		InstanceType:               "m6g.large.search",
		InstanceCount:              instanceCount,
		Duration:                   riDurationSeconds,
		FixedPrice:                 riFixedPrice,
		UsagePrice:                 riUsagePrice,
		CurrencyCode:               "USD",
		PaymentOption:              "ALL_UPFRONT",
		State:                      "payment-pending",
		StartTime:                  m.now(),
	}
	m.reserved.Set(id, ri)

	return id, reservationName, nil
}

// DescribeReservedInstances lists purchased reserved instances, sorted by ID.
func (m *Mock) DescribeReservedInstances(_ context.Context, reservedInstanceID string,
	page driver.Page,
) ([]driver.ReservedInstance, string, error) {
	ids := m.reserved.Keys()
	sort.Strings(ids)

	out := make([]driver.ReservedInstance, 0, len(ids))

	for _, id := range ids {
		if reservedInstanceID != "" && id != reservedInstanceID {
			continue
		}

		if ri, ok := m.reserved.Get(id); ok {
			out = append(out, copyReserved(ri))
		}
	}

	start, end, next := paginate(len(out), page)

	return out[start:end], next, nil
}

// DescribeReservedInstanceOfferings returns the synthesized offering catalog.
func (*Mock) DescribeReservedInstanceOfferings(
	_ context.Context, offeringID string, page driver.Page,
) (offerings []map[string]json.RawMessage, nextToken string, err error) {
	all := reservedOfferings()

	if offeringID != "" {
		// A specific offering ID never matches the freshly minted catalog IDs,
		// so return an empty set rather than a spurious match.
		all = []map[string]json.RawMessage{}
	}

	start, end, next := paginate(len(all), page)

	return all[start:end], next, nil
}
