package lambda

import (
	"net/http"
	"net/url"
	"slices"
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
	Description             string             `json:"Description"`
	Content                 *layerContentInput `json:"Content"`
	CompatibleRuntimes      []string           `json:"CompatibleRuntimes"`
	CompatibleArchitectures []string           `json:"CompatibleArchitectures"`
	LicenseInfo             string             `json:"LicenseInfo"`
}

// layerContentOutput is the LayerVersionContentOutput shape.
type layerContentOutput struct {
	Location   string `json:"Location,omitempty"`
	CodeSha256 string `json:"CodeSha256,omitempty"`
	CodeSize   int64  `json:"CodeSize,omitempty"`
}

// layerVersionResponse is the shared shape for PublishLayerVersion,
// GetLayerVersion and GetLayerVersionByArn.
type layerVersionResponse struct {
	Content                 *layerContentOutput `json:"Content,omitempty"`
	LayerArn                string              `json:"LayerArn,omitempty"`
	LayerVersionArn         string              `json:"LayerVersionArn,omitempty"`
	Description             string              `json:"Description,omitempty"`
	CreatedDate             string              `json:"CreatedDate,omitempty"`
	Version                 int64               `json:"Version"`
	CompatibleRuntimes      []string            `json:"CompatibleRuntimes,omitempty"`
	CompatibleArchitectures []string            `json:"CompatibleArchitectures,omitempty"`
	LicenseInfo             string              `json:"LicenseInfo,omitempty"`
}

// layerVersionListItem is the LayerVersionsListItem shape (no Content, no
// LicenseInfo) used by ListLayerVersions and as the LatestMatchingVersion of
// ListLayers.
type layerVersionListItem struct {
	LayerVersionArn         string   `json:"LayerVersionArn,omitempty"`
	Version                 int64    `json:"Version"`
	Description             string   `json:"Description,omitempty"`
	CreatedDate             string   `json:"CreatedDate,omitempty"`
	CompatibleRuntimes      []string `json:"CompatibleRuntimes,omitempty"`
	CompatibleArchitectures []string `json:"CompatibleArchitectures,omitempty"`
}

// layersListItem is the LayersListItem shape returned by ListLayers.
type layersListItem struct {
	LayerName             string               `json:"LayerName,omitempty"`
	LayerArn              string               `json:"LayerArn,omitempty"`
	LatestMatchingVersion layerVersionListItem `json:"LatestMatchingVersion"`
}

// addLayerVersionPermissionRequest is the body of AddLayerVersionPermission
// (POST .../layers/{name}/versions/{version}/policy).
type addLayerVersionPermissionRequest struct {
	StatementID    string `json:"StatementId"`
	Action         string `json:"Action"`
	Principal      string `json:"Principal"`
	OrganizationID string `json:"OrganizationId"`
	RevisionID     string `json:"RevisionId"`
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
		LayerArn:                layerARN(lv),
		LayerVersionArn:         lv.ARN,
		Description:             lv.Description,
		CreatedDate:             lv.CreatedAt,
		Version:                 int64(lv.Version),
		CompatibleRuntimes:      lv.CompatibleRuntimes,
		CompatibleArchitectures: lv.CompatibleArchitectures,
		LicenseInfo:             lv.LicenseInfo,
	}
}

func toLayerVersionListItem(lv *sdrv.LayerVersion) layerVersionListItem {
	return layerVersionListItem{
		LayerVersionArn:         lv.ARN,
		Version:                 int64(lv.Version),
		Description:             lv.Description,
		CreatedDate:             lv.CreatedAt,
		CompatibleRuntimes:      lv.CompatibleRuntimes,
		CompatibleArchitectures: lv.CompatibleArchitectures,
	}
}

// findLayerVersion is the ListLayers/GetLayerVersionByArn "find" query value
// that selects the by-Arn lookup on the layers collection endpoint.
const findLayerVersion = "LayerVersion"

// serveLayers dispatches the /2018-10-31/layers paths: the layers collection
// (GET=ListLayers, or GET?find=LayerVersion&Arn=...=GetLayerVersionByArn), the
// per-layer versions collection (POST=PublishLayerVersion,
// GET=ListLayerVersions), a specific version (GET/DELETE), and a version's
// resource policy (POST/GET=.../policy, DELETE=.../policy/{statementId}).
func (h *Handler) serveLayers(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, layersPrefix), "/")

	if rest == "" {
		h.serveLayersCollection(w, r)
		return
	}

	h.serveLayerSubpath(w, r, strings.Split(rest, "/"))
}

