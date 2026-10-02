package appinsights

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	// subBillingFeatures is the components/{c}/currentbillingfeatures child.
	subBillingFeatures = "currentbillingfeatures"

	// Documented defaults for a component's billing features: the Basic plan
	// with a 100 GB daily cap and a warning at 90%.
	defaultDailyCapGB       = 100
	defaultWarningThreshold = 90
)

// serveBillingFeatures answers components/{c}/currentbillingfeatures, an
// always-present singleton. cloudemu does not model billing, so a read of an
// existing component returns the documented default and every write is 501.
func (h *Handler) serveBillingFeatures(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method == http.MethodGet {
		if _, ok := h.store.get(rp.Subscription, rp.ResourceGroup, rp.ResourceName); !ok {
			azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "component "+rp.ResourceName+" not found")
			return
		}
	}

	azurearm.ServeDeferred(w, r, rp, azurearm.DeferredSingleton, defaultBillingFeatures)
}

func defaultBillingFeatures() any {
	return map[string]any{
		"CurrentBillingFeatures": []string{"Basic"},
		"DataVolumeCap": map[string]any{
			"Cap":                                  defaultDailyCapGB,
			"ResetTime":                            0,
			"WarningThreshold":                     defaultWarningThreshold,
			"StopSendNotificationWhenHitCap":       false,
			"StopSendNotificationWhenHitThreshold": false,
		},
	}
}
