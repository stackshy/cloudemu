package notificationhubs

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// --- namespaces ---

// createOrUpdateNamespace maps Namespaces.CreateOrUpdate to the driver.
// CreateOrUpdate is idempotent: if the namespace already exists, echo it back.
func (h *Handler) createOrUpdateNamespace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body putBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if info, err := h.notif.GetTopic(r.Context(), rp.ResourceName); err == nil {
		azurearm.WriteJSON(w, http.StatusOK, toNamespaceJSON(rp, info))
		return
	}

	info, err := h.notif.CreateTopic(r.Context(), notifdriver.TopicConfig{
		Name: rp.ResourceName,
		Tags: body.Tags,
	})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusCreated, toNamespaceJSON(rp, info))
}

func (h *Handler) getNamespace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.notif.GetTopic(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toNamespaceJSON(rp, info))
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
	for _, name := range h.hubTopicNames(r, rp.ResourceName) {
		_ = h.notif.DeleteTopic(r.Context(), name)
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listNamespaces(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	topics, err := h.notif.ListTopics(r.Context())
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
		out = append(out, toNamespaceJSON(rp, &info))
	}

	azurearm.WriteJSON(w, http.StatusOK, namespaceListResult{Value: out})
}

// --- notification hubs ---

func (h *Handler) createOrUpdateHub(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body putBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	key := hubKey(rp.ResourceName, rp.SubResourceName)

	ttl := ""
	if body.Properties != nil {
		ttl = body.Properties.RegistrationTTL
	}

	if info, err := h.notif.GetTopic(r.Context(), key); err == nil {
		azurearm.WriteJSON(w, http.StatusOK, toHubJSON(rp, rp.ResourceName, rp.SubResourceName, info))
		return
	}

	info, err := h.notif.CreateTopic(r.Context(), notifdriver.TopicConfig{
		Name:        key,
		DisplayName: ttl,
		Tags:        body.Tags,
	})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

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
	topics, err := h.notif.ListTopics(r.Context())
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
// given namespace.
func (h *Handler) hubTopicNames(r *http.Request, namespace string) []string {
	topics, err := h.notif.ListTopics(r.Context())
	if err != nil {
		return nil
	}

	prefix := namespace + hubKeySep

	var names []string
	for i := range topics {
		if strings.HasPrefix(topics[i].Name, prefix) {
			names = append(names, topics[i].Name)
		}
	}

	return names
}
