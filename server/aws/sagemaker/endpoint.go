package sagemaker

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// describeEndpoint serves DescribeEndpoint with the real response shape:
// ProductionVariantSummary entries (current vs desired weight and count,
// deployed images, in-flight variant status), the pending deployment while the
// endpoint is Creating or Updating, and the last deployment config.
func (h *Handler) describeEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EndpointName string `json:"EndpointName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ep, err := h.svc.DescribeEndpoint(r.Context(), req.EndpointName)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	resp := map[string]any{
		"EndpointName":       ep.EndpointName,
		"EndpointArn":        ep.EndpointARN,
		"EndpointConfigName": ep.ConfigName,
		"EndpointStatus":     ep.Status,
		"ProductionVariants": variantSummariesToWire(ep.VariantSummaries, nil),
		"CreationTime":       epoch(ep.CreationTime),
		"LastModifiedTime":   epoch(ep.LastModifiedTime),
	}

	if ep.FailureReason != "" {
		resp["FailureReason"] = ep.FailureReason
	}

	if ep.Pending != nil {
		resp["PendingDeploymentSummary"] = map[string]any{
			"EndpointConfigName": ep.Pending.ConfigName,
			"ProductionVariants": variantSummariesToWire(ep.Pending.Variants, ep.Variants),
			"StartTime":          epoch(ep.Pending.StartTime),
		}
	}

	if len(ep.LastDeploymentConfig) > 0 {
		resp["LastDeploymentConfig"] = ep.LastDeploymentConfig
	}

	wire.WriteJSON(w, resp)
}

// variantSummariesToWire projects variant summaries. When configVariants is
// given (the pending summary), each entry also carries its InstanceType.
func variantSummariesToWire(in []driver.VariantSummary, configVariants []driver.ProductionVariant) []map[string]any {
	instanceType := make(map[string]string, len(configVariants))
	for _, v := range configVariants {
		instanceType[v.VariantName] = v.InstanceType
	}

	out := make([]map[string]any, 0, len(in))

	for i := range in {
		s := &in[i]
		m := map[string]any{
			"VariantName":          s.VariantName,
			"CurrentWeight":        s.CurrentWeight,
			"DesiredWeight":        s.DesiredWeight,
			"CurrentInstanceCount": s.CurrentInstanceCount,
			"DesiredInstanceCount": s.DesiredInstanceCount,
			"DeployedImages":       deployedImagesToWire(s.DeployedImages),
		}

		if len(s.VariantStatus) > 0 {
			status := make([]map[string]any, 0, len(s.VariantStatus))

			for _, vs := range s.VariantStatus {
				e := map[string]any{"Status": vs.Status, "StartTime": epoch(vs.StartTime)}
				if vs.StatusMessage != "" {
					e["StatusMessage"] = vs.StatusMessage
				}

				status = append(status, e)
			}

			m["VariantStatus"] = status
		}

		if t, ok := instanceType[s.VariantName]; ok && t != "" {
			m["InstanceType"] = t
		}

		out = append(out, m)
	}

	return out
}

func deployedImagesToWire(in []driver.DeployedImage) []map[string]any {
	out := make([]map[string]any, 0, len(in))

	for _, d := range in {
		out = append(out, map[string]any{
			"SpecifiedImage": d.SpecifiedImage,
			"ResolvedImage":  d.ResolvedImage,
			"ResolutionTime": epoch(d.ResolutionTime),
		})
	}

	return out
}

func (h *Handler) updateEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EndpointName                     string          `json:"EndpointName"`
		EndpointConfigName               string          `json:"EndpointConfigName"`
		RetainAllVariantProperties       bool            `json:"RetainAllVariantProperties"`
		RetainDeploymentConfig           bool            `json:"RetainDeploymentConfig"`
		DeploymentConfig                 json.RawMessage `json:"DeploymentConfig"`
		ExcludeRetainedVariantProperties []struct {
			VariantPropertyType string `json:"VariantPropertyType"`
		} `json:"ExcludeRetainedVariantProperties"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	spec := driver.UpdateEndpointSpec{
		EndpointName:               req.EndpointName,
		ConfigName:                 req.EndpointConfigName,
		RetainAllVariantProperties: req.RetainAllVariantProperties,
		RetainDeploymentConfig:     req.RetainDeploymentConfig,
		DeploymentConfig:           req.DeploymentConfig,
	}
	for _, e := range req.ExcludeRetainedVariantProperties {
		spec.ExcludeRetained = append(spec.ExcludeRetained, e.VariantPropertyType)
	}

	ep, err := h.svc.UpdateEndpoint(r.Context(), spec)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"EndpointArn": ep.EndpointARN})
}

func (h *Handler) updateEndpointWeightsAndCapacities(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EndpointName                string `json:"EndpointName"`
		DesiredWeightsAndCapacities []struct {
			VariantName          string   `json:"VariantName"`
			DesiredWeight        *float64 `json:"DesiredWeight"`
			DesiredInstanceCount *int     `json:"DesiredInstanceCount"`
		} `json:"DesiredWeightsAndCapacities"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	weights := make([]driver.VariantWeight, 0, len(req.DesiredWeightsAndCapacities))
	for _, d := range req.DesiredWeightsAndCapacities {
		weights = append(weights, driver.VariantWeight{
			VariantName: d.VariantName, DesiredWeight: d.DesiredWeight, DesiredInstanceCount: d.DesiredInstanceCount,
		})
	}

	ep, err := h.svc.UpdateEndpointWeightsAndCapacities(r.Context(), req.EndpointName, weights)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"EndpointArn": ep.EndpointARN})
}
