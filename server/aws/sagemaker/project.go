package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

type wireProvisioningParameter struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type wireServiceCatalog struct {
	ProductID              string                      `json:"ProductId"`
	PathID                 string                      `json:"PathId,omitempty"`
	ProvisioningArtifactID string                      `json:"ProvisioningArtifactId,omitempty"`
	ProvisioningParameters []wireProvisioningParameter `json:"ProvisioningParameters,omitempty"`
}

type wireCfnTemplate struct {
	TemplateName string                      `json:"TemplateName"`
	TemplateURL  string                      `json:"TemplateURL"`
	RoleARN      string                      `json:"RoleARN,omitempty"`
	Parameters   []wireProvisioningParameter `json:"Parameters,omitempty"`
}

type wireTemplateProvider struct {
	CfnTemplateProvider *wireCfnTemplate `json:"CfnTemplateProvider"`
}

func toParams(in []wireProvisioningParameter) []driver.ProvisioningParameter {
	out := make([]driver.ProvisioningParameter, 0, len(in))
	for _, p := range in {
		out = append(out, driver.ProvisioningParameter{Key: p.Key, Value: p.Value})
	}

	return out
}

func fromParams(in []driver.ProvisioningParameter) []wireProvisioningParameter {
	out := make([]wireProvisioningParameter, 0, len(in))
	for _, p := range in {
		out = append(out, wireProvisioningParameter{Key: p.Key, Value: p.Value})
	}

	return out
}

func toTemplateProviders(in []wireTemplateProvider) []driver.TemplateProvider {
	out := make([]driver.TemplateProvider, 0, len(in))

	for _, t := range in {
		if t.CfnTemplateProvider == nil {
			continue
		}

		c := t.CfnTemplateProvider
		out = append(out, driver.TemplateProvider{
			TemplateName: c.TemplateName, TemplateURL: c.TemplateURL, RoleARN: c.RoleARN, Parameters: toParams(c.Parameters),
		})
	}

	return out
}

func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectName                       string                 `json:"ProjectName"`
		ProjectDescription                string                 `json:"ProjectDescription"`
		ServiceCatalogProvisioningDetails *wireServiceCatalog    `json:"ServiceCatalogProvisioningDetails"`
		TemplateProviders                 []wireTemplateProvider `json:"TemplateProviders"`
		Tags                              []wireTag              `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	spec := driver.ProjectSpec{
		Name: req.ProjectName, Description: req.ProjectDescription,
		TemplateProviders: toTemplateProviders(req.TemplateProviders), Tags: toTags(req.Tags),
	}

	if sc := req.ServiceCatalogProvisioningDetails; sc != nil {
		spec.ServiceCatalog = &driver.ServiceCatalogProvisioning{
			ProductID: sc.ProductID, PathID: sc.PathID, ProvisioningArtifactID: sc.ProvisioningArtifactID,
			Parameters: toParams(sc.ProvisioningParameters),
		}
	}

	p, err := h.svc.CreateProject(r.Context(), spec)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ProjectArn": p.ARN, "ProjectId": p.ID})
}

func (h *Handler) describeProject(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "ProjectName")
	if !ok {
		return
	}

	p, err := h.svc.DescribeProject(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	resp := map[string]any{
		"ProjectName":      p.Name,
		"ProjectArn":       p.ARN,
		"ProjectId":        p.ID,
		"ProjectStatus":    p.Status,
		"CreationTime":     epoch(p.CreationTime),
		"LastModifiedTime": epoch(p.LastModifiedTime),
	}
	setIf(resp, "ProjectDescription", p.Description)

	if p.ServiceCatalog != nil {
		resp["ServiceCatalogProvisioningDetails"] = wireServiceCatalog{
			ProductID: p.ServiceCatalog.ProductID, PathID: p.ServiceCatalog.PathID,
			ProvisioningArtifactID: p.ServiceCatalog.ProvisioningArtifactID,
			ProvisioningParameters: fromParams(p.ServiceCatalog.Parameters),
		}
		resp["ServiceCatalogProvisionedProductDetails"] = map[string]any{
			"ProvisionedProductId": p.ProvisionedProductID, "ProvisionedProductStatusMessage": p.ProvisionedStatusMessage,
		}
	}

	if len(p.TemplateProviders) > 0 {
		details := make([]map[string]any, 0, len(p.TemplateProviders))

		for _, t := range p.TemplateProviders {
			details = append(details, map[string]any{"CfnTemplateProviderDetail": wireCfnTemplate{
				TemplateName: t.TemplateName, TemplateURL: t.TemplateURL, RoleARN: t.RoleARN, Parameters: fromParams(t.Parameters),
			}})
		}

		resp["TemplateProviderDetails"] = details
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListProjects(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "ProjectSummaryList", page, func(p *driver.Project) map[string]any {
		m := map[string]any{
			"ProjectName": p.Name, "ProjectArn": p.ARN, "ProjectId": p.ID, "ProjectStatus": p.Status,
			"CreationTime": epoch(p.CreationTime),
		}
		setIf(m, "ProjectDescription", p.Description)

		return m
	})
}

func (h *Handler) updateProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectName                             string  `json:"ProjectName"`
		ProjectDescription                      *string `json:"ProjectDescription"`
		ServiceCatalogProvisioningUpdateDetails *struct {
			ProvisioningArtifactID string                      `json:"ProvisioningArtifactId"`
			ProvisioningParameters []wireProvisioningParameter `json:"ProvisioningParameters"`
		} `json:"ServiceCatalogProvisioningUpdateDetails"`
		Tags                      []wireTag              `json:"Tags"`
		TemplateProvidersToUpdate []wireTemplateProvider `json:"TemplateProvidersToUpdate"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	up := driver.ProjectUpdate{
		Name: req.ProjectName, Description: req.ProjectDescription,
		TemplateProvidersToUpdate: toTemplateProviders(req.TemplateProvidersToUpdate),
	}

	if req.Tags != nil {
		up.Tags = toTags(req.Tags)
	}

	if d := req.ServiceCatalogProvisioningUpdateDetails; d != nil {
		up.ProvisioningArtifactID = d.ProvisioningArtifactID
		up.Parameters = toParams(d.ProvisioningParameters)
	}

	p, err := h.svc.UpdateProject(r.Context(), up)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ProjectArn": p.ARN})
}

func (h *Handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "ProjectName", h.svc.DeleteProject)
}
