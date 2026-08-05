package vpclattice

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

type wireListener struct {
	Arn           string          `json:"arn,omitempty"`
	ID            string          `json:"id,omitempty"`
	Name          string          `json:"name,omitempty"`
	Protocol      string          `json:"protocol,omitempty"`
	Port          int32           `json:"port,omitempty"`
	DefaultAction json.RawMessage `json:"defaultAction,omitempty"`
	ServiceArn    string          `json:"serviceArn,omitempty"`
	ServiceID     string          `json:"serviceId,omitempty"`
	CreatedAt     string          `json:"createdAt,omitempty"`
	LastUpdatedAt string          `json:"lastUpdatedAt,omitempty"`
}

func listenerToWire(l *driver.Listener) wireListener {
	w := wireListener{
		Arn: l.ARN, ID: l.ID, Name: l.Name, Protocol: l.Protocol, Port: l.Port,
		ServiceArn: l.ServiceARN, ServiceID: l.ServiceID,
		CreatedAt: l.CreatedAt, LastUpdatedAt: l.LastUpdatedAt,
	}
	if len(l.DefaultAction) > 0 {
		w.DefaultAction = json.RawMessage(l.DefaultAction)
	}

	return w
}

// serveListeners routes /services/{serviceID}/listeners[/{id}[/rules...]].
func (h *Handler) serveListeners(w http.ResponseWriter, r *http.Request, serviceID string, rest []string) {
	if len(rest) == 0 {
		routeCollection(w, r,
			func(w http.ResponseWriter, r *http.Request) { h.createListener(w, r, serviceID) },
			func(w http.ResponseWriter, r *http.Request) { h.listListeners(w, r, serviceID) })

		return
	}

	listenerID := rest[0]

	if len(rest) >= 2 && rest[1] == "rules" {
		h.serveRules(w, r, serviceID, listenerID, rest[2:])

		return
	}

	routeByID(w, r, listenerID,
		func(w http.ResponseWriter, r *http.Request, id string) { h.getListener(w, r, serviceID, id) },
		func(w http.ResponseWriter, r *http.Request, id string) { h.updateListener(w, r, serviceID, id) },
		func(w http.ResponseWriter, r *http.Request, id string) { h.deleteListener(w, r, serviceID, id) })
}

func (h *Handler) createListener(w http.ResponseWriter, r *http.Request, serviceID string) {
	var req struct {
		Name          string            `json:"name"`
		Protocol      string            `json:"protocol"`
		Port          int32             `json:"port"`
		DefaultAction json.RawMessage   `json:"defaultAction"`
		Tags          map[string]string `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	l, err := h.lattice.CreateListener(r.Context(), &driver.CreateListenerInput{
		ServiceID: serviceID, Name: req.Name, Protocol: req.Protocol, Port: req.Port,
		DefaultAction: req.DefaultAction, Tags: req.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listenerToWire(l))
}

func (h *Handler) getListener(w http.ResponseWriter, r *http.Request, serviceID, id string) {
	l, err := h.lattice.GetListener(r.Context(), serviceID, id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listenerToWire(l))
}

func (h *Handler) updateListener(w http.ResponseWriter, r *http.Request, serviceID, id string) {
	var req struct {
		DefaultAction json.RawMessage `json:"defaultAction"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	l, err := h.lattice.UpdateListener(r.Context(), serviceID, id, req.DefaultAction)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listenerToWire(l))
}

func (h *Handler) deleteListener(w http.ResponseWriter, r *http.Request, serviceID, id string) {
	if err := h.lattice.DeleteListener(r.Context(), serviceID, id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listListeners(w http.ResponseWriter, r *http.Request, serviceID string) {
	ls, err := h.lattice.ListListeners(r.Context(), serviceID)
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireListener, 0, len(ls))
	for i := range ls {
		items = append(items, listenerToWire(&ls[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}
