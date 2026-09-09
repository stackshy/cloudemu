package bastion

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	bastiondriver "github.com/stackshy/cloudemu/v2/services/bastion/driver"
)

// createOrUpdateBastionHost handles PUT .../bastionHosts/{name}. The whole host
// arrives in one body and fully REPLACES the stored state, matching ARM's
// CreateOrUpdate semantics. BastionHosts.CreateOrUpdate is an LRO; returning the
// fully-provisioned body (provisioningState=Succeeded) completes the poller on
// the first response — 201 on create, 200 on update.
func (h *Handler) createOrUpdateBastionHost(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body bastionHostJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	host := buildBastionHost(&body)

	stored, created, err := h.hosts.CreateOrUpdateBastionHost(r.Context(), rp.ResourceGroup, rp.ResourceName, host)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toBastionHostJSON(rp, stored))
}

func (h *Handler) getBastionHost(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.hosts.GetBastionHost(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toBastionHostJSON(rp, stored))
}

// updateBastionHostTags handles PATCH .../bastionHosts/{name} —
// BastionHosts.UpdateTags. Real armnetwork UpdateTags REPLACES the tag collection
// wholesale (an omitted existing key is dropped); every other property is left
// untouched. A request with tags entirely omitted (nil map) is a no-op.
func (h *Handler) updateBastionHostTags(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body bastionHostJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.hosts.GetBastionHost(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	replaced := *stored
	if body.Tags != nil {
		replaced.Tags = body.Tags
	}

	updated, _, err := h.hosts.CreateOrUpdateBastionHost(r.Context(), rp.ResourceGroup, rp.ResourceName, replaced)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toBastionHostJSON(rp, updated))
}

