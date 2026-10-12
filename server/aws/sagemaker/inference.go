package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// wireContainer is the JSON shape of a model container definition.
type wireContainer struct {
	Image        string            `json:"Image"`
	ModelDataURL string            `json:"ModelDataUrl,omitempty"`
	Environment  map[string]string `json:"Environment,omitempty"`
	Mode         string            `json:"Mode,omitempty"`
}

func toContainer(c wireContainer) driver.ContainerDefinition {
	return driver.ContainerDefinition{
		Image: c.Image, ModelDataURL: c.ModelDataURL, Environment: c.Environment, Mode: c.Mode,
	}
}

func fromContainer(c driver.ContainerDefinition) wireContainer {
	return wireContainer{
		Image: c.Image, ModelDataURL: c.ModelDataURL, Environment: c.Environment, Mode: c.Mode,
	}
}

func (h *Handler) createModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelName        string          `json:"ModelName"`
		ExecutionRoleArn string          `json:"ExecutionRoleArn"`
		PrimaryContainer *wireContainer  `json:"PrimaryContainer"`
		Containers       []wireContainer `json:"Containers"`
		Tags             []wireTag       `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	containers := make([]driver.ContainerDefinition, 0)
	if req.PrimaryContainer != nil {
		containers = append(containers, toContainer(*req.PrimaryContainer))
	}

	for _, c := range req.Containers {
		containers = append(containers, toContainer(c))
	}

	// An inference-pipeline model is created via Containers; a single-container
	// model via PrimaryContainer. Track which so Describe echoes only that field.
	pipeline := req.PrimaryContainer == nil && len(req.Containers) > 0

	model, err := h.svc.CreateModel(r.Context(), driver.ModelConfig{
		ModelName:  req.ModelName,
		RoleARN:    req.ExecutionRoleArn,
		Containers: containers,
		Pipeline:   pipeline,
		Tags:       toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ModelArn": model.ModelARN})
}

func (h *Handler) describeModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelName string `json:"ModelName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	model, err := h.svc.DescribeModel(r.Context(), req.ModelName)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	resp := map[string]any{
		"ModelName":        model.ModelName,
		"ModelArn":         model.ModelARN,
		"ExecutionRoleArn": model.RoleARN,
		"CreationTime":     epoch(model.CreationTime),
	}
	// Echo only the field the model was created with: a pipeline model reports
	// Containers; a single-container model reports PrimaryContainer.
	switch {
	case model.Pipeline:
		conts := make([]wireContainer, 0, len(model.Containers))
		for _, c := range model.Containers {
			conts = append(conts, fromContainer(c))
		}

		resp["Containers"] = conts
	case len(model.Containers) > 0:
		resp["PrimaryContainer"] = fromContainer(model.Containers[0])
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) listModels(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	models, err := h.svc.ListModels(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "Models", models, func(m *driver.Model) map[string]any {
		return map[string]any{
			"ModelName":    m.ModelName,
			"ModelArn":     m.ModelARN,
			"CreationTime": epoch(m.CreationTime),
		}
	})
}

