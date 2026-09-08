// Package iothub serves the Azure IoT Hub ARM API
// (Microsoft.Devices/IotHubs) — the management-plane resource provider, the
// listkeys / getKeysForKeyName shared-access-key actions, and the nested
// event-hub consumer groups. Real armdeviceprovisioningservices / azure-sdk
// IotHubResourceClient and terraform-provider-azurerm requests hit this handler
// the same way they hit management.azure.com.
//
// Real Azure runs hub CreateOrUpdate and Delete as long-running operations; the
// emulator completes them synchronously (sync-200/201) with
// provisioningState=Succeeded and state=Active. Shared-access-policy keys are
// minted once at create, stored, and byte-stable across every listkeys call;
// they are never echoed on the plain hub GET.
package iothub

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/iothub"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.Devices"
	hubType      = "IotHubs"
	hubArmType   = providerName + "/" + hubType

	// eventsKey is the only valid key of the eventHubEndpoints map.
	eventsKey = "events"

	// segEventHubEndpoints / segConsumerGroups are the fixed path segments of the
	// consumer-group sub-tree (.../eventHubEndpoints/events/ConsumerGroups/{name}).
	segEventHubEndpoints = "eventhubendpoints"
	segConsumerGroups    = "consumergroups"

	// actionListKeys is the hub-level POST action returning all policy keys.
	actionListKeys = "listkeys"
	// segIotHubKeys is the sub-resource for a single policy's keys
	// (.../IotHubKeys/{keyName}/listkeys).
	segIotHubKeys = "iothubkeys"

	// cgTailLen is the segment count of a named consumer-group path tail
	// (eventHubEndpoints/events/ConsumerGroups/{name}).
	cgTailLen = 4
	// keysTailLen is the segment count of a getKeysForKeyName path tail
	// (IotHubKeys/{keyName}/listkeys).
	keysTailLen = 3
)

// Store is the minimal IoT Hub backend the handler needs. *iothub.Mock
// satisfies it.
type Store interface {
	CreateOrUpdateHub(
		ctx context.Context, sub, rg, name, location string, in *iothub.HubInput,
	) (iothub.Hub, bool, error)
	GetHub(ctx context.Context, sub, rg, name string) (iothub.Hub, error)
	DeleteHub(ctx context.Context, sub, rg, name string) (bool, error)
	ListHubsByResourceGroup(ctx context.Context, sub, rg string) ([]iothub.Hub, error)
	ListHubsBySubscription(ctx context.Context, sub string) ([]iothub.Hub, error)
	ListKeys(ctx context.Context, sub, rg, name string) ([]iothub.SharedAccessPolicy, error)
	GetKeysForKeyName(ctx context.Context, sub, rg, name, keyName string) (iothub.SharedAccessPolicy, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error

	CreateOrUpdateConsumerGroup(ctx context.Context, sub, rg, hub, name string) (iothub.ConsumerGroup, bool, error)
	GetConsumerGroup(ctx context.Context, sub, rg, hub, name string) (iothub.ConsumerGroup, error)
	DeleteConsumerGroup(ctx context.Context, sub, rg, hub, name string) (bool, error)
	ListConsumerGroups(ctx context.Context, sub, rg, hub string) ([]iothub.ConsumerGroup, error)
}

// Handler serves Microsoft.Devices/IotHubs (and nested consumer-group) ARM
// requests.
type Handler struct {
	store Store
}

// New returns an IoT Hub handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets an IoT Hub ARM URL. The provider and type
// are matched case-insensitively.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, hubType)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		h.listHubs(w, r, &rp)
		return
	}

	tail := hubTail(r.URL.Path, rp.ResourceName)
	if len(tail) == 0 {
		h.serveHub(w, r, &rp)
		return
	}

	h.serveSubResource(w, r, &rp, tail)
}

// serveSubResource routes the sub-resource tail below a named hub: the key
// actions and the consumer-group sub-tree.
func (h *Handler) serveSubResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, tail []string) {
	switch {
	case len(tail) == 1 && strings.EqualFold(tail[0], actionListKeys):
		h.listKeys(w, r, rp)
	case len(tail) == keysTailLen && strings.EqualFold(tail[0], segIotHubKeys) &&
		strings.EqualFold(tail[2], actionListKeys):
		h.getKeysForKeyName(w, r, rp, tail[1])
	case isConsumerGroupTail(tail):
		h.serveConsumerGroup(w, r, rp, tail)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType",
			"unknown sub-resource "+strings.Join(tail, "/"))
	}
}

// PurgeResourceGroup deletes every hub and consumer group under sub/rg so a
// resource-group delete cascades into them.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

// serveHub routes the top-level hub CRUD surface.
func (h *Handler) serveHub(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createHub(w, r, rp)
	case http.MethodPatch:
		h.updateHub(w, r, rp)
	case http.MethodGet:
		h.getHub(w, r, rp)
	case http.MethodDelete:
		h.deleteHub(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createHub(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req hubRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := hubInputFromRequest(&req)

	hub, created, err := h.store.CreateOrUpdateHub(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toHubResponse(&hub))
}

// updateHub applies an ARM PATCH: only the supplied fields are overlaid onto the
// stored hub; the immutable location and computed fields are preserved. Tags are
// replaced wholesale (resource-level PATCH semantics). A PATCH on a missing hub
// is a 404.
func (h *Handler) updateHub(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.store.GetHub(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req hubRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := hubInputFromRequest(&req)

	hub, _, err := h.store.CreateOrUpdateHub(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, existing.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toHubResponse(&hub))
}

func (h *Handler) getHub(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	hub, err := h.store.GetHub(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toHubResponse(&hub))
}

func (h *Handler) deleteHub(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeleteHub(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listHubs(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		items []iothub.Hub
		err   error
	)

	if rp.ResourceGroup != "" {
		items, err = h.store.ListHubsByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		items, err = h.store.ListHubsBySubscription(r.Context(), rp.Subscription)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := hubListResponse{Value: make([]hubResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toHubResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	policies, err := h.store.ListKeys(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toKeysList(policies))
}

func (h *Handler) getKeysForKeyName(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, keyName string) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	p, err := h.store.GetKeysForKeyName(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, keyName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toPolicyWire(&p))
}

// writeDeleteStatus writes the idempotent ARM DELETE result: 200 when the
// resource existed, 204 when it did not.
func writeDeleteStatus(w http.ResponseWriter, existed bool) {
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// hubTail returns the path segments after the hub name in an IoT Hub ARM URL.
// azurearm.ParsePath captures only four trailing segments, but the consumer-group
// path is deeper (eventHubEndpoints/events/ConsumerGroups/{name}), so the tail is
// recovered directly from the raw path. The hub name is matched
// case-insensitively; segments before and including it are dropped.
func hubTail(urlPath, hub string) []string {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.EqualFold(parts[i], hub) {
			return parts[i+1:]
		}
	}

	return nil
}

// isConsumerGroupTail reports whether tail addresses the consumer-group sub-tree
// (a list at eventHubEndpoints/events/ConsumerGroups or a named group below it).
func isConsumerGroupTail(tail []string) bool {
	if len(tail) < cgTailLen-1 || len(tail) > cgTailLen {
		return false
	}

	return strings.EqualFold(tail[0], segEventHubEndpoints) &&
		strings.EqualFold(tail[1], eventsKey) &&
		strings.EqualFold(tail[2], segConsumerGroups)
}
