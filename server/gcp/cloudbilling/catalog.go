package cloudbilling

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// Seeded identifiers. The billing-account id follows GCP's
// XXXXXX-XXXXXX-XXXXXX shape; the service ids mirror real Cloud Billing
// catalog ids for Compute Engine and Cloud Storage.
const (
	seedAccountID    = "012345-567890-ABCDEF"
	computeServiceID = "6F81-5844-456A"
	storageServiceID = "95FF-2EF5-5EA1"

	// Seeded USD unit prices, in nano-units per usage unit (1 unit = 1 USD).
	computeCoreHourNanos = 21181818 // ~$0.0212 per vCPU-hour
	storageGiBMonthNanos = 20000000 // $0.020 per GiB-month
)

// seed installs one open billing account and a small service/SKU catalog so
// list/get calls return representative data out of the box.
func (h *Handler) seed() {
	h.accounts[seedAccountID] = &billingAccount{
		Name:         billingAccountsName + seedAccountID,
		Open:         true,
		DisplayName:  "CloudEmu Billing Account",
		CurrencyCode: "USD",
	}

	h.services = []*service{
		{
			Name:               "services/" + computeServiceID,
			ServiceID:          computeServiceID,
			DisplayName:        "Compute Engine",
			BusinessEntityName: "businessEntities/GCP",
		},
		{
			Name:               "services/" + storageServiceID,
			ServiceID:          storageServiceID,
			DisplayName:        "Cloud Storage",
			BusinessEntityName: "businessEntities/GCP",
		},
	}

	h.skus[computeServiceID] = []*sku{
		newSKU(computeServiceID, "D041-B8A1-6E0B", "N1 Predefined Instance Core running in Americas",
			"h", "hour", 0, computeCoreHourNanos),
	}
	h.skus[storageServiceID] = []*sku{
		newSKU(storageServiceID, "E5F0-6A5D-7BAD", "Standard Storage US Regional",
			"GiBy.mo", "gibibyte month", 0, storageGiBMonthNanos),
	}
}

// newSKU builds a seeded SKU priced at units + nanos per usage unit in USD.
func newSKU(serviceID, skuID, desc, usageUnit, usageUnitDesc string, units, nanos int64) *sku {
	return &sku{
		Name:                "services/" + serviceID + "/skus/" + skuID,
		SkuID:               skuID,
		Description:         desc,
		ServiceProviderName: "Google",
		ServiceRegions:      []string{"us-central1"},
		PricingInfo: []pricingInfo{{
			Summary:                "",
			CurrencyConversionRate: 1,
			PricingExpression: &pricingExpression{
				UsageUnit:            usageUnit,
				UsageUnitDescription: usageUnitDesc,
				BaseUnit:             usageUnit,
				DisplayQuantity:      1,
				TieredRates: []tieredRate{{
					StartUsageAmount: 0,
					UnitPrice:        &money{CurrencyCode: "USD", Units: units, Nanos: nanos},
				}},
			},
		}},
	}
}

// serveServices dispatches /v1/services.
func (h *Handler) serveServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeUnsupported(w)
		return
	}

	h.mu.RLock()
	all := append([]*service(nil), h.services...)
	h.mu.RUnlock()

	page, next := paginate(all, r)
	gcprest.WriteJSON(w, http.StatusOK, listServicesResponse{Services: page, NextPageToken: next})
}

// serveSkus dispatches /v1/services/{service}/skus.
func (h *Handler) serveSkus(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodGet {
		writeUnsupported(w)
		return
	}

	h.mu.RLock()
	list, ok := h.skus[rt.serviceID]
	all := append([]*sku(nil), list...)
	h.mu.RUnlock()

	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "service not found: "+rt.serviceID)
		return
	}

	page, next := paginate(all, r)
	gcprest.WriteJSON(w, http.StatusOK, listSkusResponse{Skus: page, NextPageToken: next})
}
