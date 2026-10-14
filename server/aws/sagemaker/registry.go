package sagemaker

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) createModelPackageGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelPackageGroupName        string    `json:"ModelPackageGroupName"`
		ModelPackageGroupDescription string    `json:"ModelPackageGroupDescription"`
		Tags                         []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	g, err := h.svc.CreateModelPackageGroup(r.Context(), driver.ModelPackageGroupSpec{
		GroupName:   req.ModelPackageGroupName,
		Description: req.ModelPackageGroupDescription,
		Tags:        toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ModelPackageGroupArn": g.GroupARN})
}

func (h *Handler) describeModelPackageGroup(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "ModelPackageGroupName")
	if !ok {
		return
	}

	g, err := h.svc.DescribeModelPackageGroup(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"ModelPackageGroupName":   g.GroupName,
		"ModelPackageGroupArn":    g.GroupARN,
		"ModelPackageGroupStatus": g.Status,
		"CreationTime":            epoch(g.CreationTime),
	})
}

func (h *Handler) listModelPackageGroups(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	groups, err := h.svc.ListModelPackageGroups(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "ModelPackageGroupSummaryList", groups, func(g *driver.ModelPackageGroup) map[string]any {
		return map[string]any{
			"ModelPackageGroupName":   g.GroupName,
			"ModelPackageGroupArn":    g.GroupARN,
			"ModelPackageGroupStatus": g.Status,
			"CreationTime":            epoch(g.CreationTime),
		}
	})
}

func (h *Handler) deleteModelPackageGroup(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "ModelPackageGroupName", h.svc.DeleteModelPackageGroup)
}

func (h *Handler) createModelPackage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelPackageGroupName   string `json:"ModelPackageGroupName"`
		ModelPackageDescription string `json:"ModelPackageDescription"`
		ModelApprovalStatus     string `json:"ModelApprovalStatus"`
		InferenceSpecification  struct {
			Containers []wireContainer `json:"Containers"`
		} `json:"InferenceSpecification"`
		Tags []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	spec := driver.ModelPackageSpec{
		GroupName:      req.ModelPackageGroupName,
		Description:    req.ModelPackageDescription,
		ApprovalStatus: req.ModelApprovalStatus,
		Tags:           toTags(req.Tags),
	}
	if len(req.InferenceSpecification.Containers) > 0 {
		spec.InferenceImage = req.InferenceSpecification.Containers[0].Image
		spec.ModelDataURL = req.InferenceSpecification.Containers[0].ModelDataURL
	}

	p, err := h.svc.CreateModelPackage(r.Context(), spec)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ModelPackageArn": p.PackageARN})
}

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) describeModelPackage(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "ModelPackageName")
	if !ok {
		return
	}

	p, err := h.svc.DescribeModelPackage(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	resp := map[string]any{
		"ModelPackageArn":        p.PackageARN,
		"ModelPackageName":       p.GroupName + "/" + strconv.Itoa(p.Version),
		"ModelPackageGroupName":  p.GroupName,
		"ModelPackageVersion":    p.Version,
		"ModelPackageStatus":     p.Status,
		"ModelApprovalStatus":    p.ApprovalStatus,
		"CreationTime":           epoch(p.CreationTime),
		"InferenceSpecification": packageInferenceSpec(p),
	}
	setIf(resp, "ModelPackageDescription", p.Description)

	wire.WriteJSON(w, resp)
}

func (h *Handler) listModelPackages(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	pkgs, err := h.svc.ListModelPackages(r.Context(), req.ModelPackageGroupName, req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "ModelPackageSummaryList", pkgs, func(p *driver.ModelPackage) map[string]any {
		return map[string]any{
			"ModelPackageArn":       p.PackageARN,
			"ModelPackageGroupName": p.GroupName,
			"ModelPackageVersion":   p.Version,
			"ModelPackageStatus":    p.Status,
			"ModelApprovalStatus":   p.ApprovalStatus,
			"CreationTime":          epoch(p.CreationTime),
		}
	})
}

func (h *Handler) updateModelPackage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelPackageArn     string `json:"ModelPackageArn"`
		ModelApprovalStatus string `json:"ModelApprovalStatus"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	p, err := h.svc.UpdateModelPackage(r.Context(), req.ModelPackageArn, req.ModelApprovalStatus)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ModelPackageArn": p.PackageARN})
}

func (h *Handler) deleteModelPackage(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "ModelPackageName", h.svc.DeleteModelPackage)
}

// packageInferenceSpec renders a model package's inference specification.
func packageInferenceSpec(p *driver.ModelPackage) map[string]any {
	c := map[string]any{"Image": p.InferenceImage}
	setIf(c, "ModelDataUrl", p.ModelDataURL)

	return map[string]any{"Containers": []map[string]any{c}}
}

func (h *Handler) batchDescribeModelPackage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelPackageArnList []string `json:"ModelPackageArnList"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	res, err := h.svc.BatchDescribeModelPackage(r.Context(), req.ModelPackageArnList)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	summaries := make(map[string]any, len(res.Packages))

	for arn := range res.Packages {
		p := res.Packages[arn]
		s := map[string]any{
			"ModelPackageGroupName":  p.GroupName,
			"ModelPackageArn":        p.PackageARN,
			"ModelPackageVersion":    p.Version,
			"ModelPackageStatus":     p.Status,
			"ModelApprovalStatus":    p.ApprovalStatus,
			"CreationTime":           epoch(p.CreationTime),
			"InferenceSpecification": packageInferenceSpec(&p),
		}
		setIf(s, "ModelPackageDescription", p.Description)
		summaries[arn] = s
	}

	errs := make(map[string]any, len(res.Errors))
	for arn, e := range res.Errors {
		errs[arn] = map[string]any{"ErrorCode": e.Code, "ErrorResponse": e.Message}
	}

	wire.WriteJSON(w, map[string]any{"ModelPackageSummaries": summaries, "BatchDescribeModelPackageErrorMap": errs})
}

func (h *Handler) putModelPackageGroupPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelPackageGroupName string `json:"ModelPackageGroupName"`
		ResourcePolicy        string `json:"ResourcePolicy"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	arn, err := h.svc.PutModelPackageGroupPolicy(r.Context(), req.ModelPackageGroupName, req.ResourcePolicy)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ModelPackageGroupArn": arn})
}

func (h *Handler) getModelPackageGroupPolicy(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "ModelPackageGroupName")
	if !ok {
		return
	}

	policy, err := h.svc.GetModelPackageGroupPolicy(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResourcePolicy": policy})
}

func (h *Handler) deleteModelPackageGroupPolicy(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "ModelPackageGroupName", h.svc.DeleteModelPackageGroupPolicy)
}
