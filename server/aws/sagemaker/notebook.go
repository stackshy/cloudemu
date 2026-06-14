package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/sagemaker/driver"
	"github.com/stackshy/cloudemu/server/wire"
)

func (h *Handler) routeNotebook(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateNotebookInstance":
		h.createNotebookInstance(w, r)
	case "DescribeNotebookInstance":
		h.describeNotebookInstance(w, r)
	case "ListNotebookInstances":
		h.listNotebookInstances(w, r)
	case "StartNotebookInstance":
		stopByName(w, r, "NotebookInstanceName", h.svc.StartNotebookInstance)
	case "StopNotebookInstance":
		stopByName(w, r, "NotebookInstanceName", h.svc.StopNotebookInstance)
	case "DeleteNotebookInstance":
		stopByName(w, r, "NotebookInstanceName", h.svc.DeleteNotebookInstance)
	default:
		return h.routeNotebookSupport(w, r, op)
	}

	return true
}

func (h *Handler) routeNotebookSupport(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateNotebookInstanceLifecycleConfig":
		h.createNotebookLC(w, r)
	case "DescribeNotebookInstanceLifecycleConfig":
		h.describeNotebookLC(w, r)
	case "ListNotebookInstanceLifecycleConfigs":
		h.listNotebookLCs(w, r)
	case "DeleteNotebookInstanceLifecycleConfig":
		stopByName(w, r, "NotebookInstanceLifecycleConfigName", h.svc.DeleteNotebookInstanceLifecycleConfig)
	case "CreateCodeRepository":
		h.createCodeRepository(w, r)
	case "DescribeCodeRepository":
		h.describeCodeRepository(w, r)
	case "ListCodeRepositories":
		h.listCodeRepositories(w, r)
	case "DeleteCodeRepository":
		stopByName(w, r, "CodeRepositoryName", h.svc.DeleteCodeRepository)
	default:
		return false
	}

	return true
}

func (h *Handler) createNotebookInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NotebookInstanceName  string    `json:"NotebookInstanceName"`
		InstanceType          string    `json:"InstanceType"`
		RoleArn               string    `json:"RoleArn"`
		VolumeSizeInGB        int       `json:"VolumeSizeInGB"`
		LifecycleConfigName   string    `json:"LifecycleConfigName"`
		DefaultCodeRepository string    `json:"DefaultCodeRepository"`
		Tags                  []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	nb, err := h.svc.CreateNotebookInstance(r.Context(), driver.NotebookInstanceSpec{
		Name:            req.NotebookInstanceName,
		InstanceType:    req.InstanceType,
		RoleARN:         req.RoleArn,
		VolumeSizeInGB:  req.VolumeSizeInGB,
		LifecycleConfig: req.LifecycleConfigName,
		DefaultCodeRepo: req.DefaultCodeRepository,
		Tags:            toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"NotebookInstanceArn": nb.ARN})
}

func (h *Handler) describeNotebookInstance(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "NotebookInstanceName")
	if !ok {
		return
	}

	nb, err := h.svc.DescribeNotebookInstance(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"NotebookInstanceName":   nb.Name,
		"NotebookInstanceArn":    nb.ARN,
		"NotebookInstanceStatus": nb.Status,
		"InstanceType":           nb.InstanceType,
		"RoleArn":                nb.RoleARN,
		"Url":                    nb.URL,
		"CreationTime":           epoch(nb.CreationTime),
	})
}

func (h *Handler) listNotebookInstances(w http.ResponseWriter, r *http.Request) {
	nbs, err := h.svc.ListNotebookInstances(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(nbs))
	for i := range nbs {
		out = append(out, map[string]any{
			"NotebookInstanceName":   nbs[i].Name,
			"NotebookInstanceArn":    nbs[i].ARN,
			"NotebookInstanceStatus": nbs[i].Status,
			"InstanceType":           nbs[i].InstanceType,
			"CreationTime":           epoch(nbs[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"NotebookInstances": out})
}

// wireLCStep is one OnCreate/OnStart shell step.
type wireLCStep struct {
	Content string `json:"Content"`
}

func firstStepContent(steps []wireLCStep) string {
	if len(steps) > 0 {
		return steps[0].Content
	}

	return ""
}

func (h *Handler) createNotebookLC(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NotebookInstanceLifecycleConfigName string       `json:"NotebookInstanceLifecycleConfigName"`
		OnCreate                            []wireLCStep `json:"OnCreate"`
		OnStart                             []wireLCStep `json:"OnStart"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	lc, err := h.svc.CreateNotebookInstanceLifecycleConfig(r.Context(), driver.NotebookLifecycleConfigSpec{
		Name:     req.NotebookInstanceLifecycleConfigName,
		OnCreate: firstStepContent(req.OnCreate),
		OnStart:  firstStepContent(req.OnStart),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"NotebookInstanceLifecycleConfigArn": lc.ARN})
}

func (h *Handler) describeNotebookLC(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "NotebookInstanceLifecycleConfigName")
	if !ok {
		return
	}

	lc, err := h.svc.DescribeNotebookInstanceLifecycleConfig(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"NotebookInstanceLifecycleConfigName": lc.Name,
		"NotebookInstanceLifecycleConfigArn":  lc.ARN,
		"OnCreate":                            []wireLCStep{{Content: lc.OnCreate}},
		"OnStart":                             []wireLCStep{{Content: lc.OnStart}},
		"CreationTime":                        epoch(lc.CreationTime),
	})
}

func (h *Handler) listNotebookLCs(w http.ResponseWriter, r *http.Request) {
	lcs, err := h.svc.ListNotebookInstanceLifecycleConfigs(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(lcs))
	for i := range lcs {
		out = append(out, map[string]any{
			"NotebookInstanceLifecycleConfigName": lcs[i].Name,
			"NotebookInstanceLifecycleConfigArn":  lcs[i].ARN,
			"CreationTime":                        epoch(lcs[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"NotebookInstanceLifecycleConfigs": out})
}

func (h *Handler) createCodeRepository(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CodeRepositoryName string `json:"CodeRepositoryName"`
		GitConfig          struct {
			RepositoryURL string `json:"RepositoryUrl"`
			Branch        string `json:"Branch"`
			SecretArn     string `json:"SecretArn"`
		} `json:"GitConfig"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	repo, err := h.svc.CreateCodeRepository(r.Context(), driver.CodeRepositorySpec{
		Name:      req.CodeRepositoryName,
		GitURL:    req.GitConfig.RepositoryURL,
		Branch:    req.GitConfig.Branch,
		SecretARN: req.GitConfig.SecretArn,
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"CodeRepositoryArn": repo.ARN})
}

func (h *Handler) describeCodeRepository(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "CodeRepositoryName")
	if !ok {
		return
	}

	repo, err := h.svc.DescribeCodeRepository(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"CodeRepositoryName": repo.Name,
		"CodeRepositoryArn":  repo.ARN,
		"GitConfig": map[string]any{
			"RepositoryUrl": repo.GitURL,
			"Branch":        repo.Branch,
			"SecretArn":     repo.SecretARN,
		},
		"CreationTime": epoch(repo.CreationTime),
	})
}

func (h *Handler) listCodeRepositories(w http.ResponseWriter, r *http.Request) {
	repos, err := h.svc.ListCodeRepositories(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(repos))
	for i := range repos {
		out = append(out, map[string]any{
			"CodeRepositoryName": repos[i].Name,
			"CodeRepositoryArn":  repos[i].ARN,
			"CreationTime":       epoch(repos[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"CodeRepositorySummaryList": out})
}
