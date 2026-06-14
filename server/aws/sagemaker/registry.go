package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/sagemaker/driver"
	"github.com/stackshy/cloudemu/server/wire"
)

func (h *Handler) routeRegistry(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateModelPackageGroup":
		h.createModelPackageGroup(w, r)
	case "DescribeModelPackageGroup":
		h.describeModelPackageGroup(w, r)
	case "ListModelPackageGroups":
		h.listModelPackageGroups(w, r)
	case "DeleteModelPackageGroup":
		h.deleteModelPackageGroup(w, r)
	case "CreateModelPackage":
		h.createModelPackage(w, r)
	case "DescribeModelPackage":
		h.describeModelPackage(w, r)
	case "ListModelPackages":
		h.listModelPackages(w, r)
	case "UpdateModelPackage":
		h.updateModelPackage(w, r)
	case "DeleteModelPackage":
		h.deleteModelPackage(w, r)
	default:
		return false
	}

	return true
}

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
	groups, err := h.svc.ListModelPackageGroups(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(groups))
	for i := range groups {
		out = append(out, map[string]any{
			"ModelPackageGroupName":   groups[i].GroupName,
			"ModelPackageGroupArn":    groups[i].GroupARN,
			"ModelPackageGroupStatus": groups[i].Status,
			"CreationTime":            epoch(groups[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"ModelPackageGroupSummaryList": out})
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

	wire.WriteJSON(w, map[string]any{
		"ModelPackageArn":       p.PackageARN,
		"ModelPackageGroupName": p.GroupName,
		"ModelPackageVersion":   p.Version,
		"ModelPackageStatus":    p.Status,
		"ModelApprovalStatus":   p.ApprovalStatus,
		"CreationTime":          epoch(p.CreationTime),
	})
}

func (h *Handler) listModelPackages(w http.ResponseWriter, r *http.Request) {
	groupName, ok := decodeName1(w, r, "ModelPackageGroupName")
	if !ok {
		return
	}

	pkgs, err := h.svc.ListModelPackages(r.Context(), groupName)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(pkgs))
	for i := range pkgs {
		out = append(out, map[string]any{
			"ModelPackageArn":       pkgs[i].PackageARN,
			"ModelPackageGroupName": pkgs[i].GroupName,
			"ModelPackageVersion":   pkgs[i].Version,
			"ModelPackageStatus":    pkgs[i].Status,
			"ModelApprovalStatus":   pkgs[i].ApprovalStatus,
			"CreationTime":          epoch(pkgs[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"ModelPackageSummaryList": out})
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
