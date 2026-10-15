package appinsights

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	// subBillingFeatures is the components/{c}/currentbillingfeatures child.
	subBillingFeatures = "currentbillingfeatures"

	// Documented defaults for a component's billing features: the Basic plan
	// with a 100 GB daily cap, a warning at 90% and cap notifications on
	// (azurerm daily_data_cap_in_gb defaults to 100 and
	// daily_data_cap_notifications_enabled to true).
	defaultDailyCapGB       = 100
	defaultWarningThreshold = 90

	billingResetTime = "ResetTime"
)

// billingRequest is the writable shape of a currentbillingfeatures PUT.
type billingRequest struct {
	CurrentBillingFeatures []string       `json:"CurrentBillingFeatures"`
	DataVolumeCap          map[string]any `json:"DataVolumeCap"`
}

// billingReadOnly are the DataVolumeCap fields Azure computes, so a PUT can
// never change them.
//
//nolint:gochecknoglobals // read-only lookup set, not mutable state
var billingReadOnly = map[string]bool{billingResetTime: true, "MaxHistoryCap": true}

// serveBillingFeatures answers components/{c}/currentbillingfeatures, an
// always-present singleton of an existing component. GET returns the stored
// features (the documented default until the first write) and PUT replaces
// them, as the real Get and Update operations do.
func (h *Handler) serveBillingFeatures(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	existing, ok := h.store.Get(rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "component "+rp.ResourceName+" not found")
		return
	}

	if r.Method == http.MethodGet {
		azurearm.WriteJSON(w, http.StatusOK, billingOrDefault(existing.Billing))
		return
	}

	var req billingRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	updated := *existing
	updated.Billing = mergeBilling(&req)
	h.store.Set(&updated)

	azurearm.WriteJSON(w, http.StatusOK, updated.Billing)
}

// mergeBilling lays the writable fields of req over the defaults, so a PUT that
// omits a field reads back with its default rather than a zero value.
func mergeBilling(req *billingRequest) map[string]any {
	out := defaultBillingFeatures()

	if req.CurrentBillingFeatures != nil {
		out["CurrentBillingFeatures"] = append([]string(nil), req.CurrentBillingFeatures...)
	}

	capOut, _ := out["DataVolumeCap"].(map[string]any)

	for k, v := range req.DataVolumeCap {
		if !billingReadOnly[k] {
			capOut[k] = v
		}
	}

	return out
}

func billingOrDefault(stored map[string]any) map[string]any {
	if stored == nil {
		return defaultBillingFeatures()
	}

	return stored
}

func defaultBillingFeatures() map[string]any {
	return map[string]any{
		"CurrentBillingFeatures": []string{"Basic"},
		"DataVolumeCap": map[string]any{
			"Cap":                                  defaultDailyCapGB,
			billingResetTime:                       0,
			"WarningThreshold":                     defaultWarningThreshold,
			"StopSendNotificationWhenHitCap":       false,
			"StopSendNotificationWhenHitThreshold": false,
		},
	}
}
