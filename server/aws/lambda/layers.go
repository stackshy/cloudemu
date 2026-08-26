package lambda

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// layerContentInput is the deployment package in a PublishLayerVersion body.
// The AWS SDK sends the zip as base64 in ZipFile, which Go unmarshals into
// []byte. S3-sourced content is accepted but not fetched.
type layerContentInput struct {
	ZipFile []byte `json:"ZipFile"`
}

// publishLayerVersionRequest is the body of PublishLayerVersion
// (POST .../layers/{name}/versions).
type publishLayerVersionRequest struct {
	Description        string             `json:"Description"`
	Content            *layerContentInput `json:"Content"`
	CompatibleRuntimes []string           `json:"CompatibleRuntimes"`
	LicenseInfo        string             `json:"LicenseInfo"`
}

// layerContentOutput is the LayerVersionContentOutput shape.
type layerContentOutput struct {
	Location   string `json:"Location,omitempty"`
	CodeSha256 string `json:"CodeSha256,omitempty"`
	CodeSize   int64  `json:"CodeSize,omitempty"`
}

// layerVersionResponse is the shared shape for PublishLayerVersion and
// GetLayerVersion.
type layerVersionResponse struct {
	Content            *layerContentOutput `json:"Content,omitempty"`
	LayerArn           string              `json:"LayerArn,omitempty"`
	LayerVersionArn    string              `json:"LayerVersionArn,omitempty"`
	Description        string              `json:"Description,omitempty"`
	CreatedDate        string              `json:"CreatedDate,omitempty"`
	Version            int64               `json:"Version"`
	CompatibleRuntimes []string            `json:"CompatibleRuntimes,omitempty"`
	LicenseInfo        string              `json:"LicenseInfo,omitempty"`
}

// layerVersionListItem is the LayerVersionsListItem shape (no Content) used by
// ListLayerVersions and as the LatestMatchingVersion of ListLayers.
type layerVersionListItem struct {
	LayerVersionArn    string   `json:"LayerVersionArn,omitempty"`
	Version            int64    `json:"Version"`
	Description        string   `json:"Description,omitempty"`
	CreatedDate        string   `json:"CreatedDate,omitempty"`
	CompatibleRuntimes []string `json:"CompatibleRuntimes,omitempty"`
	LicenseInfo        string   `json:"LicenseInfo,omitempty"`
}

// layersListItem is the LayersListItem shape returned by ListLayers.
type layersListItem struct {
	LayerName             string               `json:"LayerName,omitempty"`
	LayerArn              string               `json:"LayerArn,omitempty"`
	LatestMatchingVersion layerVersionListItem `json:"LatestMatchingVersion"`
}

// layerARN strips the trailing :{version} to yield the version-less layer ARN.
func layerARN(lv *sdrv.LayerVersion) string {
	return strings.TrimSuffix(lv.ARN, ":"+strconv.Itoa(lv.Version))
}

func toLayerVersionResponse(lv *sdrv.LayerVersion) layerVersionResponse {
	return layerVersionResponse{
		Content: &layerContentOutput{
			Location:   "https://cloudemu-mock/layers/" + lv.Name + "/" + strconv.Itoa(lv.Version),
			CodeSha256: lv.ContentSHA256,
			CodeSize:   lv.ContentSize,
		},
		LayerArn:           layerARN(lv),
		LayerVersionArn:    lv.ARN,
		Description:        lv.Description,
		CreatedDate:        lv.CreatedAt,
		Version:            int64(lv.Version),
		CompatibleRuntimes: lv.CompatibleRuntimes,
	}
}

func toLayerVersionListItem(lv *sdrv.LayerVersion) layerVersionListItem {
	return layerVersionListItem{
		LayerVersionArn:    lv.ARN,
		Version:            int64(lv.Version),
		Description:        lv.Description,
		CreatedDate:        lv.CreatedAt,
		CompatibleRuntimes: lv.CompatibleRuntimes,
	}
}

