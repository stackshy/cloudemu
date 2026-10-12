package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

func (h *Handler) createNotebookInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NotebookInstanceName  string   `json:"NotebookInstanceName"`
		InstanceType          string   `json:"InstanceType"`
		RoleArn               string   `json:"RoleArn"`
		VolumeSizeInGB        int      `json:"VolumeSizeInGB"`
		LifecycleConfigName   string   `json:"LifecycleConfigName"`
		DefaultCodeRepository string   `json:"DefaultCodeRepository"`
		AdditionalCodeRepos   []string `json:"AdditionalCodeRepositories"`
		RootAccess            string   `json:"RootAccess"`
		SubnetID              string   `json:"SubnetId"`
		SecurityGroupIDs      []string `json:"SecurityGroupIds"`
		KmsKeyID              string   `json:"KmsKeyId"`
		DirectInternetAccess  string   `json:"DirectInternetAccess"`
		IPAddressType         string   `json:"IpAddressType"`
		IMDS                  *struct {
			Version string `json:"MinimumInstanceMetadataServiceVersion"`
		} `json:"InstanceMetadataServiceConfiguration"`
		PlatformIdentifier string    `json:"PlatformIdentifier"`
		Tags               []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	nb, err := h.svc.CreateNotebookInstance(r.Context(), driver.NotebookInstanceSpec{
		Name:                 req.NotebookInstanceName,
		InstanceType:         req.InstanceType,
		RoleARN:              req.RoleArn,
		VolumeSizeInGB:       req.VolumeSizeInGB,
		LifecycleConfig:      req.LifecycleConfigName,
		DefaultCodeRepo:      req.DefaultCodeRepository,
		AdditionalCodeRepos:  req.AdditionalCodeRepos,
		RootAccess:           req.RootAccess,
		SubnetID:             req.SubnetID,
		SecurityGroups:       req.SecurityGroupIDs,
		KmsKeyID:             req.KmsKeyID,
		DirectInternetAccess: req.DirectInternetAccess,
		IMDSVersion:          imdsVersion(req.IMDS),
		IPAddressType:        req.IPAddressType,
		PlatformIdentifier:   req.PlatformIdentifier,
		Tags:                 toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"NotebookInstanceArn": nb.ARN})
}

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
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

	resp := map[string]any{
		"NotebookInstanceName":   nb.Name,
		"NotebookInstanceArn":    nb.ARN,
		"NotebookInstanceStatus": nb.Status,
		"InstanceType":           nb.InstanceType,
		"RoleArn":                nb.RoleARN,
		"Url":                    nb.URL,
		"VolumeSizeInGB":         nb.VolumeSizeInGB,
		"RootAccess":             nb.RootAccess,
		"DirectInternetAccess":   nb.DirectInternetAccess,
		"IpAddressType":          nb.IPAddressType,
		"InstanceMetadataServiceConfiguration": map[string]any{
			"MinimumInstanceMetadataServiceVersion": nb.IMDSVersion,
		},
		"PlatformIdentifier": nb.PlatformIdentifier,
		"CreationTime":       epoch(nb.CreationTime),
		"LastModifiedTime":   epoch(nb.LastModifiedTime),
	}

	// Optional members are omitted when empty, not sent as "" or null.
	setIf(resp, "NotebookInstanceLifecycleConfigName", nb.LifecycleConfig)
	setIf(resp, "DefaultCodeRepository", nb.DefaultCodeRepo)
	setIf(resp, "FailureReason", nb.FailureReason)
	setIf(resp, "SubnetId", nb.SubnetID)
	setIf(resp, "KmsKeyId", nb.KmsKeyID)

	if len(nb.SecurityGroups) > 0 {
		resp["SecurityGroups"] = nb.SecurityGroups
	}

	if len(nb.AdditionalCodeRepos) > 0 {
		resp["AdditionalCodeRepositories"] = nb.AdditionalCodeRepos
	}

	wire.WriteJSON(w, resp)
}

// setIf sets m[key] = v when v is non-empty.
func setIf(m map[string]any, key, v string) {
	if v != "" {
		m[key] = v
	}
}

