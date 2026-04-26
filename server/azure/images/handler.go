// Package images serves Azure ARM Microsoft.Compute/images requests.
package images

import (
	"context"
	"net/http"
	"strings"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName    = "Microsoft.Compute"
	resourceType    = "images"
	armNameTag      = "cloudemu:azureImageName"
	defaultLocation = "eastus"

	// vmARMNameTag mirrors the constant in server/azure/virtualmachines so
	// we can resolve a VM ARM ID to its driver-internal instance ID.
	vmARMNameTag = "cloudemu:azureName"
)

// Handler serves Microsoft.Compute/images requests.
type Handler struct {
	compute computedriver.Compute
}

// New returns an images handler.
func New(c computedriver.Compute) *Handler {
	return &Handler{compute: c}
}

func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == resourceType
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		h.serveCollection(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, rp)
	case http.MethodGet:
		h.get(w, r, rp)
	case http.MethodDelete:
		h.delete(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"not implemented: "+r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"not implemented: "+r.Method+" "+r.URL.Path)

		return
	}

	imgs, err := h.compute.DescribeImages(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]imageResponse, 0, len(imgs))

	for i := range imgs {
		name := tagOr(imgs[i].Tags, armNameTag, imgs[i].Name)
		scope := rp
		scope.ResourceName = name
		out = append(out, toImageResponse(&imgs[i], scope, ""))
	}

	azurearm.WriteJSON(w, http.StatusOK, imageListResponse{Value: out})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req imageRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	driverInstanceID, err := h.resolveSourceVMID(r.Context(), sourceVMID(req.Properties.SourceVirtualMachine))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	cfg := computedriver.ImageConfig{
		InstanceID: driverInstanceID,
		Name:       rp.ResourceName,
		Tags:       mergeTags(req.Tags, rp.ResourceName),
	}

	img, err := h.compute.CreateImage(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	location := req.Location
	if location == "" {
		location = defaultLocation
	}

	body := toImageResponse(img, rp, location)

	// Azure SDK images poller is finicky about LRO terminal-state detection.
	// Return 200 OK directly (sync semantics) so PollUntilDone resolves on
	// the response body's ProvisioningState alone, no header polling.
	azurearm.WriteJSON(w, http.StatusOK, body)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	img, err := findImageByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toImageResponse(img, rp, ""))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	img, err := findImageByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.compute.DeregisterImage(r.Context(), img.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeImageAsync(w, r, rp.Subscription, "img-delete-"+rp.ResourceName, nil)
}

func findImageByName(ctx context.Context, c computedriver.Compute, name string) (*computedriver.ImageInfo, error) {
	imgs, err := c.DescribeImages(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range imgs {
		if tagOr(imgs[i].Tags, armNameTag, imgs[i].Name) == name {
			return &imgs[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "image %s not found", name)
}

// resolveSourceVMID translates an ARM VM resource ID to the driver-internal
// instance ID by looking up the VM via its ARM-name tag.
func (h *Handler) resolveSourceVMID(ctx context.Context, src string) (string, error) {
	if src == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "sourceVirtualMachine.id is required")
	}

	idx := strings.LastIndex(src, "/virtualMachines/")
	if idx < 0 {
		return src, nil
	}

	name := src[idx+len("/virtualMachines/"):]
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}

	insts, err := h.compute.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return "", err
	}

	for i := range insts {
		if tagOr(insts[i].Tags, vmARMNameTag, "") == name {
			return insts[i].ID, nil
		}
	}

	return "", cerrors.Newf(cerrors.NotFound, "source VM %s not found", name)
}

func sourceVMID(r *resourceRef) string {
	if r == nil {
		return ""
	}

	return r.ID
}

func writeImageAsync(w http.ResponseWriter, r *http.Request, sub, opID string, body any) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	statusURL := scheme + "://" + r.Host +
		"/subscriptions/" + sub +
		"/providers/Microsoft.Compute/locations/eastus/operationStatuses/" + opID +
		"?api-version=2023-09-01"

	w.Header().Set("Azure-AsyncOperation", statusURL)
	w.Header().Set("Location", statusURL)
	w.Header().Set("Retry-After", "0")

	if body != nil {
		azurearm.WriteJSON(w, http.StatusAccepted, body)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

//nolint:gocritic // rp is a request-scoped value
func toImageResponse(img *computedriver.ImageInfo, rp azurearm.ResourcePath, location string) imageResponse {
	if location == "" {
		location = defaultLocation
	}

	name := tagOr(img.Tags, armNameTag, img.Name)

	return imageResponse{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, name),
		Name:     name,
		Type:     providerName + "/" + resourceType,
		Location: location,
		Tags:     stripInternalTags(img.Tags),
		Properties: imageResponseProps{
			ProvisioningState: "Succeeded",
		},
	}
}

func mergeTags(in map[string]string, name string) map[string]string {
	out := make(map[string]string, len(in)+1)

	for k, v := range in {
		out[k] = v
	}

	out[armNameTag] = name

	return out
}

func tagOr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}

	return fallback
}

func stripInternalTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if k == armNameTag {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}
