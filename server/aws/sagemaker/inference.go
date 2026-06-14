package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/sagemaker/driver"
	"github.com/stackshy/cloudemu/server/wire"
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

	model, err := h.svc.CreateModel(r.Context(), driver.ModelConfig{
		ModelName:  req.ModelName,
		RoleARN:    req.ExecutionRoleArn,
		Containers: containers,
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
	if len(model.Containers) > 0 {
		resp["PrimaryContainer"] = fromContainer(model.Containers[0])

		conts := make([]wireContainer, 0, len(model.Containers))
		for _, c := range model.Containers {
			conts = append(conts, fromContainer(c))
		}

		resp["Containers"] = conts
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) listModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.svc.ListModels(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(models))
	for i := range models {
		out = append(out, map[string]any{
			"ModelName":    models[i].ModelName,
			"ModelArn":     models[i].ModelARN,
			"CreationTime": epoch(models[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"Models": out})
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
	InitialVariantWeight float64         `json:"InitialVariantWeight,omitempty"`
	ServerlessConfig     *wireServerless `json:"ServerlessConfig,omitempty"`
}

type wireServerless struct {
	MemorySizeInMB int `json:"MemorySizeInMB"`
	MaxConcurrency int `json:"MaxConcurrency"`
}

func (h *Handler) createEndpointConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EndpointConfigName string        `json:"EndpointConfigName"`
		ProductionVariants []wireVariant `json:"ProductionVariants"`
		Tags               []wireTag     `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	spec := driver.EndpointConfigSpec{ConfigName: req.EndpointConfigName, Tags: toTags(req.Tags)}
	for _, v := range req.ProductionVariants {
		spec.ProductionVariants = append(spec.ProductionVariants, driver.ProductionVariant{
			VariantName:          v.VariantName,
			ModelName:            v.ModelName,
			InstanceType:         v.InstanceType,
			InitialInstanceCount: v.InitialInstanceCount,
			InitialVariantWeight: v.InitialVariantWeight,
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

	wire.WriteJSON(w, map[string]any{
		"EndpointConfigName": ec.ConfigName,
		"EndpointConfigArn":  ec.ConfigARN,
		"ProductionVariants": variantsToWire(ec.ProductionVariants),
		"CreationTime":       epoch(ec.CreationTime),
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

func variantsToWire(in []driver.ProductionVariant) []wireVariant {
	out := make([]wireVariant, 0, len(in))
	for _, v := range in {
		out = append(out, wireVariant{
			VariantName:          v.VariantName,
			ModelName:            v.ModelName,
			InstanceType:         v.InstanceType,
			InitialInstanceCount: v.InitialInstanceCount,
			InitialVariantWeight: v.InitialVariantWeight,
		})
	}

	return out
}

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

	wire.WriteJSON(w, map[string]any{
		"EndpointName":       ep.EndpointName,
		"EndpointArn":        ep.EndpointARN,
		"EndpointConfigName": ep.ConfigName,
		"EndpointStatus":     ep.Status,
		"ProductionVariants": variantsToWire(ep.Variants),
		"CreationTime":       epoch(ep.CreationTime),
		"LastModifiedTime":   epoch(ep.LastModifiedTime),
	})
}

//nolint:dupl // the list-summaries shape recurs across resources; sharing it would obscure each surface.
func (h *Handler) listEndpoints(w http.ResponseWriter, r *http.Request) {
	eps, err := h.svc.ListEndpoints(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(eps))
	for i := range eps {
		out = append(out, map[string]any{
			"EndpointName":     eps[i].EndpointName,
			"EndpointArn":      eps[i].EndpointARN,
			"EndpointStatus":   eps[i].Status,
			"CreationTime":     epoch(eps[i].CreationTime),
			"LastModifiedTime": epoch(eps[i].LastModifiedTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"Endpoints": out})
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
