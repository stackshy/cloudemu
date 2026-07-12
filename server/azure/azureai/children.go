package azureai

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	csdriver "github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

// serveChild dispatches account sub-resource requests by collection, method,
// and whether a child name is present.
func (h *CognitiveServicesHandler) serveChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)

			return
		}

		h.listChildren(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putChild(w, r, rp)
	case http.MethodGet:
		h.getChild(w, r, rp)
	case http.MethodDelete:
		h.deleteChild(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func deploymentJSON(d *csdriver.Deployment) map[string]any {
	return map[string]any{
		"id": d.ID, "name": d.Name, "type": csProvider + "/accounts/deployments",
		"sku": map[string]any{"name": d.SKUName, "capacity": d.SKUCapacity},
		"properties": map[string]any{
			"provisioningState": d.ProvisioningState,
			"model": map[string]any{
				"name": d.ModelName, "version": d.ModelVersion, "format": d.ModelFormat,
			},
		},
	}
}

func projectJSON(p *csdriver.Project) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "type": csProvider + "/accounts/projects",
		"location": p.Location, "tags": p.Tags,
		"properties": map[string]any{
			"displayName": p.DisplayName, "description": p.Description,
			"provisioningState": p.ProvisioningState,
		},
	}
}

func raiPolicyJSON(p *csdriver.RaiPolicy) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "type": csProvider + "/accounts/raiPolicies",
		"properties": map[string]any{"mode": p.Mode, "basePolicyName": p.BasePolicy},
	}
}

func commitmentPlanJSON(p *csdriver.CommitmentPlan) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "type": csProvider + "/accounts/commitmentPlans",
		"properties": map[string]any{
			"planType": p.PlanType, "autoRenew": p.AutoRenew,
			"provisioningState": p.ProvisioningState,
		},
	}
}

func pecJSON(c *csdriver.PrivateEndpointConnection) map[string]any {
	return map[string]any{
		"id": c.ID, "name": c.Name, "type": csProvider + "/accounts/privateEndpointConnections",
		"properties": map[string]any{
			"provisioningState": c.ProvisioningState,
			"privateLinkServiceConnectionState": map[string]any{
				"status": c.Status, "description": c.Description,
			},
		},
	}
}