// deleteBastionHost removes the host. BastionHosts.Delete is an LRO; a 200 with
// empty body completes the poller.
func (h *Handler) deleteBastionHost(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if derr := h.hosts.DeleteBastionHost(r.Context(), rp.ResourceGroup, rp.ResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listBastionHosts(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.hosts.ListBastionHosts(r.Context(), rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := bastionListResult{Value: make([]bastionHostJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.ResourceGroup = stored[i].ResourceGroup
		scope.ResourceName = stored[i].Name
		out.Value = append(out.Value, toBastionHostJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// --- request → native model ---

// buildBastionHost maps the ARM request body to the native store model. sku and
// zones are top-level; scaleUnits, the feature toggles and ipConfigurations are
// extracted from the properties object; every other property is preserved
// verbatim in OtherProps.
func buildBastionHost(body *bastionHostJSON) bastiondriver.BastionHost {
	host := bastiondriver.BastionHost{
		Location:            firstNonEmpty(body.Location, defaultLocation),
		Zones:               body.Zones,
		Tags:                body.Tags,
		SKUName:             skuName(body.SKU),
		ScaleUnits:          scaleUnitsFromProps(body.Properties),
		DisableCopyPaste:    boolField(body.Properties, disableCopyPasteKey),
		EnableTunneling:     boolField(body.Properties, enableTunnelingKey),
		EnableIPConnect:     boolField(body.Properties, enableIPConnectKey),
		EnableShareableLink: boolField(body.Properties, enableShareableLinkKey),
		EnableFileCopy:      boolField(body.Properties, enableFileCopyKey),
		EnableKerberos:      boolField(body.Properties, enableKerberosKey),
		IPConfigurations:    buildIPConfigs(body.Properties),
	}

	host.OtherProps = bastionOtherProps(body.Properties)

	return host
}

// bastionModeledKeys are the properties keys handled explicitly and therefore
// stripped from OtherProps.
//
//nolint:gochecknoglobals // fixed, read-only registry of modeled property keys.
var bastionModeledKeys = []string{
	ipConfigsKey, dnsNameKey, scaleUnitsKey,
	disableCopyPasteKey, enableTunnelingKey, enableIPConnectKey,
	enableShareableLinkKey, enableFileCopyKey, enableKerberosKey,
	provisioningStateKey,
}

// bastionOtherProps returns the deferred/echo-through properties (everything but
// the modeled keys).
func bastionOtherProps(props map[string]any) map[string]any {
	return stripKeys(props, bastionModeledKeys)
}

// buildIPConfigs reads the ipConfigurations array into modeled items (name +
// subnet/publicIPAddress references).
func buildIPConfigs(props map[string]any) []bastiondriver.BastionHostIPConfig {
	arr, ok := props[ipConfigsKey].([]any)
	if !ok {
		return nil
	}

	out := make([]bastiondriver.BastionHostIPConfig, 0, len(arr))

	for _, el := range arr {
		item, ok := el.(map[string]any)
		if !ok {
			continue
		}

		cfg := bastiondriver.BastionHostIPConfig{Name: stringField(item, "name")}
		p, _ := item["properties"].(map[string]any)
		cfg.SubnetID = nestedID(p, "subnet")
		cfg.PublicIPAddressID = nestedID(p, "publicIPAddress")

		out = append(out, cfg)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// --- native model → response ---

// toBastionHostJSON reconstructs the nested ARM host body from the stored model,
// stamping the host id/etag, the top-level sku and the modeled properties. rp is
// authoritative for the id (taken from the URL).
func toBastionHostJSON(rp *azurearm.ResourcePath, host *bastiondriver.BastionHost) bastionHostJSON {
	hostID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeBastionHosts, rp.ResourceName)

	return bastionHostJSON{
		ID:         hostID,
		Name:       rp.ResourceName,
		Type:       bastionResourceType,
		Location:   firstNonEmpty(host.Location, defaultLocation),
		Etag:       azurearm.WeakETag(hostID),
		Zones:      host.Zones,
		Tags:       host.Tags,
		SKU:        &skuJSON{Name: firstNonEmpty(host.SKUName, skuStandard)},
		Properties: assembleBastionProps(hostID, host),
	}
}

// assembleBastionProps builds the response properties object: every deferred
// property verbatim, then the computed dnsName, scaleUnits, the always-present
// feature toggles, ipConfigurations and the terminal provisioningState.
func assembleBastionProps(hostID string, host *bastiondriver.BastionHost) map[string]any {
	const injected = 10

	props := make(map[string]any, len(host.OtherProps)+injected)
	for k, v := range host.OtherProps {
		props[k] = v
	}

	props[dnsNameKey] = host.DNSName
	props[scaleUnitsKey] = scaleUnitsOrDefault(host.ScaleUnits)

	props[disableCopyPasteKey] = boolOrFalse(host.DisableCopyPaste)
	props[enableTunnelingKey] = boolOrFalse(host.EnableTunneling)
	props[enableIPConnectKey] = boolOrFalse(host.EnableIPConnect)
	props[enableShareableLinkKey] = boolOrFalse(host.EnableShareableLink)
	props[enableFileCopyKey] = boolOrFalse(host.EnableFileCopy)
	props[enableKerberosKey] = boolOrFalse(host.EnableKerberos)

	if cfgs := ipConfigsJSON(hostID, host.IPConfigurations); cfgs != nil {
		props[ipConfigsKey] = cfgs
	}

	props[provisioningStateKey] = provisioningStateSucceeded

	return props
}

// scaleUnitsOrDefault guards against a zero count leaking from a direct driver
// call or a legacy snapshot; the store defaults it, but a response must never
// report 0.
func scaleUnitsOrDefault(units int) int {
	if units == 0 {
		return defaultScaleUnits
	}

	return units
}

// ipConfigsJSON stamps each ipConfiguration's ARM id/etag and injects the
// computed privateIPAllocationMethod and terminal provisioningState.
func ipConfigsJSON(hostID string, cfgs []bastiondriver.BastionHostIPConfig) []ipConfigJSON {
	if len(cfgs) == 0 {
		return nil
	}

	out := make([]ipConfigJSON, 0, len(cfgs))

	for i := range cfgs {
		id := hostID + "/" + ipConfigChildType + "/" + cfgs[i].Name
		out = append(out, ipConfigJSON{
			ID:   id,
			Name: cfgs[i].Name,
			Etag: azurearm.WeakETag(id),
			Properties: &ipConfigPropsJSON{
				Subnet:                    optionalSubResource(cfgs[i].SubnetID),
				PublicIPAddress:           optionalSubResource(cfgs[i].PublicIPAddressID),
				PrivateIPAllocationMethod: privateIPAllocationDynamic,
				ProvisioningState:         provisioningStateSucceeded,
			},
		})
	}

	return out
}