// serveLayersCollection handles the bare /2018-10-31/layers endpoint:
// GET=ListLayers, or GET?find=LayerVersion&Arn=...=GetLayerVersionByArn.
func (h *Handler) serveLayersCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
		return
	}

	if r.URL.Query().Get("find") == findLayerVersion {
		h.getLayerVersionByArn(w, r)
		return
	}

	h.listLayers(w, r)
}

// Segment counts for a /2018-10-31/layers/{name}/... path with the layers
// prefix stripped and split on "/".
const (
	partsVersions   = 2 // /layers/{name}/versions
	partsVersion    = 3 // /layers/{name}/versions/{version}
	partsPolicy     = 4 // /layers/{name}/versions/{version}/policy
	partsPolicyStmt = 5 // /layers/{name}/versions/{version}/policy/{statementId}
)

// serveLayerSubpath routes a /2018-10-31/layers/{name}/... path to the
// per-layer versions collection, a specific version, or (via
// serveLayerVersionPolicyPath) a version's resource policy.
func (h *Handler) serveLayerSubpath(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < partsVersions || parts[1] != subVersions {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda layers path")
		return
	}

	name := parts[0]

	switch len(parts) {
	case partsVersions:
		h.serveLayerVersions(w, r, name)
	case partsVersion:
		h.serveLayerVersion(w, r, name, parts[2])
	default:
		h.serveLayerVersionPolicyPath(w, r, name, parts)
	}
}

// serveLayerVersionPolicyPath routes the .../policy and
// .../policy/{statementId} tails of a layer-version path.
func (h *Handler) serveLayerVersionPolicyPath(w http.ResponseWriter, r *http.Request, name string, parts []string) {
	if parts[3] != subPolicy {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda layers path")
		return
	}

	switch len(parts) {
	case partsPolicy:
		h.serveLayerVersionPolicy(w, r, name, parts[2])
	case partsPolicyStmt:
		h.serveRemoveLayerVersionPermission(w, r, name, parts[2], parts[4])
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda layers path")
	}
}