//nolint:gocyclo,funlen // flat per-collection dispatch; one decode+case per child type.
func (h *CognitiveServicesHandler) putChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rg, account, name := rp.ResourceGroup, rp.ResourceName, rp.SubResourceName

	switch rp.SubResource {
	case collDeployments:
		var body struct {
			SKU *struct {
				Name     string `json:"name"`
				Capacity int    `json:"capacity"`
			} `json:"sku"`
			Properties struct {
				Model struct {
					Name    string `json:"name"`
					Version string `json:"version"`
					Format  string `json:"format"`
				} `json:"model"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		cfg := csdriver.DeploymentConfig{
			Account: account, ResourceGroup: rg, Name: name,
			ModelName:    body.Properties.Model.Name,
			ModelVersion: body.Properties.Model.Version,
			ModelFormat:  body.Properties.Model.Format,
		}
		if body.SKU != nil {
			cfg.SKUName = body.SKU.Name
			cfg.SKUCapacity = body.SKU.Capacity
		}

		d, err := h.svc.CreateDeployment(r.Context(), cfg)
		writeChild(w, deploymentJSON, d, err)
	case collProjects:
		var body struct {
			Location   string            `json:"location"`
			Tags       map[string]string `json:"tags"`
			Properties struct {
				DisplayName string `json:"displayName"`
				Description string `json:"description"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		p, err := h.svc.CreateProject(r.Context(), csdriver.ProjectConfig{
			Account: account, ResourceGroup: rg, Name: name, Location: body.Location,
			DisplayName: body.Properties.DisplayName, Description: body.Properties.Description, Tags: body.Tags,
		})
		writeChild(w, projectJSON, p, err)
	case collRaiPolicies:
		var body struct {
			Properties struct {
				Mode           string `json:"mode"`
				BasePolicyName string `json:"basePolicyName"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		p, err := h.svc.CreateRaiPolicy(r.Context(), csdriver.RaiPolicyConfig{
			Account: account, ResourceGroup: rg, Name: name,
			Mode: body.Properties.Mode, BasePolicy: body.Properties.BasePolicyName,
		})
		writeChild(w, raiPolicyJSON, p, err)
	case collCommitmentPlans:
		var body struct {
			Properties struct {
				PlanType  string `json:"planType"`
				AutoRenew bool   `json:"autoRenew"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		p, err := h.svc.CreateCommitmentPlan(r.Context(), csdriver.CommitmentPlanConfig{
			Account: account, ResourceGroup: rg, Name: name,
			PlanType: body.Properties.PlanType, AutoRenew: body.Properties.AutoRenew,
		})
		writeChild(w, commitmentPlanJSON, p, err)
	case collPEC:
		var body struct {
			Properties struct {
				State struct {
					Status      string `json:"status"`
					Description string `json:"description"`
				} `json:"privateLinkServiceConnectionState"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		c, err := h.svc.PutPrivateEndpointConnection(r.Context(), rg, account, name, body.Properties.State.Status)
		writeChild(w, pecJSON, c, err)
	}
}

func (h *CognitiveServicesHandler) getChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rg, account, name := rp.ResourceGroup, rp.ResourceName, rp.SubResourceName

	switch rp.SubResource {
	case collDeployments:
		d, err := h.svc.GetDeployment(r.Context(), rg, account, name)
		writeChild(w, deploymentJSON, d, err)
	case collProjects:
		p, err := h.svc.GetProject(r.Context(), rg, account, name)
		writeChild(w, projectJSON, p, err)
	case collRaiPolicies:
		p, err := h.svc.GetRaiPolicy(r.Context(), rg, account, name)
		writeChild(w, raiPolicyJSON, p, err)
	case collCommitmentPlans:
		p, err := h.svc.GetCommitmentPlan(r.Context(), rg, account, name)
		writeChild(w, commitmentPlanJSON, p, err)
	case collPEC:
		c, err := h.svc.GetPrivateEndpointConnection(r.Context(), rg, account, name)
		writeChild(w, pecJSON, c, err)
	}
}

func (h *CognitiveServicesHandler) deleteChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rg, account, name := rp.ResourceGroup, rp.ResourceName, rp.SubResourceName

	var err error

	switch rp.SubResource {
	case collDeployments:
		err = h.svc.DeleteDeployment(r.Context(), rg, account, name)
	case collProjects:
		err = h.svc.DeleteProject(r.Context(), rg, account, name)
	case collRaiPolicies:
		err = h.svc.DeleteRaiPolicy(r.Context(), rg, account, name)
	case collCommitmentPlans:
		err = h.svc.DeleteCommitmentPlan(r.Context(), rg, account, name)
	case collPEC:
		err = h.svc.DeletePrivateEndpointConnection(r.Context(), rg, account, name)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{})
}

//nolint:gocyclo // flat per-collection dispatch; one case per child type.
func (h *CognitiveServicesHandler) listChildren(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rg, account := rp.ResourceGroup, rp.ResourceName

	var (
		out []map[string]any
		err error
	)

	switch rp.SubResource {
	case collDeployments:
		var ds []csdriver.Deployment

		ds, err = h.svc.ListDeployments(r.Context(), rg, account)
		for i := range ds {
			out = append(out, deploymentJSON(&ds[i]))
		}
	case collProjects:
		var ps []csdriver.Project

		ps, err = h.svc.ListProjects(r.Context(), rg, account)
		for i := range ps {
			out = append(out, projectJSON(&ps[i]))
		}
	case collRaiPolicies:
		var ps []csdriver.RaiPolicy

		ps, err = h.svc.ListRaiPolicies(r.Context(), rg, account)
		for i := range ps {
			out = append(out, raiPolicyJSON(&ps[i]))
		}
	case collCommitmentPlans:
		var ps []csdriver.CommitmentPlan

		ps, err = h.svc.ListCommitmentPlans(r.Context(), rg, account)
		for i := range ps {
			out = append(out, commitmentPlanJSON(&ps[i]))
		}
	case collPEC:
		var cs []csdriver.PrivateEndpointConnection

		cs, err = h.svc.ListPrivateEndpointConnections(r.Context(), rg, account)
		for i := range cs {
			out = append(out, pecJSON(&cs[i]))
		}
	}

	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	if out == nil {
		out = []map[string]any{}
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}

// writeChild writes a created/fetched child resource or maps the error.
func writeChild[T any](w http.ResponseWriter, toJSON func(*T) map[string]any, res *T, err error) {
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toJSON(res))
}