// serveLayers dispatches the /2018-10-31/layers paths: the layers collection
// (GET=ListLayers), the per-layer versions collection (POST=PublishLayerVersion,
// GET=ListLayerVersions), and a specific version (GET/DELETE).
func (h *Handler) serveLayers(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, layersPrefix), "/")

	if rest == "" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
			return
		}

		h.listLayers(w, r)

		return
	}

	parts := strings.Split(rest, "/")

	const (
		partsVersions = 2 // /layers/{name}/versions
		partsVersion  = 3 // /layers/{name}/versions/{version}
	)

	switch {
	case len(parts) == partsVersions && parts[1] == subVersions:
		h.serveLayerVersions(w, r, parts[0])
	case len(parts) == partsVersion && parts[1] == subVersions:
		h.serveLayerVersion(w, r, parts[0], parts[2])
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda layers path")
	}
}

// serveLayerVersions handles POST (PublishLayerVersion) and GET
// (ListLayerVersions) on .../layers/{name}/versions.
func (h *Handler) serveLayerVersions(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPost:
		var req publishLayerVersionRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		cfg := sdrv.LayerConfig{
			Name:               name,
			Description:        req.Description,
			CompatibleRuntimes: req.CompatibleRuntimes,
		}
		if req.Content != nil {
			cfg.Content = req.Content.ZipFile
		}

		lv, err := h.fn.PublishLayerVersion(r.Context(), cfg)
		if err != nil {
			writeErr(w, err)
			return
		}

		// Stage the zip bytes so a function importing this layer can have its
		// files overlaid into the deployment package at CreateFunction.
		if req.Content != nil {
			h.putLayerContent(lv.Name, lv.Version, req.Content.ZipFile)
		}

		resp := toLayerVersionResponse(lv)
		resp.LicenseInfo = req.LicenseInfo
		writeJSON(w, http.StatusCreated, resp)
	case http.MethodGet:
		vers, err := h.fn.ListLayerVersions(r.Context(), name)
		if err != nil {
			writeErr(w, err)
			return
		}

		items := make([]layerVersionListItem, 0, len(vers))
		for i := range vers {
			items = append(items, toLayerVersionListItem(&vers[i]))
		}

		writeJSON(w, http.StatusOK, map[string]any{"LayerVersions": items})
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// serveLayerVersion handles GET (GetLayerVersion) and DELETE
// (DeleteLayerVersion) on .../layers/{name}/versions/{version}.
func (h *Handler) serveLayerVersion(w http.ResponseWriter, r *http.Request, name, versionStr string) {
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidParameterValueException", "invalid layer version number")
		return
	}

	switch r.Method {
	case http.MethodGet:
		lv, gerr := h.fn.GetLayerVersion(r.Context(), name, version)
		if gerr != nil {
			writeErr(w, gerr)
			return
		}

		writeJSON(w, http.StatusOK, toLayerVersionResponse(lv))
	case http.MethodDelete:
		if derr := h.fn.DeleteLayerVersion(r.Context(), name, version); derr != nil {
			writeErr(w, derr)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// listLayers handles GET /2018-10-31/layers (ListLayers), returning the latest
// version of each layer.
func (h *Handler) listLayers(w http.ResponseWriter, r *http.Request) {
	layers, err := h.fn.ListLayers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	// Sort by name so Marker offsets stay stable across paginated calls.
	sort.Slice(layers, func(i, j int) bool { return layers[i].Name < layers[j].Name })

	start, end, nextMarker, truncated := pageWindow(len(layers), r.URL.Query())

	items := make([]layersListItem, 0, end-start)
	for i := start; i < end; i++ {
		items = append(items, layersListItem{
			LayerName:             layers[i].Name,
			LayerArn:              layerARN(&layers[i]),
			LatestMatchingVersion: toLayerVersionListItem(&layers[i]),
		})
	}

	body := map[string]any{"Layers": items}
	if truncated {
		body["NextMarker"] = nextMarker
	}

	writeJSON(w, http.StatusOK, body)
}