func (h *Handler) deleteModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelName string `json:"ModelName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.svc.DeleteModel(r.Context(), req.ModelName); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// wireVariant is the JSON shape of a production variant.
type wireVariant struct {
	VariantName          string          `json:"VariantName"`
	ModelName            string          `json:"ModelName,omitempty"`
	InstanceType         string          `json:"InstanceType,omitempty"`
	InitialInstanceCount int             `json:"InitialInstanceCount,omitempty"`
	InitialVariantWeight *float64        `json:"InitialVariantWeight,omitempty"`
	ServerlessConfig     *wireServerless `json:"ServerlessConfig,omitempty"`
}

type wireServerless struct {
	MemorySizeInMB int `json:"MemorySizeInMB"`
	MaxConcurrency int `json:"MaxConcurrency"`
}

func (h *Handler) createEndpointConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EndpointConfigName string `json:"EndpointConfigName"`
		ProductionVariants []struct {
			wireVariant
			// InitialVariantWeight shadows the embedded field so an omitted
			// weight can default to 1 (an explicit 0 is a valid weight).
			InitialVariantWeight *float64 `json:"InitialVariantWeight"`
		} `json:"ProductionVariants"`
		AsyncInferenceConfig *struct {
			OutputConfig struct {
				S3OutputPath string `json:"S3OutputPath"`
			} `json:"OutputConfig"`
		} `json:"AsyncInferenceConfig"`
		Tags []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	spec := driver.EndpointConfigSpec{ConfigName: req.EndpointConfigName, Tags: toTags(req.Tags)}
	if req.AsyncInferenceConfig != nil {
		spec.AsyncOutputS3URI = req.AsyncInferenceConfig.OutputConfig.S3OutputPath
	}

	for _, v := range req.ProductionVariants {
		spec.ProductionVariants = append(spec.ProductionVariants, driver.ProductionVariant{
			VariantName:          v.VariantName,
			ModelName:            v.ModelName,
			InstanceType:         v.InstanceType,
			InitialInstanceCount: v.InitialInstanceCount,
			InitialVariantWeight: weightOrDefault(v.InitialVariantWeight),
		})
		if v.ServerlessConfig != nil {
			spec.Serverless = &driver.ServerlessConfig{
				MemorySizeInMB: v.ServerlessConfig.MemorySizeInMB,
				MaxConcurrency: v.ServerlessConfig.MaxConcurrency,
			}
		}
	}

	ec, err := h.svc.CreateEndpointConfig(r.Context(), spec)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"EndpointConfigArn": ec.ConfigARN})
}

func (h *Handler) describeEndpointConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EndpointConfigName string `json:"EndpointConfigName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ec, err := h.svc.DescribeEndpointConfig(r.Context(), req.EndpointConfigName)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	resp := map[string]any{
		"EndpointConfigName": ec.ConfigName,
		"EndpointConfigArn":  ec.ConfigARN,
		"ProductionVariants": variantsToWire(ec.ProductionVariants, ec.Serverless),
		"CreationTime":       epoch(ec.CreationTime),
	}
	if ec.AsyncOutputS3URI != "" {
		resp["AsyncInferenceConfig"] = map[string]any{"OutputConfig": map[string]any{"S3OutputPath": ec.AsyncOutputS3URI}}
	}

	wire.WriteJSON(w, resp)
}

//nolint:dupl // per-resource shim mirrors the SDK shape
func (h *Handler) listEndpointConfigs(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListEndpointConfigs(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "EndpointConfigs", page, func(c *driver.EndpointConfig) map[string]any {
		return map[string]any{
			"EndpointConfigName": c.ConfigName,
			"EndpointConfigArn":  c.ConfigARN,
			"CreationTime":       epoch(c.CreationTime),
		}
	})
}

func (h *Handler) deleteEndpointConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EndpointConfigName string `json:"EndpointConfigName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.svc.DeleteEndpointConfig(r.Context(), req.EndpointConfigName); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// weightOrDefault returns an omitted InitialVariantWeight as SageMaker's
// default of 1.
func weightOrDefault(w *float64) float64 {
	if w == nil {
		return 1
	}

	return *w
}

func variantsToWire(in []driver.ProductionVariant, serverless *driver.ServerlessConfig) []wireVariant {
	out := make([]wireVariant, 0, len(in))

	for i := range in {
		v := &in[i]
		weight := v.InitialVariantWeight // an explicit 0 is a valid weight, so it is always emitted
		wv := wireVariant{
			VariantName:          v.VariantName,
			ModelName:            v.ModelName,
			InstanceType:         v.InstanceType,
			InitialInstanceCount: v.InitialInstanceCount,
			InitialVariantWeight: &weight,
		}

		if serverless != nil {
			wv.ServerlessConfig = &wireServerless{MemorySizeInMB: serverless.MemorySizeInMB, MaxConcurrency: serverless.MaxConcurrency}
		}

		out = append(out, wv)
	}

	return out
}

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EndpointName       string    `json:"EndpointName"`
		EndpointConfigName string    `json:"EndpointConfigName"`
		Tags               []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ep, err := h.svc.CreateEndpoint(r.Context(), driver.EndpointSpec{
		EndpointName: req.EndpointName,
		ConfigName:   req.EndpointConfigName,
		Tags:         toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"EndpointArn": ep.EndpointARN})
}

func (h *Handler) listEndpoints(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	eps, err := h.svc.ListEndpoints(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "Endpoints", eps, func(e *driver.Endpoint) map[string]any {
		return map[string]any{
			"EndpointName":     e.EndpointName,
			"EndpointArn":      e.EndpointARN,
			"EndpointStatus":   e.Status,
			"CreationTime":     epoch(e.CreationTime),
			"LastModifiedTime": epoch(e.LastModifiedTime),
		}
	})
}

func (h *Handler) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EndpointName string `json:"EndpointName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.svc.DeleteEndpoint(r.Context(), req.EndpointName); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}
