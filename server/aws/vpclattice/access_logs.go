package vpclattice

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

type wireAccessLogSub struct {
	Arn                   string `json:"arn,omitempty"`
	ID                    string `json:"id,omitempty"`
	DestinationArn        string `json:"destinationArn,omitempty"`
	ResourceArn           string `json:"resourceArn,omitempty"`
	ResourceID            string `json:"resourceId,omitempty"`
	ServiceNetworkLogType string `json:"serviceNetworkLogType,omitempty"`
	CreatedAt             string `json:"createdAt,omitempty"`
	LastUpdatedAt         string `json:"lastUpdatedAt,omitempty"`
}

func accessLogSubToWire(a *driver.AccessLogSubscription) wireAccessLogSub {
	return wireAccessLogSub{
		Arn: a.ARN, ID: a.ID, DestinationArn: a.DestinationARN, ResourceArn: a.ResourceARN,
		ResourceID: a.ResourceID, ServiceNetworkLogType: a.ServiceNetworkLogType,
		CreatedAt: a.CreatedAt, LastUpdatedAt: a.LastUpdatedAt,
	}
}

func (h *Handler) serveAccessLogSubs(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		routeCollection(w, r, h.createAccessLogSub, h.listAccessLogSubs)

		return
	}

	routeByID(w, r, rest[0], h.getAccessLogSub, h.updateAccessLogSub, h.deleteAccessLogSub)
}

func (h *Handler) createAccessLogSub(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DestinationArn        string            `json:"destinationArn"`
		ResourceIdentifier    string            `json:"resourceIdentifier"`
		ServiceNetworkLogType string            `json:"serviceNetworkLogType"`
		Tags                  map[string]string `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	a, err := h.lattice.CreateAccessLogSubscription(r.Context(),
		req.ResourceIdentifier, req.DestinationArn, req.ServiceNetworkLogType, req.Tags)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, accessLogSubToWire(a))
}

func (h *Handler) getAccessLogSub(w http.ResponseWriter, r *http.Request, id string) {
	a, err := h.lattice.GetAccessLogSubscription(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, accessLogSubToWire(a))
}

func (h *Handler) updateAccessLogSub(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		DestinationArn string `json:"destinationArn"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	a, err := h.lattice.UpdateAccessLogSubscription(r.Context(), id, req.DestinationArn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, accessLogSubToWire(a))
}

func (h *Handler) deleteAccessLogSub(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.lattice.DeleteAccessLogSubscription(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listAccessLogSubs(w http.ResponseWriter, r *http.Request) {
	as, err := h.lattice.ListAccessLogSubscriptions(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireAccessLogSub, 0, len(as))
	for i := range as {
		items = append(items, accessLogSubToWire(&as[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}
