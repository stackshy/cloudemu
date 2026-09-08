package iothub

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// serveConsumerGroup routes the nested consumer-group surface. A tail of
// eventHubEndpoints/events/ConsumerGroups lists the collection; a fourth segment
// names a single group for CRUD.
func (h *Handler) serveConsumerGroup(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, tail []string,
) {
	if len(tail) < cgTailLen {
		h.listConsumerGroups(w, r, rp)
		return
	}

	name := tail[cgTailLen-1]

	switch r.Method {
	case http.MethodPut:
		h.createConsumerGroup(w, r, rp, name)
	case http.MethodGet:
		h.getConsumerGroup(w, r, rp, name)
	case http.MethodDelete:
		h.deleteConsumerGroup(w, r, rp, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createConsumerGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, name string) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	c, created, err := h.store.CreateOrUpdateConsumerGroup(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, name)
	if err != nil {
		writeChildErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toConsumerGroupResponse(&c))
}

func (h *Handler) getConsumerGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, name string) {
	c, err := h.store.GetConsumerGroup(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, name)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toConsumerGroupResponse(&c))
}

func (h *Handler) deleteConsumerGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, name string) {
	existed, err := h.store.DeleteConsumerGroup(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, name)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listConsumerGroups(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	items, err := h.store.ListConsumerGroups(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := consumerGroupListResponse{Value: make([]consumerGroupResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toConsumerGroupResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// writeChildErr maps a consumer-group create error, translating a missing parent
// hub (NotFound) into the ARM ParentResourceNotFound 404 real Azure returns.
func writeChildErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		azurearm.WriteParentNotFound(w, err)
		return
	}

	azurearm.WriteCErr(w, err)
}
