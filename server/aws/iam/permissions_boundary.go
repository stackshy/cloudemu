package iam

import (
	"context"
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// permissionsBoundaryType is the only value AWS reports for a boundary's type.
const permissionsBoundaryType = "PermissionsBoundaryPolicy"

// permissionsBoundaryManager is the AWS-specific permissions-boundary surface.
// It's not part of the portable IAM driver, so the handler type-asserts for it.
type permissionsBoundaryManager interface {
	PutRolePermissionsBoundary(ctx context.Context, roleName, boundaryARN string) error
	DeleteRolePermissionsBoundary(ctx context.Context, roleName string) error
	RolePermissionsBoundary(ctx context.Context, roleName string) (string, error)
	PutUserPermissionsBoundary(ctx context.Context, userName, boundaryARN string) error
	DeleteUserPermissionsBoundary(ctx context.Context, userName string) error
	UserPermissionsBoundary(ctx context.Context, userName string) (string, error)
}

type permissionsBoundaryXML struct {
	PermissionsBoundaryType string `xml:"PermissionsBoundaryType"`
	PermissionsBoundaryArn  string `xml:"PermissionsBoundaryArn"`
}

type putRolePermissionsBoundaryResponse struct {
	XMLName  xml.Name         `xml:"PutRolePermissionsBoundaryResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteRolePermissionsBoundaryResponse struct {
	XMLName  xml.Name         `xml:"DeleteRolePermissionsBoundaryResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type putUserPermissionsBoundaryResponse struct {
	XMLName  xml.Name         `xml:"PutUserPermissionsBoundaryResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteUserPermissionsBoundaryResponse struct {
	XMLName  xml.Name         `xml:"DeleteUserPermissionsBoundaryResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// boundaryManager returns the driver's permissions-boundary surface, or nil
// when the backend does not support boundaries.
func (h *Handler) boundaryManager() permissionsBoundaryManager {
	mgr, ok := h.iam.(permissionsBoundaryManager)
	if !ok {
		return nil
	}

	return mgr
}

// roleBoundaryXML resolves a role's permissions boundary to its wire shape, or
// nil when none is set (or the backend does not track boundaries).
func (h *Handler) roleBoundaryXML(ctx context.Context, roleName string) *permissionsBoundaryXML {
	mgr := h.boundaryManager()
	if mgr == nil {
		return nil
	}

	arn, err := mgr.RolePermissionsBoundary(ctx, roleName)
	if err != nil || arn == "" {
		return nil
	}

	return &permissionsBoundaryXML{PermissionsBoundaryType: permissionsBoundaryType, PermissionsBoundaryArn: arn}
}

// userBoundaryXML resolves a user's permissions boundary to its wire shape.
func (h *Handler) userBoundaryXML(ctx context.Context, userName string) *permissionsBoundaryXML {
	mgr := h.boundaryManager()
	if mgr == nil {
		return nil
	}

	arn, err := mgr.UserPermissionsBoundary(ctx, userName)
	if err != nil || arn == "" {
		return nil
	}

	return &permissionsBoundaryXML{PermissionsBoundaryType: permissionsBoundaryType, PermissionsBoundaryArn: arn}
}

func (h *Handler) putRolePermissionsBoundary(w http.ResponseWriter, r *http.Request) {
	mgr := h.boundaryManager()
	if mgr == nil {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "permissions boundaries not supported")
		return
	}

	if err := mgr.PutRolePermissionsBoundary(r.Context(),
		r.Form.Get("RoleName"), r.Form.Get("PermissionsBoundary")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, putRolePermissionsBoundaryResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteRolePermissionsBoundary(w http.ResponseWriter, r *http.Request) {
	mgr := h.boundaryManager()
	if mgr == nil {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "permissions boundaries not supported")
		return
	}

	if err := mgr.DeleteRolePermissionsBoundary(r.Context(), r.Form.Get("RoleName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteRolePermissionsBoundaryResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) putUserPermissionsBoundary(w http.ResponseWriter, r *http.Request) {
	mgr := h.boundaryManager()
	if mgr == nil {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "permissions boundaries not supported")
		return
	}

	if err := mgr.PutUserPermissionsBoundary(r.Context(),
		r.Form.Get("UserName"), r.Form.Get("PermissionsBoundary")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, putUserPermissionsBoundaryResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteUserPermissionsBoundary(w http.ResponseWriter, r *http.Request) {
	mgr := h.boundaryManager()
	if mgr == nil {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "permissions boundaries not supported")
		return
	}

	if err := mgr.DeleteUserPermissionsBoundary(r.Context(), r.Form.Get("UserName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteUserPermissionsBoundaryResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
