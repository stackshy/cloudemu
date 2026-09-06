package applicationgateway

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	agdriver "github.com/stackshy/cloudemu/v2/services/applicationgateway/driver"
)

// createOrUpdateGateway handles PUT .../applicationGateways/{name}. The whole
// nested gateway arrives in one body and fully REPLACES the stored state, so any
// child or top-level property omitted from the body is removed — matching ARM's
// CreateOrUpdate semantics. ApplicationGateways.CreateOrUpdate is an LRO;
// returning the fully-provisioned body (provisioningState=Succeeded) completes
// the poller on the first response — 201 on create, 200 on update.
func (h *Handler) createOrUpdateGateway(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body appGwJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	gw := buildGateway(&body)

	stored, created, err := h.gw.CreateOrUpdateAzureApplicationGateway(r.Context(), rp.ResourceGroup, rp.ResourceName, gw)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toGatewayJSON(rp, stored))
}

func (h *Handler) getGateway(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.gw.GetAzureApplicationGateway(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toGatewayJSON(rp, stored))
}

// updateGatewayTags handles PATCH .../applicationGateways/{name} —
// ApplicationGateways.UpdateTags. Real armnetwork UpdateTags REPLACES the tag
// collection wholesale (an omitted existing key is dropped), matching every
// other Microsoft.Network UpdateTags handler in this server; every child of the
// gateway is left untouched. A request with the tags field entirely omitted
// (nil map) is a no-op. Returns 200 with the updated gateway.
func (h *Handler) updateGatewayTags(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body appGwJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.gw.GetAzureApplicationGateway(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	replaced := *stored
	if body.Tags != nil {
		replaced.Tags = body.Tags
	}

	updated, _, err := h.gw.CreateOrUpdateAzureApplicationGateway(r.Context(), rp.ResourceGroup, rp.ResourceName, replaced)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toGatewayJSON(rp, updated))
}

