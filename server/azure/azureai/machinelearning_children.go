package azureai

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mldriver "github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

func endpointKind(collection string) string {
	if collection == collBatchEndpoints {
		return kindBatch
	}

	return "online"
}

// --- Computes ---

func computeJSON(c *mldriver.Compute) map[string]any {
	return map[string]any{
		"id": c.ID, "name": c.Name, "type": mlProvider + "/workspaces/computes",
		"properties": map[string]any{
			"computeType": c.ComputeType, "provisioningState": c.ProvisioningState,
			"properties": map[string]any{
				"vmSize": c.VMSize, "state": c.State,
				"scaleSettings": map[string]any{"minNodeCount": c.MinNodes, "maxNodeCount": c.MaxNodes},
			},
		},
	}
}

func (h *MachineLearningHandler) serveComputes(w http.ResponseWriter, r *http.Request, p *mlPath, ws string) {
	if len(p.rest) == mlLenCollection {
		h.listComputesOrMethod(w, r, p, ws)

		return
	}

	name := p.rest[3]

	// .../computes/{c}/{action}
	if len(p.rest) > mlLenChild {
		h.computeAction(w, r, p, ws, name, p.rest[4])

		return
	}

	switch r.Method {
	case http.MethodPut:
		var body struct {
			Properties struct {
				ComputeType string `json:"computeType"`
				Properties  struct {
					VMSize        string `json:"vmSize"`
					ScaleSettings struct {
						MinNodeCount int `json:"minNodeCount"`
						MaxNodeCount int `json:"maxNodeCount"`
					} `json:"scaleSettings"`
				} `json:"properties"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		c, err := h.svc.CreateCompute(r.Context(), mldriver.ComputeConfig{
			Workspace: ws, ResourceGroup: p.resourceGroup, Name: name,
			ComputeType: body.Properties.ComputeType, VMSize: body.Properties.Properties.VMSize,
			MinNodes: body.Properties.Properties.ScaleSettings.MinNodeCount,
			MaxNodes: body.Properties.Properties.ScaleSettings.MaxNodeCount,
		})
		writeML(w, computeJSON, c, err)
	case http.MethodGet:
		c, err := h.svc.GetCompute(r.Context(), p.resourceGroup, ws, name)
		writeML(w, computeJSON, c, err)
	case http.MethodDelete:
		writeMLDelete(w, h.svc.DeleteCompute(r.Context(), p.resourceGroup, ws, name))
	default:
		writeMLMethodNotAllowed(w)
	}
}

func (h *MachineLearningHandler) listComputesOrMethod(w http.ResponseWriter, r *http.Request, p *mlPath, ws string) {
	if r.Method != http.MethodGet {
		writeMLMethodNotAllowed(w)

		return
	}

	cs, err := h.svc.ListComputes(r.Context(), p.resourceGroup, ws)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(cs))
	for i := range cs {
		out = append(out, computeJSON(&cs[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}

func (h *MachineLearningHandler) computeAction(w http.ResponseWriter, r *http.Request, p *mlPath, ws, name, action string) {
	if r.Method != http.MethodPost {
		writeMLMethodNotAllowed(w)

		return
	}

	var err error

	switch action {
	case "start":
		err = h.svc.StartCompute(r.Context(), p.resourceGroup, ws, name)
	case "stop":
		err = h.svc.StopCompute(r.Context(), p.resourceGroup, ws, name)
	case "restart":
		err = h.svc.RestartCompute(r.Context(), p.resourceGroup, ws, name)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown compute action: "+action)

		return
	}

	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{})
}

// --- Endpoints + deployments ---

func endpointJSON(e *mldriver.Endpoint) map[string]any {
	return map[string]any{
		"id": e.ID, "name": e.Name, "type": mlProvider + "/workspaces/" + endpointCollection(e.Kind),
		"properties": map[string]any{
			"authMode": e.AuthMode, "description": e.Description,
			"scoringUri": e.ScoringURI, "provisioningState": e.ProvisioningState, "traffic": e.Traffic,
		},
	}
}

func endpointCollection(kind string) string {
	if kind == kindBatch {
		return collBatchEndpoints
	}

	return collOnlineEndpoints
}

func endpointDeploymentJSON(d *mldriver.EndpointDeployment) map[string]any {
	return map[string]any{
		"id": d.ID, "name": d.Name,
		"properties": map[string]any{
			"model": d.Model, "instanceType": d.InstanceType,
			"provisioningState": d.ProvisioningState,
		},
		"sku": map[string]any{"capacity": d.InstanceCount},
	}
}

func (h *MachineLearningHandler) serveEndpoints(w http.ResponseWriter, r *http.Request, p *mlPath, ws, coll string) {
	kind := endpointKind(coll)

	if len(p.rest) == mlLenCollection {
		h.listEndpoints(w, r, p, ws, kind)

		return
	}

	ep := p.rest[3]

	// Nested deployments: .../{online,batch}Endpoints/{e}/deployments[/{d}]
	if len(p.rest) > mlLenChild && p.rest[4] == "deployments" {
		h.serveEndpointDeployments(w, r, p, ws, kind, ep)

		return
	}

	switch r.Method {
	case http.MethodPut:
		var body struct {
			Properties struct {
				AuthMode    string `json:"authMode"`
				Description string `json:"description"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		e, err := h.svc.CreateEndpoint(r.Context(), mldriver.EndpointConfig{
			Workspace: ws, ResourceGroup: p.resourceGroup, Name: ep, Kind: kind,
			AuthMode: body.Properties.AuthMode, Description: body.Properties.Description,
		})
		writeML(w, endpointJSON, e, err)
	case http.MethodGet:
		e, err := h.svc.GetEndpoint(r.Context(), p.resourceGroup, ws, kind, ep)
		writeML(w, endpointJSON, e, err)
	case http.MethodDelete:
		writeMLDelete(w, h.svc.DeleteEndpoint(r.Context(), p.resourceGroup, ws, kind, ep))
	default:
		writeMLMethodNotAllowed(w)
	}
}

func (h *MachineLearningHandler) listEndpoints(w http.ResponseWriter, r *http.Request, p *mlPath, ws, kind string) {
	if r.Method != http.MethodGet {
		writeMLMethodNotAllowed(w)

		return
	}

	eps, err := h.svc.ListEndpoints(r.Context(), p.resourceGroup, ws, kind)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(eps))
	for i := range eps {
		out = append(out, endpointJSON(&eps[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}

func (h *MachineLearningHandler) serveEndpointDeployments(w http.ResponseWriter, r *http.Request, p *mlPath, ws, kind, ep string) {
	// .../deployments[/{d}]
	if len(p.rest) == mlLenSub {
		if r.Method != http.MethodGet {
			writeMLMethodNotAllowed(w)

			return
		}

		ds, err := h.svc.ListEndpointDeployments(r.Context(), p.resourceGroup, ws, kind, ep)
		if err != nil {
			azurearm.WriteCErr(w, err)

			return
		}

		out := make([]map[string]any, 0, len(ds))
		for i := range ds {
			out = append(out, endpointDeploymentJSON(&ds[i]))
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})

		return
	}

	name := p.rest[5]

	switch r.Method {
	case http.MethodPut:
		var body struct {
			Properties struct {
				Model        string `json:"model"`
				InstanceType string `json:"instanceType"`
			} `json:"properties"`
			SKU struct {
				Capacity int `json:"capacity"`
			} `json:"sku"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		d, err := h.svc.CreateEndpointDeployment(r.Context(), mldriver.EndpointDeploymentConfig{
			Workspace: ws, ResourceGroup: p.resourceGroup, Endpoint: ep, EndpointKind: kind, Name: name,
			Model: body.Properties.Model, InstanceType: body.Properties.InstanceType, InstanceCount: body.SKU.Capacity,
		})
		writeML(w, endpointDeploymentJSON, d, err)
	case http.MethodGet:
		d, err := h.svc.GetEndpointDeployment(r.Context(), p.resourceGroup, ws, kind, ep, name)
		writeML(w, endpointDeploymentJSON, d, err)
	case http.MethodDelete:
		writeMLDelete(w, h.svc.DeleteEndpointDeployment(r.Context(), p.resourceGroup, ws, kind, ep, name))
	default:
		writeMLMethodNotAllowed(w)
	}
}

// --- Jobs ---

func jobJSON(j *mldriver.Job) map[string]any {
	return map[string]any{
		"id": j.ID, "name": j.Name, "type": mlProvider + "/workspaces/jobs",
		"properties": map[string]any{"jobType": j.JobType, "displayName": j.DisplayName, "status": j.Status},
	}
}

//nolint:gocyclo // flat dispatch over list/cancel/CRUD job paths.
func (h *MachineLearningHandler) serveJobs(w http.ResponseWriter, r *http.Request, p *mlPath, ws string) {
	if len(p.rest) == mlLenCollection {
		if r.Method != http.MethodGet {
			writeMLMethodNotAllowed(w)

			return
		}

		js, err := h.svc.ListJobs(r.Context(), p.resourceGroup, ws)
		if err != nil {
			azurearm.WriteCErr(w, err)

			return
		}

		out := make([]map[string]any, 0, len(js))
		for i := range js {
			out = append(out, jobJSON(&js[i]))
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})

		return
	}

	name := p.rest[3]

	if len(p.rest) > mlLenChild && p.rest[4] == "cancel" {
		if r.Method != http.MethodPost {
			writeMLMethodNotAllowed(w)

			return
		}

		writeMLDelete(w, h.svc.CancelJob(r.Context(), p.resourceGroup, ws, name))

		return
	}

	switch r.Method {
	case http.MethodPut:
		var body struct {
			Properties struct {
				JobType     string `json:"jobType"`
				DisplayName string `json:"displayName"`
				ComputeID   string `json:"computeId"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		j, err := h.svc.CreateJob(r.Context(), mldriver.JobConfig{
			Workspace: ws, ResourceGroup: p.resourceGroup, Name: name,
			JobType: body.Properties.JobType, DisplayName: body.Properties.DisplayName, ComputeID: body.Properties.ComputeID,
		})
		writeML(w, jobJSON, j, err)
	case http.MethodGet:
		j, err := h.svc.GetJob(r.Context(), p.resourceGroup, ws, name)
		writeML(w, jobJSON, j, err)
	default:
		writeMLMethodNotAllowed(w)
	}
}

// --- Versioned assets ---

func assetJSON(a *mldriver.Asset) map[string]any {
	return map[string]any{
		"id": a.ID, "name": a.Version, "type": mlProvider + "/workspaces/" + a.AssetType + "/versions",
		"properties": map[string]any{
			"description": a.Description, "path": a.Path, "properties": a.Properties,
		},
	}
}

//nolint:gocyclo // flat dispatch over container/version path shapes.
func (h *MachineLearningHandler) serveAssets(w http.ResponseWriter, r *http.Request, p *mlPath, ws, assetType string) {
	// .../{assetType}                               -> list containers
	// .../{assetType}/{name}/versions               -> list versions
	// .../{assetType}/{name}/versions/{version}      -> version CRUD
	if len(p.rest) == mlLenCollection {
		h.listAssetContainers(w, r, p, ws, assetType)

		return
	}

	name := p.rest[3]

	if len(p.rest) < mlLenSub || p.rest[4] != "versions" {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "expected /versions under asset container")

		return
	}

	if len(p.rest) == mlLenSub {
		if r.Method != http.MethodGet {
			writeMLMethodNotAllowed(w)

			return
		}

		vs, err := h.svc.ListAssetVersions(r.Context(), p.resourceGroup, ws, assetType, name)
		if err != nil {
			azurearm.WriteCErr(w, err)

			return
		}

		out := make([]map[string]any, 0, len(vs))
		for i := range vs {
			out = append(out, assetJSON(&vs[i]))
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})

		return
	}

	version := p.rest[5]

	switch r.Method {
	case http.MethodPut:
		var body struct {
			Properties struct {
				Description string            `json:"description"`
				Path        string            `json:"path"`
				Properties  map[string]string `json:"properties"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		a, err := h.svc.CreateAsset(r.Context(), mldriver.AssetConfig{
			Workspace: ws, ResourceGroup: p.resourceGroup, AssetType: assetType, Name: name, Version: version,
			Description: body.Properties.Description, Path: body.Properties.Path, Properties: body.Properties.Properties,
		})
		writeML(w, assetJSON, a, err)
	case http.MethodGet:
		a, err := h.svc.GetAsset(r.Context(), p.resourceGroup, ws, assetType, name, version)
		writeML(w, assetJSON, a, err)
	case http.MethodDelete:
		writeMLDelete(w, h.svc.DeleteAsset(r.Context(), p.resourceGroup, ws, assetType, name, version))
	default:
		writeMLMethodNotAllowed(w)
	}
}

func (h *MachineLearningHandler) listAssetContainers(w http.ResponseWriter, r *http.Request, p *mlPath, ws, assetType string) {
	if r.Method != http.MethodGet {
		writeMLMethodNotAllowed(w)

		return
	}

	cs, err := h.svc.ListAssetContainers(r.Context(), p.resourceGroup, ws, assetType)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(cs))
	for i := range cs {
		out = append(out, map[string]any{
			"id": cs[i].ID, "name": cs[i].Name, "type": mlProvider + "/workspaces/" + assetType,
			"properties": map[string]any{"latestVersion": cs[i].Version, "description": cs[i].Description},
		})
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}

func writeMLDelete(w http.ResponseWriter, err error) {
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{})
}
