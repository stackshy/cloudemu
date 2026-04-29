// Package disks serves Azure ARM Microsoft.Compute/disks requests against a
// CloudEmu compute driver's volume operations.
//
// Supported operations:
//
//	PUT    .../disks/{name}  — CreateOrUpdate (returns 202 + Azure-AsyncOperation)
//	GET    .../disks/{name}  — Get
//	GET    .../disks         — List in resource group
//	DELETE .../disks/{name}  — Delete (returns 202 + Azure-AsyncOperation)
package disks

import (
	"context"
	"net/http"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName    = "Microsoft.Compute"
	resourceType    = "disks"
	armNameTag      = "cloudemu:azureDiskName"
	defaultLocation = "eastus"
)

// Handler serves Microsoft.Compute/disks requests.
type Handler struct {
	compute computedriver.Compute
}

// New returns a disks handler backed by c. The underlying driver's volume
// methods (CreateVolume, DescribeVolumes, DeleteVolume) provide the storage.
func New(c computedriver.Compute) *Handler {
	return &Handler{compute: c}
}

// Matches returns true for ARM disks paths.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == resourceType
}

// ServeHTTP routes the request based on method and presence of a name segment.
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

	vols, err := h.compute.DescribeVolumes(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]diskResponse, 0, len(vols))

	for i := range vols {
		name := tagOr(vols[i].Tags, armNameTag, vols[i].ID)
		scope := rp
		scope.ResourceName = name
		out = append(out, toDiskResponse(&vols[i], scope, ""))
	}

	azurearm.WriteJSON(w, http.StatusOK, diskListResponse{Value: out})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req diskRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	cfg := computedriver.VolumeConfig{
		Size:       req.Properties.DiskSizeGB,
		VolumeType: skuName(req.SKU),
		Tags:       mergeDiskTags(req.Tags, rp.ResourceName),
	}

	vol, err := h.compute.CreateVolume(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	location := req.Location
	if location == "" {
		location = defaultLocation
	}

	body := toDiskResponse(vol, rp, location)
	body.SKU = req.SKU

	writeDiskAsync(w, r, rp.Subscription, "disk-create-"+rp.ResourceName, body)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	vol, err := findDiskByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toDiskResponse(vol, rp, ""))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	vol, err := findDiskByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.compute.DeleteVolume(r.Context(), vol.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDiskAsync(w, r, rp.Subscription, "disk-delete-"+rp.ResourceName, nil)
}

// findDiskByName looks up a volume by its ARM-tagged name.
func findDiskByName(ctx context.Context, c computedriver.Compute, name string) (*computedriver.VolumeInfo, error) {
	vols, err := c.DescribeVolumes(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range vols {
		if tagOr(vols[i].Tags, armNameTag, "") == name {
			return &vols[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "disk %s not found", name)
}

// writeDiskAsync replies 202 + Azure-AsyncOperation header. Optionally
// returns a JSON body for ops that the SDK reads (e.g. CreateOrUpdate body).
func writeDiskAsync(w http.ResponseWriter, r *http.Request, sub, opID string, body any) {
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

// toDiskResponse maps a driver VolumeInfo to an ARM disk JSON body.
//
//nolint:gocritic // rp is a request-scoped value
func toDiskResponse(vol *computedriver.VolumeInfo, rp azurearm.ResourcePath, location string) diskResponse {
	if location == "" {
		location = defaultLocation
	}

	name := tagOr(vol.Tags, armNameTag, rp.ResourceName)

	return diskResponse{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, name),
		Name:     name,
		Type:     providerName + "/" + resourceType,
		Location: location,
		Tags:     stripInternalDiskTags(vol.Tags),
		Properties: diskResponseProps{
			ProvisioningState: "Succeeded",
			DiskSizeGB:        vol.Size,
			DiskState:         diskStateFor(vol.State),
			CreationData:      &creationData{CreateOption: "Empty"},
		},
	}
}

// ARM disk states we expose. Real Azure has more (ActiveSAS, ReadyToUpload,
// etc.) but the driver only models attached/unattached.
const (
	diskStateUnattached = "Unattached"
	diskStateAttached   = "Attached"
)

func diskStateFor(state string) string {
	if state == "in-use" {
		return diskStateAttached
	}

	return diskStateUnattached
}

func skuName(s *diskSKU) string {
	if s == nil {
		return ""
	}

	return s.Name
}

func mergeDiskTags(in map[string]string, name string) map[string]string {
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

func stripInternalDiskTags(in map[string]string) map[string]string {
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
