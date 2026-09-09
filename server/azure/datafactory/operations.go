package datafactory

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dfdriver "github.com/stackshy/cloudemu/v2/services/datafactory/driver"
)

// createOrUpdateFactory handles PUT .../factories/{name}. The whole factory
// arrives in one body and fully REPLACES the stored state, matching ARM's
// CreateOrUpdate semantics. Factories.CreateOrUpdate is synchronous; returning
// the fully-provisioned body (provisioningState=Succeeded) completes the client
// on the first response — 201 on create, 200 on update.
func (h *Handler) createOrUpdateFactory(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body factoryJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := buildFactoryConfig(rp, &body)

	stored, created, err := h.df.CreateOrUpdateFactory(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toFactoryJSON(rp, stored))
}

func (h *Handler) getFactory(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.df.GetFactory(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toFactoryJSON(rp, stored))
}

// updateFactory handles PATCH .../factories/{name} — Factories.Update. Tags and
// identity are REPLACED wholesale (resource-level UpdateTags = replace, not
// merge); every other property is left untouched. Fields omitted from the body
// leave that field unchanged.
func (h *Handler) updateFactory(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body factoryJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.df.UpdateFactory(
		r.Context(), rp.ResourceGroup, rp.ResourceName, body.Tags, fromIdentityJSON(body.Identity),
	)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toFactoryJSON(rp, stored))
}

// deleteFactory removes the factory. Factories.Delete is synchronous; a 200 with
// empty body completes the client.
func (h *Handler) deleteFactory(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if derr := h.df.DeleteFactory(r.Context(), rp.ResourceGroup, rp.ResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// listFactories serves both the resource-group-scoped list (.../factories) and
// the subscription-wide list (/providers/Microsoft.DataFactory/factories).
func (h *Handler) listFactories(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var (
		stored []dfdriver.Factory
		err    error
	)

	if rp.ResourceGroup != "" {
		stored, err = h.df.ListFactoriesByResourceGroup(r.Context(), rp.ResourceGroup)
	} else {
		stored, err = h.df.ListFactories(r.Context())
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := factoryListResult{Value: make([]factoryJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.ResourceGroup = stored[i].ResourceGroup
		scope.ResourceName = stored[i].Name
		out.Value = append(out.Value, toFactoryJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// --- request → native model ---

// buildFactoryConfig maps the ARM request body to the driver config. Location,
// tags and identity are top-level; publicNetworkAccess and globalParameters are
// extracted from the properties object; every other property is preserved
// verbatim in OtherProps.
func buildFactoryConfig(rp *azurearm.ResourcePath, body *factoryJSON) dfdriver.FactoryConfig {
	return dfdriver.FactoryConfig{
		Name:                rp.ResourceName,
		Subscription:        rp.Subscription,
		ResourceGroup:       rp.ResourceGroup,
		Location:            body.Location,
		Tags:                body.Tags,
		Identity:            fromIdentityJSON(body.Identity),
		PublicNetworkAccess: stringProp(body.Properties, publicNetworkKey),
		GlobalParameters:    buildGlobalParameters(body.Properties),
		OtherProps:          factoryOtherProps(body.Properties),
	}
}

// factoryModeledKeys are the properties keys handled explicitly and therefore
// stripped from OtherProps.
//
//nolint:gochecknoglobals // fixed, read-only registry of modeled property keys.
var factoryModeledKeys = []string{
	provisioningStateKey, createTimeKey, versionKey, publicNetworkKey, globalParametersKey,
}

// factoryOtherProps returns the deferred/echo-through properties (everything but
// the modeled keys — repoConfiguration, purviewConfiguration, encryption, ...).
func factoryOtherProps(props map[string]any) map[string]any {
	if len(props) == 0 {
		return nil
	}

	out := make(map[string]any, len(props))

	for k, v := range props {
		if isModeledKey(k) {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func isModeledKey(k string) bool {
	for _, mk := range factoryModeledKeys {
		if k == mk {
			return true
		}
	}

	return false
}

// buildGlobalParameters reads the properties.globalParameters map into modeled
// entries.
func buildGlobalParameters(props map[string]any) map[string]dfdriver.GlobalParameterSpec {
	raw, ok := props[globalParametersKey].(map[string]any)
	if !ok {
		return nil
	}

	out := make(map[string]dfdriver.GlobalParameterSpec, len(raw))

	for name, v := range raw {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}

		spec := dfdriver.GlobalParameterSpec{Value: item["value"]}
		if t, ok := item["type"].(string); ok {
			spec.Type = t
		}

		out[name] = spec
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func stringProp(props map[string]any, key string) string {
	if s, ok := props[key].(string); ok {
		return s
	}

	return ""
}

// --- native model → response ---

// toFactoryJSON reconstructs the ARM factory body from the stored model, stamping
// the id/eTag and the computed properties. rp is authoritative for the id (taken
// from the URL).
func toFactoryJSON(rp *azurearm.ResourcePath, f *dfdriver.Factory) factoryJSON {
	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, factoriesType, rp.ResourceName)

	return factoryJSON{
		ID:         id,
		Name:       rp.ResourceName,
		Type:       factoryResourceType,
		Location:   f.Location,
		ETag:       `"` + azurearm.ETag(id) + `"`,
		Tags:       f.Tags,
		Identity:   toIdentityJSON(f.Identity),
		Properties: assembleFactoryProps(f),
	}
}

// assembleFactoryProps builds the response properties object: every deferred
// property verbatim, then the computed provisioningState/createTime/version, the
// explicit publicNetworkAccess and the modeled globalParameters.
func assembleFactoryProps(f *dfdriver.Factory) map[string]any {
	const injected = 5

	props := make(map[string]any, len(f.OtherProps)+injected)
	for k, v := range f.OtherProps {
		props[k] = v
	}

	props[provisioningStateKey] = f.ProvisioningState
	props[versionKey] = f.Version

	if f.CreateTime != "" {
		props[createTimeKey] = f.CreateTime
	}

	if f.PublicNetworkAccess != "" {
		props[publicNetworkKey] = f.PublicNetworkAccess
	}

	if gp := globalParametersJSON(f.GlobalParameters); gp != nil {
		props[globalParametersKey] = gp
	}

	return props
}

// globalParametersJSON projects the modeled globalParameters back to the wire
// shape, or nil when unset.
func globalParametersJSON(in map[string]dfdriver.GlobalParameterSpec) map[string]globalParameterJSON {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]globalParameterJSON, len(in))
	for name, spec := range in {
		out[name] = globalParameterJSON{Type: spec.Type, Value: spec.Value}
	}

	return out
}
