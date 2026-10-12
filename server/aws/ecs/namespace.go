package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

func (h *Handler) routeNamespace(w http.ResponseWriter, r *http.Request, op string) bool {
	if op != "ListServicesByNamespace" {
		return false
	}

	if sn, ok := capabilityOf[driver.ServiceNamespaces](h.ecs, w, op); ok {
		listServicesByNamespace(w, r, sn)
	}

	return true
}

func listServicesByNamespace(w http.ResponseWriter, r *http.Request, sn driver.ServiceNamespaces) {
	var req struct {
		Namespace  string `json:"namespace"`
		MaxResults int    `json:"maxResults"`
		NextToken  string `json:"nextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	arns, next, err := sn.ListServicesByNamespace(r.Context(), req.Namespace, req.MaxResults, req.NextToken)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, listResponse("serviceArns", arns, next))
}
