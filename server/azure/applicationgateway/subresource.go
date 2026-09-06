package applicationgateway

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	agdriver "github.com/stackshy/cloudemu/v2/services/applicationgateway/driver"
)

// serveSubResource serves a request addressing one nested collection of the
// gateway. Application Gateway has NO standalone child ARM operation groups —
// every child is created and mutated only through the whole-gateway PUT — so a
// sub-resource path is served read-only: GET reflects the inline children (with
// their stamped ids), and any mutation (PUT/DELETE) is 405. An unknown or
// deferred (unmodeled) collection segment is 404, since only the modeled
// collections carry stamped, individually-addressable ids.
func (h *Handler) serveSubResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if !isModeledCollection(rp.SubResource) {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"unknown application gateway sub-resource "+rp.SubResource)

		return
	}

	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	if rp.SubResourceName == "" {
		h.listSubResource(w, r, rp)
		return
	}

	h.getSubResource(w, r, rp)
}

// listSubResource handles GET .../applicationGateways/{name}/{collection} — every
// modeled child of that collection on the gateway.
func (h *Handler) listSubResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	gw, err := h.gw.GetAzureApplicationGateway(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	gwID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeAppGws, rp.ResourceName)
	children := childrenJSON(gwID, rp.SubResource, gw.Collections[rp.SubResource])

	azurearm.WriteJSON(w, http.StatusOK, subResourceListResult{Value: children})
}

// getSubResource handles GET .../applicationGateways/{name}/{collection}/{child}
// — the one addressed child.
func (h *Handler) getSubResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	gw, err := h.gw.GetAzureApplicationGateway(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	child, found := findChild(gw.Collections[rp.SubResource], rp.SubResourceName)
	if !found {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			rp.SubResource+" "+rp.SubResourceName+" not found")

		return
	}

	gwID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeAppGws, rp.ResourceName)

	azurearm.WriteJSON(w, http.StatusOK, childrenJSON(gwID, rp.SubResource, []agdriver.AzureAppGatewayChild{child})[0])
}

// findChild locates the single named child in a collection.
func findChild(kids []agdriver.AzureAppGatewayChild, name string) (agdriver.AzureAppGatewayChild, bool) {
	for i := range kids {
		if strings.EqualFold(kids[i].Name, name) {
			return kids[i], true
		}
	}

	return agdriver.AzureAppGatewayChild{}, false
}