// deleteGateway removes the gateway. ApplicationGateways.Delete is an LRO; a 200
// with empty body completes the poller.
func (h *Handler) deleteGateway(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if derr := h.gw.DeleteAzureApplicationGateway(r.Context(), rp.ResourceGroup, rp.ResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listGateways(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.gw.ListAzureApplicationGateways(r.Context(), rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := appGwListResult{Value: make([]appGwJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.ResourceGroup = stored[i].ResourceGroup
		scope.ResourceName = stored[i].Name
		out.Value = append(out.Value, toGatewayJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// --- request → native model ---

// buildGateway maps the ARM request body to the native store model. Zones and
// identity are modeled explicitly (they are top-level, so the server-wide
// unmodeled-property echo — which only reaches "properties" — cannot preserve
// them). The armnetwork SDK and Terraform carry sku UNDER properties.sku, so it
// is modeled by extracting it from the properties object (with a top-level
// fallback for a raw REST client that sends it there). The modeled nested
// collections are split out of properties while every other property is
// preserved verbatim in OtherProps.
func buildGateway(body *appGwJSON) agdriver.AzureAppGateway {
	gw := agdriver.AzureAppGateway{
		Location: firstNonEmpty(body.Location, defaultLocation),
		Zones:    body.Zones,
		Identity: body.Identity,
		Tags:     body.Tags,
	}

	applySKU(&gw, resolveSKU(body))

	gw.Collections, gw.OtherProps = splitProperties(body.Properties)

	return gw
}

// resolveSKU returns the sku to model, preferring a top-level sku (raw REST) and
// falling back to properties.sku (the armnetwork SDK / Terraform wire shape).
func resolveSKU(body *appGwJSON) *appGwSKU {
	if body.SKU != nil {
		return body.SKU
	}

	return skuFromProps(body.Properties)
}

// applySKU copies a resolved sku into the native model's modeled fields.
func applySKU(gw *agdriver.AzureAppGateway, sku *appGwSKU) {
	if sku == nil {
		return
	}

	gw.SKUName = sku.Name
	gw.SKUTier = sku.Tier

	if sku.Capacity != nil {
		gw.SKUCapacity = *sku.Capacity
	}
}

// skuFromProps parses a sku object nested under properties.sku (generic JSON)
// into the typed sku shape. Returns nil when no sku object is present.
func skuFromProps(props map[string]any) *appGwSKU {
	raw, ok := props["sku"].(map[string]any)
	if !ok {
		return nil
	}

	sku := &appGwSKU{}
	sku.Name, _ = raw["name"].(string)
	sku.Tier, _ = raw["tier"].(string)

	if capacity, ok := raw["capacity"].(float64); ok {
		c := int(capacity)
		sku.Capacity = &c
	}

	return sku
}

// splitProperties partitions a request's properties into the modeled nested
// collections (keyed by ARM segment name) and every other property (OtherProps),
// dropping the modeled sku (handled separately) and any read-only
// provisioningState a client echoed back.
func splitProperties(
	props map[string]any,
) (collections map[string][]agdriver.AzureAppGatewayChild, other map[string]any) {
	if len(props) == 0 {
		return nil, nil
	}

	cols := make(map[string][]agdriver.AzureAppGatewayChild)
	rest := make(map[string]any)

	for k, v := range props {
		if k == provisioningStateKey || k == "sku" {
			continue
		}

		if isModeledCollection(k) {
			if kids := extractChildren(v); kids != nil {
				cols[k] = kids
			}

			continue
		}

		rest[k] = v
	}

	return emptyToNilCollections(cols), emptyToNilMap(rest)
}

// extractChildren reads a nested collection array into modeled children, keeping
// each item's name and its properties object verbatim. A request-supplied
// id/etag/type is discarded (recomputed on read).
func extractChildren(v any) []agdriver.AzureAppGatewayChild {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}

	out := make([]agdriver.AzureAppGatewayChild, 0, len(arr))

	for _, el := range arr {
		item, ok := el.(map[string]any)
		if !ok {
			continue
		}

		name, _ := item["name"].(string)
		props, _ := item["properties"].(map[string]any)
		out = append(out, agdriver.AzureAppGatewayChild{Name: name, Properties: props})
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// --- native model → response ---

// toGatewayJSON reconstructs the nested ARM gateway body from the stored model,
// stamping the gateway id/etag/provisioningState and each modeled child's id and
// provisioningState. rp is authoritative for the id (taken from the URL).
func toGatewayJSON(rp *azurearm.ResourcePath, gw *agdriver.AzureAppGateway) appGwJSON {
	gwID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeAppGws, rp.ResourceName)

	return appGwJSON{
		ID:         gwID,
		Name:       rp.ResourceName,
		Type:       gwResourceType,
		Location:   firstNonEmpty(gw.Location, defaultLocation),
		Etag:       weakETag(gwID),
		Zones:      gw.Zones,
		Identity:   gw.Identity,
		Tags:       gw.Tags,
		Properties: assembleProps(gwID, gw),
	}
}

// assembleProps builds the response properties object: every deferred property
// verbatim, then the modeled sku, then each modeled collection with stamped ids,
// then the gateway-wide provisioningState. sku and provisioningState live under
// properties in the armnetwork SDK / Terraform wire shape.
func assembleProps(gwID string, gw *agdriver.AzureAppGateway) map[string]any {
	// +2 sizing headroom for the injected sku and provisioningState entries.
	const injectedProps = 2

	props := make(map[string]any, len(gw.OtherProps)+len(gw.Collections)+injectedProps)

	for k, v := range gw.OtherProps {
		props[k] = v
	}

	if sku := skuJSON(gw); sku != nil {
		props["sku"] = sku
	}

	for _, segment := range modeledCollections {
		kids := gw.Collections[segment]
		if len(kids) == 0 {
			continue
		}

		props[segment] = childrenJSON(gwID, segment, kids)
	}

	props[provisioningStateKey] = provisioningStateSucceeded

	return props
}

// childrenJSON stamps each modeled child's ARM id/type/etag and injects
// provisioningState into its verbatim properties.
func childrenJSON(gwID, segment string, kids []agdriver.AzureAppGatewayChild) []appGwChildJSON {
	out := make([]appGwChildJSON, 0, len(kids))

	for i := range kids {
		id := gwID + "/" + segment + "/" + kids[i].Name
		out = append(out, appGwChildJSON{
			ID:         id,
			Name:       kids[i].Name,
			Type:       gwResourceType + "/" + segment,
			Etag:       weakETag(id),
			Properties: childProps(kids[i].Properties),
		})
	}

	return out
}

// childProps copies a child's stored properties, dropping write-only secret
// inputs real Azure never returns (a key ending in password/secret, or an ssl
// certificate's pfx "data" blob) and injecting the terminal provisioningState.
func childProps(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)

	for k, v := range in {
		if isWriteOnlyChildKey(k) {
			continue
		}

		out[k] = v
	}

	out[provisioningStateKey] = provisioningStateSucceeded

	return out
}

// --- helpers ---

// skuJSON projects the modeled sku fields, emitting capacity only when a fixed
// capacity was set (an autoscale gateway omits it).
func skuJSON(gw *agdriver.AzureAppGateway) *appGwSKU {
	if gw.SKUName == "" && gw.SKUTier == "" && gw.SKUCapacity == 0 {
		return nil
	}

	sku := &appGwSKU{Name: gw.SKUName, Tier: gw.SKUTier}

	if gw.SKUCapacity > 0 {
		capacity := gw.SKUCapacity
		sku.Capacity = &capacity
	}

	return sku
}

// isModeledCollection reports whether segment names a nested collection this
// handler models (stamping ids + provisioningState) rather than echoing verbatim.
func isModeledCollection(segment string) bool {
	for _, s := range modeledCollections {
		if s == segment {
			return true
		}
	}

	return false
}

// isWriteOnlyChildKey reports whether a child property key is a write-only input
// real Azure accepts but never returns, so it must never be echoed back.
func isWriteOnlyChildKey(key string) bool {
	lower := strings.ToLower(key)

	return strings.HasSuffix(lower, "password") || strings.HasSuffix(lower, "secret") || lower == "data"
}

// emptyToNilCollections returns nil for an empty collection map so an absent set
// serializes cleanly.
func emptyToNilCollections(in map[string][]agdriver.AzureAppGatewayChild) map[string][]agdriver.AzureAppGatewayChild {
	if len(in) == 0 {
		return nil
	}

	return in
}

// emptyToNilMap returns nil for an empty generic map.
func emptyToNilMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}

	return in
}

// weakETag wraps a stable etag in the weak-validator form (W/"...") ARM uses for
// Microsoft.Network resources.
func weakETag(id string) string {
	return azurearm.WeakETag(id)
}

// firstNonEmpty returns a if non-empty, otherwise b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}
