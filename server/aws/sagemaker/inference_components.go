package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

func (h *Handler) routeInferenceComponents(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateInferenceComponent":
		h.createInferenceComponent(w, r)
	case "DescribeInferenceComponent":
		h.describeInferenceComponent(w, r)
	case "ListInferenceComponents":
		h.listInferenceComponents(w, r)
	case "DeleteInferenceComponent":
		h.deleteInferenceComponent(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) createInferenceComponent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InferenceComponentName string `json:"InferenceComponentName"`
		EndpointName           string `json:"EndpointName"`
		VariantName            string `json:"VariantName"`
		Specification          struct {
			ModelName string `json:"ModelName"`
		} `json:"Specification"`
		RuntimeConfig struct {
			CopyCount int `json:"CopyCount"`
		} `json:"RuntimeConfig"`
		Tags []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ic, err := h.svc.CreateInferenceComponent(r.Context(), driver.InferenceComponentSpec{
		Name:         req.InferenceComponentName,
		EndpointName: req.EndpointName,
		ModelName:    req.Specification.ModelName,
		VariantName:  req.VariantName,
		CopyCount:    req.RuntimeConfig.CopyCount,
		Tags:         toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"InferenceComponentArn": ic.ARN})
}

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) describeInferenceComponent(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "InferenceComponentName")
	if !ok {
		return
	}

	ic, err := h.svc.DescribeInferenceComponent(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"InferenceComponentName":   ic.Name,
		"InferenceComponentArn":    ic.ARN,
		"EndpointName":             ic.EndpointName,
		"VariantName":              ic.VariantName,
		"InferenceComponentStatus": ic.Status,
		"CreationTime":             epoch(ic.CreationTime),
	})
}

func (h *Handler) listInferenceComponents(w http.ResponseWriter, r *http.Request) {
	ics, err := h.svc.ListInferenceComponents(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "InferenceComponents", ics, func(ic *driver.InferenceComponent) map[string]any {
		return map[string]any{
			"InferenceComponentName":   ic.Name,
			"InferenceComponentArn":    ic.ARN,
			"EndpointName":             ic.EndpointName,
			"InferenceComponentStatus": ic.Status,
			"CreationTime":             epoch(ic.CreationTime),
		}
	})
}

func (h *Handler) deleteInferenceComponent(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "InferenceComponentName", h.svc.DeleteInferenceComponent)
}
