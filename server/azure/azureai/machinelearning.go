// This file serves Microsoft.MachineLearningServices (Azure ML) ARM requests.
//
// Coverage (Microsoft.MachineLearningServices):
//
//	workspaces                              CRUD, list by RG / subscription
//	workspaces/{w}/computes[/{c}[/start|stop|restart]]
//	workspaces/{w}/{online,batch}Endpoints[/{e}[/deployments[/{d}]]]
//	workspaces/{w}/jobs[/{j}[/cancel]]
//	workspaces/{w}/{models,data,environments,components,featuresets}[/{n}[/versions[/{v}]]]
//	workspaces/{w}/datastores[/{d}]
//	workspaces/{w}/connections[/{c}]
//	workspaces/{w}/schedules[/{s}]
//	registries[/{r}]
//
// AML paths nest deeper than the shared azurearm.ParsePath captures, so this
// handler parses the trailing segments itself.
package azureai

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mldriver "github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

const mlProvider = "Microsoft.MachineLearningServices"

// Workspace-child path-segment lengths (p.rest), e.g.
// workspaces/{w}/{coll}/{name}/{sub}/{subName}.
const (
	mlLenWorkspace  = 2 // workspaces/{w}
	mlLenCollection = 3 // .../{coll}
	mlLenChild      = 4 // .../{coll}/{name}
	mlLenSub        = 5 // .../{coll}/{name}/{sub}
)

// Endpoint collection names and the batch-kind label.
const (
	collOnlineEndpoints = "onlineEndpoints"
	collBatchEndpoints  = "batchEndpoints"
	kindBatch           = "batch"
)

// assetTypes are the workspace versioned-asset collections.
//
//nolint:gochecknoglobals // immutable routing set
var assetTypes = map[string]bool{
	"models": true, "data": true, "environments": true, "components": true, "featuresets": true,
}

// MachineLearningHandler serves Microsoft.MachineLearningServices ARM requests.
type MachineLearningHandler struct {
	svc mldriver.MachineLearning
}

// NewMachineLearning returns a handler backed by svc.
func NewMachineLearning(svc mldriver.MachineLearning) *MachineLearningHandler {
	return &MachineLearningHandler{svc: svc}
}

// mlPath is a parsed Azure ML ARM path.
type mlPath struct {
	subscription  string
	resourceGroup string
	rest          []string // segments after the provider name
}

// parseMLPath splits an ARM URL, returning the subscription, optional resource
// group, and the resource segments after Microsoft.MachineLearningServices.
func parseMLPath(urlPath string) (mlPath, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 2 || parts[0] != "subscriptions" {
		return mlPath{}, false
	}

	p := mlPath{subscription: parts[1]}
	i := 2

	if i+1 < len(parts) && parts[i] == "resourceGroups" {
		p.resourceGroup = parts[i+1]
		i += 2
	}

	if i+1 >= len(parts) || parts[i] != "providers" || parts[i+1] != mlProvider {
		return mlPath{}, false
	}

	p.rest = parts[i+2:]

	return p, true
}

// Matches claims Microsoft.MachineLearningServices ARM paths.
func (*MachineLearningHandler) Matches(r *http.Request) bool {
	p, ok := parseMLPath(r.URL.Path)

	return ok && len(p.rest) >= 1
}

// ServeHTTP routes by the top-level resource type.
func (h *MachineLearningHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := parseMLPath(r.URL.Path)
	if !ok || len(p.rest) == 0 {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")

		return
	}

	switch p.rest[0] {
	case "workspaces":
		h.serveWorkspaces(w, r, &p)
	case "registries":
		h.serveRegistries(w, r, &p)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported resource: "+p.rest[0])
	}
}

func writeMLMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}

// --- Workspaces ---

func mlWorkspaceJSON(w *mldriver.MLWorkspace) map[string]any {
	return map[string]any{
		"id": w.ID, "name": w.Name, "type": mlProvider + "/workspaces",
		"location": w.Location, "kind": w.Kind, "tags": w.Tags,
		"properties": map[string]any{
			"friendlyName": w.FriendlyName, "description": w.Description,
			"discoveryUrl": w.DiscoveryURL, "provisioningState": w.ProvisioningState,
		},
	}
}

func (h *MachineLearningHandler) serveWorkspaces(w http.ResponseWriter, r *http.Request, p *mlPath) {
	// Collection: list by RG or subscription.
	if len(p.rest) == 1 {
		if r.Method != http.MethodGet {
			writeMLMethodNotAllowed(w)

			return
		}

		h.listWorkspaces(w, r, p)

		return
	}

	wsName := p.rest[1]

	// Nested child resources.
	if len(p.rest) > mlLenWorkspace {
		h.serveWorkspaceChild(w, r, p, wsName)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createWorkspace(w, r, p, wsName)
	case http.MethodGet:
		h.getWorkspace(w, r, p, wsName)
	case http.MethodPatch:
		h.patchWorkspace(w, r, p, wsName)
	case http.MethodDelete:
		h.deleteWorkspace(w, r, p, wsName)
	default:
		writeMLMethodNotAllowed(w)
	}
}

