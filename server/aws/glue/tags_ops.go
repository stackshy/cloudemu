package glue

import (
	"context"
	"net/http"
)

type tagResourceRequest struct {
	ResourceArn string            `json:"ResourceArn"`
	TagsToAdd   map[string]string `json:"TagsToAdd"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *tagResourceRequest) (any, error) {
		if err := h.glue.TagResource(ctx, req.ResourceArn, req.TagsToAdd); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type untagResourceRequest struct {
	ResourceArn  string   `json:"ResourceArn"`
	TagsToRemove []string `json:"TagsToRemove"`
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *untagResourceRequest) (any, error) {
		if err := h.glue.UntagResource(ctx, req.ResourceArn, req.TagsToRemove); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getTagsRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

type getTagsResponse struct {
	Tags map[string]string `json:"Tags"`
}

func (h *Handler) getTags(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getTagsRequest) (any, error) {
		tags, err := h.glue.GetTags(ctx, req.ResourceArn)
		if err != nil {
			return nil, err
		}

		return getTagsResponse{Tags: tags}, nil
	})
}

type putResourcePolicyRequest struct {
	PolicyInJSON string `json:"PolicyInJson"`
	ResourceArn  string `json:"ResourceArn"`
}

type putResourcePolicyResponse struct {
	PolicyHash string `json:"PolicyHash"`
}

func (h *Handler) putResourcePolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putResourcePolicyRequest) (any, error) {
		hash, err := h.glue.PutResourcePolicy(ctx, req.ResourceArn, req.PolicyInJSON)
		if err != nil {
			return nil, err
		}

		return putResourcePolicyResponse{PolicyHash: hash}, nil
	})
}

type getResourcePolicyRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

type getResourcePolicyResponse struct {
	PolicyInJSON string `json:"PolicyInJson"`
}

func (h *Handler) getResourcePolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getResourcePolicyRequest) (any, error) {
		policy, err := h.glue.GetResourcePolicy(ctx, req.ResourceArn)
		if err != nil {
			return nil, err
		}

		return getResourcePolicyResponse{PolicyInJSON: policy}, nil
	})
}

type deleteResourcePolicyRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

func (h *Handler) deleteResourcePolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteResourcePolicyRequest) (any, error) {
		if err := h.glue.DeleteResourcePolicy(ctx, req.ResourceArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type putEncryptionSettingsRequest struct {
	CatalogID                     string         `json:"CatalogId"`
	DataCatalogEncryptionSettings map[string]any `json:"DataCatalogEncryptionSettings"`
}

func (h *Handler) putEncryptionSettings(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putEncryptionSettingsRequest) (any, error) {
		if err := h.glue.PutDataCatalogEncryptionSettings(ctx, req.CatalogID,
			req.DataCatalogEncryptionSettings); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getEncryptionSettingsRequest struct {
	CatalogID string `json:"CatalogId"`
}

type getEncryptionSettingsResponse struct {
	DataCatalogEncryptionSettings map[string]any `json:"DataCatalogEncryptionSettings"`
}

func (h *Handler) getEncryptionSettings(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getEncryptionSettingsRequest) (any, error) {
		settings, err := h.glue.GetDataCatalogEncryptionSettings(ctx, req.CatalogID)
		if err != nil {
			return nil, err
		}

		return getEncryptionSettingsResponse{DataCatalogEncryptionSettings: settings}, nil
	})
}
