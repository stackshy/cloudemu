package notificationhubs

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// rpScope is the resource scope carried by the request path.
func rpScope(rp *azurearm.ResourcePath) scope.Scope {
	return scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}
}

// az returns the Azure-only Notification Hubs capability if the driver
// implements it.
func (h *Handler) az() (notifdriver.AzureNotificationHubs, bool) {
	az, ok := h.notif.(notifdriver.AzureNotificationHubs)
	return az, ok
}

// namespaceSKU returns the stored SKU name for a namespace, or "" when unset.
func (h *Handler) namespaceSKU(r *http.Request, namespace string) string {
	az, ok := h.az()
	if !ok {
		return ""
	}

	meta, err := az.GetNamespaceMeta(r.Context(), namespace)
	if err != nil {
		return ""
	}

	return meta.SKU
}

// --- namespaces ---

// createOrUpdateNamespace maps Namespaces.CreateOrUpdate onto the driver:
// create when absent, otherwise apply the request's mutable fields (tags) via
// UpdateTopic — ARM PUT semantics, so the caller's changes are never silently
// discarded.
func (h *Handler) createOrUpdateNamespace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body putBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := notifdriver.TopicConfig{
		Name:   rp.ResourceName,
		Tags:   body.Tags,
		Scope:  rpScope(rp),
		Region: body.Location,
	}

	skuName := ""
	if body.SKU != nil {
		skuName = body.SKU.Name
	}

	if _, err := h.notif.GetTopic(r.Context(), rp.ResourceName); err == nil {
		info, uerr := h.notif.UpdateTopic(r.Context(), cfg)
		if uerr != nil {
			azurearm.WriteCErr(w, uerr)
			return
		}

		h.storeNamespaceSKU(r, rp.ResourceName, skuName)
		azurearm.WriteJSON(w, http.StatusOK, toNamespaceJSON(rp, info, h.namespaceSKU(r, rp.ResourceName)))

		return
	}

	info, err := h.notif.CreateTopic(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.storeNamespaceSKU(r, rp.ResourceName, skuName)
	azurearm.WriteJSON(w, http.StatusCreated, toNamespaceJSON(rp, info, h.namespaceSKU(r, rp.ResourceName)))
}

// storeNamespaceSKU records the namespace SKU when supplied and supported.
func (h *Handler) storeNamespaceSKU(r *http.Request, namespace, skuName string) {
	if skuName == "" {
		return
	}

	if az, ok := h.az(); ok {
		_ = az.SetNamespaceMeta(r.Context(), namespace, notifdriver.AzureNamespaceMeta{SKU: skuName})
	}
}

func (h *Handler) getNamespace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.notif.GetTopic(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// A namespace lives in exactly one resource group; a Get scoped to a
	// different group must 404 (the driver keys topics by bare name).
	if wrongResourceGroup(rp, info) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"namespace "+rp.ResourceName+" not found in resource group "+rp.ResourceGroup)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toNamespaceJSON(rp, info, h.namespaceSKU(r, rp.ResourceName)))
}

// wrongResourceGroup reports whether the request's resource group disagrees
// with the namespace's stored resource group.
func wrongResourceGroup(rp *azurearm.ResourcePath, info *notifdriver.TopicInfo) bool {
	return rp.ResourceGroup != "" && info.Scope.ResourceGroup != "" &&
		info.Scope.ResourceGroup != rp.ResourceGroup
}

// deleteNamespace removes the namespace topic and every hub topic nested under
// it. Namespaces.BeginDelete is an LRO; returning 200 completes the poller on
// the first response.
func (h *Handler) deleteNamespace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.notif.DeleteTopic(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// Best-effort cleanup of nested hub topics.
	for _, name := range h.hubTopicNames(r, rp) {
		_ = h.notif.DeleteTopic(r.Context(), name)
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listNamespaces(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	topics, err := h.notif.ListTopics(r.Context(), rpScope(rp))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]namespaceJSON, 0, len(topics))
	for i := range topics {
		// Namespaces are top-level topics; hub topics carry a "/" in the key.
		if strings.Contains(topics[i].Name, hubKeySep) {
			continue
		}

		info := topics[i]
		out = append(out, toNamespaceJSON(rp, &info, h.namespaceSKU(r, info.Name)))
	}

	azurearm.WriteJSON(w, http.StatusOK, namespaceListResult{Value: out})
}

// --- notification hubs ---

func (h *Handler) createOrUpdateHub(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// Decode with the properties block kept raw so PNS credentials (whose shape
	// is platform-specific) round-trip verbatim for GetPnsCredentials.
	var body hubPutBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	key := hubKey(rp.ResourceName, rp.SubResourceName)

	cfg := notifdriver.TopicConfig{
		Name:        key,
		DisplayName: body.registrationTTL(),
		Tags:        body.Tags,
		Scope:       rpScope(rp),
		Region:      body.Location,
	}

	if _, err := h.notif.GetTopic(r.Context(), key); err == nil {
		info, uerr := h.notif.UpdateTopic(r.Context(), cfg)
		if uerr != nil {
			azurearm.WriteCErr(w, uerr)
			return
		}

		h.storePnsCredentials(r, key, body.Properties)
		azurearm.WriteJSON(w, http.StatusOK, toHubJSON(rp, rp.ResourceName, rp.SubResourceName, info))

		return
	}

	info, err := h.notif.CreateTopic(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.storePnsCredentials(r, key, body.Properties)
	azurearm.WriteJSON(w, http.StatusCreated, toHubJSON(rp, rp.ResourceName, rp.SubResourceName, info))
}

func (h *Handler) getHub(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.notif.GetTopic(r.Context(), hubKey(rp.ResourceName, rp.SubResourceName))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toHubJSON(rp, rp.ResourceName, rp.SubResourceName, info))
}

func (h *Handler) deleteHub(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.notif.DeleteTopic(r.Context(), hubKey(rp.ResourceName, rp.SubResourceName)); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listHubs(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	topics, err := h.notif.ListTopics(r.Context(), rpScope(rp))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	prefix := rp.ResourceName + hubKeySep

	out := make([]hubJSON, 0, len(topics))
	for i := range topics {
		if !strings.HasPrefix(topics[i].Name, prefix) {
			continue
		}

		hubName := strings.TrimPrefix(topics[i].Name, prefix)
		info := topics[i]
		out = append(out, toHubJSON(rp, rp.ResourceName, hubName, &info))
	}

	azurearm.WriteJSON(w, http.StatusOK, hubListResult{Value: out})
}

// hubTopicNames returns the driver topic keys of every hub nested under the
// namespace named by the request path.
func (h *Handler) hubTopicNames(r *http.Request, rp *azurearm.ResourcePath) []string {
	topics, err := h.notif.ListTopics(r.Context(), rpScope(rp))
	if err != nil {
		return nil
	}

	prefix := rp.ResourceName + hubKeySep

	var names []string
	for i := range topics {
		if strings.HasPrefix(topics[i].Name, prefix) {
			names = append(names, topics[i].Name)
		}
	}

	return names
}