func (h *MachineLearningHandler) createWorkspace(w http.ResponseWriter, r *http.Request, p *mlPath, name string) {
	var body struct {
		Location   string            `json:"location"`
		Kind       string            `json:"kind"`
		Tags       map[string]string `json:"tags"`
		Properties struct {
			FriendlyName string `json:"friendlyName"`
			Description  string `json:"description"`
		} `json:"properties"`
	}

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	ws, err := h.svc.CreateMLWorkspace(r.Context(), mldriver.MLWorkspaceConfig{
		Name: name, ResourceGroup: p.resourceGroup, Location: body.Location, Kind: body.Kind,
		FriendlyName: body.Properties.FriendlyName, Description: body.Properties.Description, Tags: body.Tags,
	})
	writeML(w, mlWorkspaceJSON, ws, err)
}

func (h *MachineLearningHandler) getWorkspace(w http.ResponseWriter, r *http.Request, p *mlPath, name string) {
	ws, err := h.svc.GetMLWorkspace(r.Context(), p.resourceGroup, name)
	writeML(w, mlWorkspaceJSON, ws, err)
}

func (h *MachineLearningHandler) patchWorkspace(w http.ResponseWriter, r *http.Request, p *mlPath, name string) {
	var body struct {
		Tags map[string]string `json:"tags"`
	}

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	ws, err := h.svc.UpdateMLWorkspaceTags(r.Context(), p.resourceGroup, name, body.Tags)
	writeML(w, mlWorkspaceJSON, ws, err)
}

func (h *MachineLearningHandler) deleteWorkspace(w http.ResponseWriter, r *http.Request, p *mlPath, name string) {
	if err := h.svc.DeleteMLWorkspace(r.Context(), p.resourceGroup, name); err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{})
}

//nolint:dupl // list-then-map shape mirrors the accounts handler.
func (h *MachineLearningHandler) listWorkspaces(w http.ResponseWriter, r *http.Request, p *mlPath) {
	var (
		wss []mldriver.MLWorkspace
		err error
	)

	if p.resourceGroup == "" {
		wss, err = h.svc.ListMLWorkspaces(r.Context())
	} else {
		wss, err = h.svc.ListMLWorkspacesByResourceGroup(r.Context(), p.resourceGroup)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(wss))
	for i := range wss {
		out = append(out, mlWorkspaceJSON(&wss[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}

// serveWorkspaceChild dispatches the nested workspace collections.
func (h *MachineLearningHandler) serveWorkspaceChild(w http.ResponseWriter, r *http.Request, p *mlPath, ws string) {
	coll := p.rest[2]

	switch {
	case coll == "computes":
		h.serveComputes(w, r, p, ws)
	case coll == collOnlineEndpoints || coll == collBatchEndpoints:
		h.serveEndpoints(w, r, p, ws, coll)
	case coll == "jobs":
		h.serveJobs(w, r, p, ws)
	case coll == "datastores":
		h.serveDatastores(w, r, p, ws)
	case coll == "connections":
		h.serveConnections(w, r, p, ws)
	case coll == "schedules":
		h.serveSchedules(w, r, p, ws)
	case assetTypes[coll]:
		h.serveAssets(w, r, p, ws, coll)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported workspace child: "+coll)
	}
}

// --- Registries ---

func registryJSON(r *mldriver.Registry) map[string]any {
	return map[string]any{
		"id": r.ID, "name": r.Name, "type": mlProvider + "/registries",
		"location": r.Location, "tags": r.Tags,
		"properties": map[string]any{"description": r.Description, "provisioningState": r.ProvisioningState},
	}
}

func (h *MachineLearningHandler) serveRegistries(w http.ResponseWriter, r *http.Request, p *mlPath) {
	if len(p.rest) == 1 {
		if r.Method != http.MethodGet {
			writeMLMethodNotAllowed(w)

			return
		}

		regs, err := h.svc.ListRegistries(r.Context(), p.resourceGroup)
		if err != nil {
			azurearm.WriteCErr(w, err)

			return
		}

		out := make([]map[string]any, 0, len(regs))
		for i := range regs {
			out = append(out, registryJSON(&regs[i]))
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})

		return
	}

	name := p.rest[1]

	switch r.Method {
	case http.MethodPut:
		var body struct {
			Location   string            `json:"location"`
			Tags       map[string]string `json:"tags"`
			Properties struct {
				Description string `json:"description"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		reg, err := h.svc.CreateRegistry(r.Context(), mldriver.RegistryConfig{
			Name: name, ResourceGroup: p.resourceGroup, Location: body.Location,
			Description: body.Properties.Description, Tags: body.Tags,
		})
		writeML(w, registryJSON, reg, err)
	case http.MethodGet:
		reg, err := h.svc.GetRegistry(r.Context(), p.resourceGroup, name)
		writeML(w, registryJSON, reg, err)
	case http.MethodDelete:
		if err := h.svc.DeleteRegistry(r.Context(), p.resourceGroup, name); err != nil {
			azurearm.WriteCErr(w, err)

			return
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{})
	default:
		writeMLMethodNotAllowed(w)
	}
}

// writeML writes a created/fetched resource or maps the error.
func writeML[T any](w http.ResponseWriter, toJSON func(*T) map[string]any, res *T, err error) {
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toJSON(res))
}
