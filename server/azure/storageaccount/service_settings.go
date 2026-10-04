package storageaccount

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	serviceDefaultName   = "default"
	managementPolicyKind = "managementPolicies"
	// managementPolicyName is the name real Azure reports for the account's
	// single management policy, whatever name the request used.
	managementPolicyName = "DefaultManagementPolicy"

	// Lower-cased account child segments with their own settings document.
	kindQueueServices = "queueservices"
	kindTableServices = "tableservices"
	kindFileServices  = "fileservices"

	// Deepest supported paths after {type}: …/fileServices/default/shares/{s}
	// and …/managementPolicies/default.
	serviceChildItemDepth = 5
	managementPolicyDepth = 3

	jsonNull = "null"
)

// serviceChildType is the nested collection real Azure serves under
// {x}Services/default. cloudemu does not model these over ARM yet.
func serviceChildType(kind string) string {
	switch kind {
	case kindQueueServices:
		return "queues"
	case kindTableServices:
		return "tables"
	default:
		return "shares"
	}
}

// serviceDefaults are the properties real Azure reports for a new StorageV2
// account's queue, table and file services. A stored document keeps what the
// client sent; these fill only the keys it left out.
func serviceDefaults(kind string) map[string]any {
	d := map[string]any{"cors": map[string]any{"corsRules": []any{}}}

	if kind == kindFileServices {
		d["shareDeleteRetentionPolicy"] = map[string]any{"enabled": true, "days": 7}
		d["protocolSettings"] = map[string]any{"smb": map[string]any{}}
	}

	return d
}

// armChildBody is the request envelope of an account-level settings PUT.
type armChildBody struct {
	Properties json.RawMessage `json:"properties"`
}

// armChildResource is the response shape of an account-level settings
// resource.
type armChildResource struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
}

// serveServiceSettings serves the queue, table and file service settings:
//
//	GET     …/{x}Services                  : {"value":[default]}
//	GET/PUT …/{x}Services/default          : service properties
//	GET     …/{x}Services/default/{child}  : empty list (not modeled yet)
//
// These are their own resources: a PUT never touches the account.
func (h *Handler) serveServiceSettings(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if azurearm.TooDeep(w, r, rp, serviceChildItemDepth) || !h.requireSettings(w) {
		return
	}

	kind := strings.ToLower(rp.SubResource)

	if rp.SubResourceAction != "" && !strings.EqualFold(rp.SubResourceAction, serviceChildType(kind)) {
		azurearm.WriteUnknownType(w, r, rp)
		return
	}

	if _, ok := h.lookup(w, r, rp); !ok {
		return
	}

	switch {
	case rp.SubResourceName != "" && !strings.EqualFold(rp.SubResourceName, serviceDefaultName):
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"The Resource '"+providerName+"/"+resourceType+"/"+rp.ResourceName+"/"+rp.SubResource+"/"+
				rp.SubResourceName+"' under resource group '"+rp.ResourceGroup+"' was not found.")
	case rp.SubResourceAction != "":
		serveServiceChild(w, r, rp)
	case rp.SubResourceName == "":
		h.listServiceSettings(w, r, rp, kind)
	default:
		h.serveServiceDefault(w, r, rp, kind)
	}
}

// serveServiceChild answers …/{x}Services/default/{child}[/{name}], which
// cloudemu does not model over ARM: an empty list, a missing item, writes 501.
func serveServiceChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	kind := azurearm.DeferredCollection
	if rp.Rest != "" {
		kind = azurearm.DeferredItem
	}

	azurearm.ServeDeferred(w, r, rp, kind, nil)
}

