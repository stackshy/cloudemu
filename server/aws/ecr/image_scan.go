package ecr

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// imageScanRequest is the shared request shape for StartImageScan and
// DescribeImageScanFindings.
type imageScanRequest struct {
	RepositoryName string          `json:"repositoryName"`
	ImageID        imageIdentifier `json:"imageId"`
}

type imageIdentifier struct {
	ImageTag    string `json:"imageTag,omitempty"`
	ImageDigest string `json:"imageDigest,omitempty"`
}

// reference resolves the image identifier to the tag or digest the driver keys
// on (tag preferred, as in the ECR SDK).
func (id imageIdentifier) reference() string {
	if id.ImageTag != "" {
		return id.ImageTag
	}

	return id.ImageDigest
}

func (id imageIdentifier) response() map[string]any {
	m := map[string]any{}
	if id.ImageTag != "" {
		m["imageTag"] = id.ImageTag
	}

	if id.ImageDigest != "" {
		m["imageDigest"] = id.ImageDigest
	}

	return m
}

func (h *Handler) startImageScan(w http.ResponseWriter, r *http.Request) {
	var req imageScanRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	res, err := h.registry.StartImageScan(r.Context(), req.RepositoryName, req.ImageID.reference())
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"repositoryName":  req.RepositoryName,
		"imageId":         req.ImageID.response(),
		"imageScanStatus": map[string]any{"status": res.Status},
	})
}

func (h *Handler) describeImageScanFindings(w http.ResponseWriter, r *http.Request) {
	var req imageScanRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	res, err := h.registry.GetImageScanResults(r.Context(), req.RepositoryName, req.ImageID.reference())
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"repositoryName":  req.RepositoryName,
		"imageId":         req.ImageID.response(),
		"imageScanStatus": map[string]any{"status": res.Status},
		"imageScanFindings": map[string]any{
			"findingSeverityCounts": res.FindingCounts,
		},
	})
}