func (h *Handler) updateNotebookInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NotebookInstanceName                   string   `json:"NotebookInstanceName"`
		InstanceType                           *string  `json:"InstanceType"`
		RoleArn                                *string  `json:"RoleArn"`
		LifecycleConfigName                    *string  `json:"LifecycleConfigName"`
		DisassociateLifecycleConfig            bool     `json:"DisassociateLifecycleConfig"`
		VolumeSizeInGB                         *int     `json:"VolumeSizeInGB"`
		DefaultCodeRepository                  *string  `json:"DefaultCodeRepository"`
		DisassociateDefaultCodeRepository      bool     `json:"DisassociateDefaultCodeRepository"`
		AdditionalCodeRepositories             []string `json:"AdditionalCodeRepositories"`
		DisassociateAdditionalCodeRepositories bool     `json:"DisassociateAdditionalCodeRepositories"`
		RootAccess                             *string  `json:"RootAccess"`
		IPAddressType                          *string  `json:"IpAddressType"`
		IMDS                                   *struct {
			Version string `json:"MinimumInstanceMetadataServiceVersion"`
		} `json:"InstanceMetadataServiceConfiguration"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	err := h.svc.UpdateNotebookInstance(r.Context(), driver.NotebookInstanceUpdate{
		Name:                            req.NotebookInstanceName,
		InstanceType:                    req.InstanceType,
		RoleARN:                         req.RoleArn,
		VolumeSizeInGB:                  req.VolumeSizeInGB,
		LifecycleConfig:                 req.LifecycleConfigName,
		DisassociateLifecycleConfig:     req.DisassociateLifecycleConfig,
		DefaultCodeRepo:                 req.DefaultCodeRepository,
		DisassociateDefaultCodeRepo:     req.DisassociateDefaultCodeRepository,
		AdditionalCodeRepos:             req.AdditionalCodeRepositories,
		DisassociateAdditionalCodeRepos: req.DisassociateAdditionalCodeRepositories,
		RootAccess:                      req.RootAccess,
		IPAddressType:                   req.IPAddressType,
		IMDSVersion:                     imdsVersionPtr(req.IMDS),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) updateNotebookLC(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NotebookInstanceLifecycleConfigName string        `json:"NotebookInstanceLifecycleConfigName"`
		OnCreate                            *[]wireLCStep `json:"OnCreate"`
		OnStart                             *[]wireLCStep `json:"OnStart"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	up := driver.NotebookLifecycleConfigUpdate{Name: req.NotebookInstanceLifecycleConfigName}

	if req.OnCreate != nil {
		c := firstStepContent(*req.OnCreate)
		up.OnCreate = &c
	}

	if req.OnStart != nil {
		c := firstStepContent(*req.OnStart)
		up.OnStart = &c
	}

	if err := h.svc.UpdateNotebookInstanceLifecycleConfig(r.Context(), up); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) listNotebookInstances(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	nbs, err := h.svc.ListNotebookInstances(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "NotebookInstances", nbs, func(nb *driver.NotebookInstance) map[string]any {
		return map[string]any{
			"NotebookInstanceName":   nb.Name,
			"NotebookInstanceArn":    nb.ARN,
			"NotebookInstanceStatus": nb.Status,
			"InstanceType":           nb.InstanceType,
			"CreationTime":           epoch(nb.CreationTime),
			"LastModifiedTime":       epoch(nb.LastModifiedTime),
		}
	})
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

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
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
		"LastModifiedTime":                    epoch(lc.LastModifiedTime),
	})
}

func (h *Handler) listNotebookLCs(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	lcs, err := h.svc.ListNotebookInstanceLifecycleConfigs(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "NotebookInstanceLifecycleConfigs", lcs, func(lc *driver.NotebookLifecycleConfig) map[string]any {
		return map[string]any{
			"NotebookInstanceLifecycleConfigName": lc.Name,
			"NotebookInstanceLifecycleConfigArn":  lc.ARN,
			"CreationTime":                        epoch(lc.CreationTime),
			"LastModifiedTime":                    epoch(lc.LastModifiedTime),
		}
	})
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
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	repos, err := h.svc.ListCodeRepositories(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "CodeRepositorySummaryList", repos, func(repo *driver.CodeRepository) map[string]any {
		return map[string]any{
			"CodeRepositoryName": repo.Name,
			"CodeRepositoryArn":  repo.ARN,
			"CreationTime":       epoch(repo.CreationTime),
		}
	})
}

func imdsVersion(c *struct {
	Version string `json:"MinimumInstanceMetadataServiceVersion"`
}) string {
	if c == nil {
		return ""
	}

	return c.Version
}

func imdsVersionPtr(c *struct {
	Version string `json:"MinimumInstanceMetadataServiceVersion"`
}) *string {
	if c == nil {
		return nil
	}

	return &c.Version
}
