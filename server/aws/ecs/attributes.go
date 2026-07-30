package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

func (h *Handler) routeAttributes(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "PutAttributes":
		h.putAttributes(w, r)
	case "DeleteAttributes":
		h.deleteAttributes(w, r)
	case "ListAttributes":
		h.listAttributes(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) putAttributes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster    string          `json:"cluster"`
		Attributes []wireAttribute `json:"attributes"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	attrs, err := h.ecs.PutAttributes(r.Context(), req.Cluster, toAttributes(req.Attributes))
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"attributes": fromAttributes(attrs)})
}

func (h *Handler) deleteAttributes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster    string          `json:"cluster"`
		Attributes []wireAttribute `json:"attributes"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	attrs, err := h.ecs.DeleteAttributes(r.Context(), req.Cluster, toAttributes(req.Attributes))
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"attributes": fromAttributes(attrs)})
}

func (h *Handler) listAttributes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster        string `json:"cluster"`
		TargetType     string `json:"targetType"`
		AttributeName  string `json:"attributeName"`
		AttributeValue string `json:"attributeValue"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	attrs, err := h.ecs.ListAttributes(r.Context(), req.Cluster, req.TargetType, req.AttributeName, req.AttributeValue)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"attributes": fromAttributes(attrs)})
}