func (h *Handler) listServiceSettings(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	body, ok := h.serviceSettingsBody(w, r, rp, kind)
	if !ok {
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": []armChildResource{body}})
}

func (h *Handler) serveServiceDefault(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		props, ok := decodeChildProperties(w, r)
		if !ok {
			return
		}

		if _, err := h.settings.SetAccountSetting(r.Context(), rp.ResourceName, kind, props); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	body, ok := h.serviceSettingsBody(w, r, rp, kind)
	if !ok {
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, body)
}

// serviceSettingsBody renders the stored {x}Services/default document with the
// real defaults filled in for keys the client never set.
func (h *Handler) serviceSettingsBody(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string,
) (armChildResource, bool) {
	stored, _, err := h.settings.AccountSetting(r.Context(), rp.ResourceName, kind)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return armChildResource{}, false
	}

	props := map[string]any{}
	if len(stored.Properties) > 0 {
		_ = json.Unmarshal(stored.Properties, &props)
	}

	for k, v := range serviceDefaults(kind) {
		if _, set := props[k]; !set {
			props[k] = v
		}
	}

	return armChildResource{
		ID:         childID(rp, rp.SubResource, serviceDefaultName),
		Name:       serviceDefaultName,
		Type:       providerName + "/" + resourceType + "/" + rp.SubResource,
		Properties: props,
	}, true
}

// serveManagementPolicy serves …/managementPolicies/default: GET (404 until
// set), PUT (full replace) and DELETE.
func (h *Handler) serveManagementPolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if azurearm.TooDeep(w, r, rp, managementPolicyDepth) || !h.requireSettings(w) {
		return
	}

	if _, ok := h.lookup(w, r, rp); !ok {
		return
	}

	if !strings.EqualFold(rp.SubResourceName, serviceDefaultName) {
		writePolicyNotFound(w, rp)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getManagementPolicy(w, r, rp)
	case http.MethodPut:
		h.putManagementPolicy(w, r, rp)
	case http.MethodDelete:
		h.deleteManagementPolicy(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) getManagementPolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, ok, err := h.settings.AccountSetting(r.Context(), rp.ResourceName, managementPolicyKind)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if !ok {
		writePolicyNotFound(w, rp)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, managementPolicyBody(rp, stored.Properties, stored.LastModified))
}

func (h *Handler) putManagementPolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	props, ok := decodeChildProperties(w, r)
	if !ok {
		return
	}

	var p struct {
		Policy json.RawMessage `json:"policy"`
	}

	if err := json.Unmarshal(props, &p); err != nil || len(p.Policy) == 0 || string(p.Policy) == jsonNull {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent",
			"The management policy requires properties.policy.")

		return
	}

	stored, err := h.settings.SetAccountSetting(r.Context(), rp.ResourceName, managementPolicyKind, props)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, managementPolicyBody(rp, stored.Properties, stored.LastModified))
}

// deleteManagementPolicy answers 200 when a policy was removed and 204 when
// there was none.
func (h *Handler) deleteManagementPolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.settings.DeleteAccountSetting(r.Context(), rp.ResourceName, managementPolicyKind)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if !existed {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func managementPolicyBody(rp *azurearm.ResourcePath, raw []byte, modified time.Time) armChildResource {
	props := map[string]any{}
	_ = json.Unmarshal(raw, &props)
	props["lastModifiedTime"] = modified.UTC().Format(time.RFC3339Nano)

	return armChildResource{
		ID:         childID(rp, managementPolicyKind, serviceDefaultName),
		Name:       managementPolicyName,
		Type:       providerName + "/" + resourceType + "/" + managementPolicyKind,
		Properties: props,
	}
}

func (h *Handler) requireSettings(w http.ResponseWriter) bool {
	if h.settings != nil {
		return true
	}

	azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "account settings not supported")

	return false
}

func writePolicyNotFound(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	azurearm.WriteError(w, http.StatusNotFound, "ManagementPolicyNotFound",
		"No ManagementPolicy found for account "+rp.ResourceName)
}

func childID(rp *azurearm.ResourcePath, child, name string) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, rp.ResourceName) +
		"/" + child + "/" + name
}

// decodeChildProperties reads a settings PUT body and returns its properties
// object, {} when the body has none.
func decodeChildProperties(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	var body armChildBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return nil, false
	}

	if len(body.Properties) == 0 || string(body.Properties) == jsonNull {
		return []byte("{}"), true
	}

	if body.Properties[0] != '{' {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", "properties must be an object")
		return nil, false
	}

	return body.Properties, true
}
