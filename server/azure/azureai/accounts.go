package azureai

import (
	"net/http"

	csdriver "github.com/stackshy/cloudemu/azureai/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// armAccountBody is the ARM request body for an account PUT.
type armAccountBody struct {
	Location string `json:"location"`
	Kind     string `json:"kind"`
	SKU      *struct {
		Name string `json:"name"`
	} `json:"sku"`
	Tags       map[string]string `json:"tags"`
	Properties *struct {
		CustomSubDomainName string `json:"customSubDomainName"`
	} `json:"properties"`
}

func accountJSON(a *csdriver.Account) map[string]any {
	return map[string]any{
		"id":       a.ID,
		"name":     a.Name,
		"type":     csProvider + "/accounts",
		"location": a.Location,
		"kind":     a.Kind,
		"sku":      map[string]any{"name": a.SKUName},
		"tags":     a.Tags,
		"properties": map[string]any{
			"endpoint":            a.Endpoint,
			"provisioningState":   a.ProvisioningState,
			"customSubDomainName": a.CustomDomain,
		},
	}
}

func (h *CognitiveServicesHandler) createOrUpdateAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armAccountBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := csdriver.AccountConfig{
		Name:          rp.ResourceName,
		ResourceGroup: rp.ResourceGroup,
		Location:      body.Location,
		Kind:          body.Kind,
		Tags:          body.Tags,
	}

	if body.SKU != nil {
		cfg.SKUName = body.SKU.Name
	}

	if body.Properties != nil {
		cfg.CustomDomain = body.Properties.CustomSubDomainName
	}

	a, err := h.svc.CreateAccount(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, accountJSON(a))
}

func (h *CognitiveServicesHandler) getAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	a, err := h.svc.GetAccount(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, accountJSON(a))
}

func (h *CognitiveServicesHandler) patchAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body struct {
		Tags map[string]string `json:"tags"`
	}

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	a, err := h.svc.UpdateAccountTags(r.Context(), rp.ResourceGroup, rp.ResourceName, body.Tags)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, accountJSON(a))
}

func (h *CognitiveServicesHandler) deleteAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.svc.DeleteAccount(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{})
}

//nolint:dupl // list-then-map shape mirrors the workspaces handler.
func (h *CognitiveServicesHandler) listAccounts(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var (
		accts []csdriver.Account
		err   error
	)

	if rp.ResourceGroup == "" {
		accts, err = h.svc.ListAccounts(r.Context())
	} else {
		accts, err = h.svc.ListAccountsByResourceGroup(r.Context(), rp.ResourceGroup)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(accts))
	for i := range accts {
		out = append(out, accountJSON(&accts[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}

// accountActionMethods is the HTTP method each account action requires, matching
// the real Microsoft.CognitiveServices REST contract.
//
//nolint:gochecknoglobals // immutable routing set
var accountActionMethods = map[string]string{
	"listKeys":      http.MethodPost,
	"regenerateKey": http.MethodPost,
	"models":        http.MethodGet,
	"skus":          http.MethodGet,
	"usages":        http.MethodGet,
}

// serveAccountAction handles the account-level verbs and read-only catalogs.
func (h *CognitiveServicesHandler) serveAccountAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if want, ok := accountActionMethods[rp.SubResource]; ok && r.Method != want {
		writeMethodNotAllowed(w)

		return
	}

	switch rp.SubResource {
	case "listKeys":
		h.listKeys(w, r, rp)
	case "regenerateKey":
		h.regenerateKey(w, r, rp)
	case "models":
		h.listModels(w, r, rp)
	case "skus":
		h.listSkus(w, r, rp)
	case "usages":
		h.listUsages(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown account action: "+rp.SubResource)
	}
}

func (h *CognitiveServicesHandler) listKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	keys, err := h.svc.ListAccountKeys(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"key1": keys.Key1, "key2": keys.Key2})
}

func (h *CognitiveServicesHandler) regenerateKey(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body struct {
		KeyName string `json:"keyName"`
	}

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	keys, err := h.svc.RegenerateAccountKey(r.Context(), rp.ResourceGroup, rp.ResourceName, body.KeyName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"key1": keys.Key1, "key2": keys.Key2})
}

func (h *CognitiveServicesHandler) listModels(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	models, err := h.svc.ListAccountModels(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(models))
	for _, mdl := range models {
		out = append(out, map[string]any{
			"name": mdl.Name, "format": mdl.Format, "kind": mdl.Kind,
			"model": map[string]any{"name": mdl.Name, "version": mdl.Version, "format": mdl.Format},
		})
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}

func (h *CognitiveServicesHandler) listSkus(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	skus, err := h.svc.ListAccountSkus(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(skus))
	for _, s := range skus {
		out = append(out, map[string]any{"resourceType": "accounts", "sku": map[string]any{"name": s.Name, "tier": s.Tier}})
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}

func (h *CognitiveServicesHandler) listUsages(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	usages, err := h.svc.ListAccountUsages(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(usages))
	for _, u := range usages {
		out = append(out, map[string]any{
			"name":         map[string]any{"value": u.Name, "localizedValue": u.Name},
			"currentValue": u.CurrentValue, "limit": u.Limit, "unit": u.Unit,
		})
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}