// serveLayerVersions handles POST (PublishLayerVersion) and GET
// (ListLayerVersions) on .../layers/{name}/versions.
func (h *Handler) serveLayerVersions(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPost:
		h.publishLayerVersion(w, r, name)
	case http.MethodGet:
		h.listLayerVersions(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// publishLayerVersion handles POST .../layers/{name}/versions.
func (h *Handler) publishLayerVersion(w http.ResponseWriter, r *http.Request, name string) {
	var req publishLayerVersionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	cfg := sdrv.LayerConfig{
		Name:                    name,
		Description:             req.Description,
		CompatibleRuntimes:      req.CompatibleRuntimes,
		CompatibleArchitectures: req.CompatibleArchitectures,
		LicenseInfo:             req.LicenseInfo,
	}
	if req.Content != nil {
		cfg.Content = req.Content.ZipFile
	}

	lv, err := h.fn.PublishLayerVersion(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Stage the zip bytes so a function importing this layer can have its files
	// overlaid into the deployment package at CreateFunction.
	if req.Content != nil {
		h.putLayerContent(lv.Name, lv.Version, req.Content.ZipFile)
	}

	writeJSON(w, http.StatusCreated, toLayerVersionResponse(lv))
}

// listLayerVersions handles GET .../layers/{name}/versions (ListLayerVersions),
// applying the CompatibleRuntime/CompatibleArchitecture filters and
// Marker/MaxItems pagination real Lambda supports. Results are already
// descending-by-version (matching AWS's latest-first order), which keeps Marker
// offsets stable across paginated calls.
func (h *Handler) listLayerVersions(w http.ResponseWriter, r *http.Request, name string) {
	vers, err := h.fn.ListLayerVersions(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	vers = filterLayerVersions(vers, r.URL.Query())

	start, end, nextMarker, truncated := pageWindow(len(vers), r.URL.Query())

	items := make([]layerVersionListItem, 0, end-start)
	for i := start; i < end; i++ {
		items = append(items, toLayerVersionListItem(&vers[i]))
	}

	body := map[string]any{"LayerVersions": items}
	if truncated {
		body["NextMarker"] = nextMarker
	}

	writeJSON(w, http.StatusOK, body)
}

// filterLayerVersions keeps only the versions matching the CompatibleRuntime
// and/or CompatibleArchitecture query parameters, when present.
func filterLayerVersions(vers []sdrv.LayerVersion, form url.Values) []sdrv.LayerVersion {
	runtime := form.Get("CompatibleRuntime")
	arch := form.Get("CompatibleArchitecture")

	if runtime == "" && arch == "" {
		return vers
	}

	out := vers[:0]

	for i := range vers {
		if runtime != "" && !slices.Contains(vers[i].CompatibleRuntimes, runtime) {
			continue
		}

		if arch != "" && !slices.Contains(vers[i].CompatibleArchitectures, arch) {
			continue
		}

		out = append(out, vers[i])
	}

	return out
}

// serveLayerVersion handles GET (GetLayerVersion) and DELETE
// (DeleteLayerVersion) on .../layers/{name}/versions/{version}.
func (h *Handler) serveLayerVersion(w http.ResponseWriter, r *http.Request, name, versionStr string) {
	version, ok := parseLayerVersionNumber(w, versionStr)
	if !ok {
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

// parseLayerVersionNumber parses a layer version path segment, writing an
// InvalidParameterValueException and returning ok=false on a malformed value.
func parseLayerVersionNumber(w http.ResponseWriter, versionStr string) (version int, ok bool) {
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidParameterValueException", "invalid layer version number")
		return 0, false
	}

	return version, true
}

// getLayerVersionByArn handles GET /2018-10-31/layers?find=LayerVersion&Arn=...
// (GetLayerVersionByArn), the SDK's alternate lookup that resolves a full layer
// version ARN without requiring the caller to already know the layer name and
// version number separately.
func (h *Handler) getLayerVersionByArn(w http.ResponseWriter, r *http.Request) {
	arn := r.URL.Query().Get("Arn")

	name, version, ok := parseLayerARN(arn)
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidParameterValueException", "invalid layer version ARN")
		return
	}

	lv, err := h.fn.GetLayerVersion(r.Context(), name, version)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toLayerVersionResponse(lv))
}

// listLayers handles GET /2018-10-31/layers (ListLayers), returning the latest
// matching version of each layer, filtered by CompatibleRuntime/
// CompatibleArchitecture when given.
func (h *Handler) listLayers(w http.ResponseWriter, r *http.Request) {
	layers, err := h.fn.ListLayers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	layers = filterLayerVersions(layers, r.URL.Query())

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

// serveLayerVersionPolicy handles POST (AddLayerVersionPermission) and GET
// (GetLayerVersionPolicy) on .../layers/{name}/versions/{version}/policy.
func (h *Handler) serveLayerVersionPolicy(w http.ResponseWriter, r *http.Request, name, versionStr string) {
	pm, ok := h.fn.(layerPolicyManager)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "layer version policies not supported")
		return
	}

	version, ok := parseLayerVersionNumber(w, versionStr)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodPost:
		addLayerVersionPermission(w, r, pm, name, version)
	case http.MethodGet:
		policy, revisionID, err := pm.GetLayerVersionPolicy(r.Context(), name, version)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"Policy": policy, "RevisionId": revisionID})
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// addLayerVersionPermission handles the POST case of serveLayerVersionPolicy.
func addLayerVersionPermission(w http.ResponseWriter, r *http.Request, pm layerPolicyManager, name string, version int) {
	var req addLayerVersionPermissionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	stmt := sdrv.LayerPermissionStatement{
		StatementID: req.StatementID, Action: req.Action,
		Principal: req.Principal, OrganizationID: req.OrganizationID,
	}

	stmtJSON, revisionID, err := pm.AddLayerVersionPermission(r.Context(), name, version, stmt, req.RevisionID)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"Statement": stmtJSON, "RevisionId": revisionID})
}

// serveRemoveLayerVersionPermission handles DELETE
// .../layers/{name}/versions/{version}/policy/{statementId}
// (RemoveLayerVersionPermission).
func (h *Handler) serveRemoveLayerVersionPermission(w http.ResponseWriter, r *http.Request, name, versionStr, statementID string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
		return
	}

	pm, ok := h.fn.(layerPolicyManager)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "layer version policies not supported")
		return
	}

	version, ok := parseLayerVersionNumber(w, versionStr)
	if !ok {
		return
	}

	err := pm.RemoveLayerVersionPermission(r.Context(), name, version, statementID, r.URL.Query().Get("RevisionId"))
	if err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
