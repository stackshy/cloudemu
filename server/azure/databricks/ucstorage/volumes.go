package ucstorage

import (
	"net/http"
	"time"
)

const defaultVolumeType = "MANAGED"

// volume is the in-memory state for a single Unity Catalog volume. It is keyed
// in the handler by its dotted three-level full_name catalog.schema.name.
type volume struct {
	catalogName     string
	schemaName      string
	name            string
	volumeType      string
	comment         string
	storageLocation string
	createdAt       int64
	updatedAt       int64
}

func (v *volume) fullName() string {
	return v.catalogName + "." + v.schemaName + "." + v.name
}

type volumeView struct {
	CatalogName     string `json:"catalog_name"`
	SchemaName      string `json:"schema_name"`
	Name            string `json:"name"`
	FullName        string `json:"full_name"`
	VolumeID        string `json:"volume_id"`
	VolumeType      string `json:"volume_type"`
	Comment         string `json:"comment,omitempty"`
	StorageLocation string `json:"storage_location,omitempty"`
	MetastoreID     string `json:"metastore_id"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

type createVolumeRequest struct {
	CatalogName     string `json:"catalog_name"`
	SchemaName      string `json:"schema_name"`
	Name            string `json:"name"`
	VolumeType      string `json:"volume_type"`
	Comment         string `json:"comment"`
	StorageLocation string `json:"storage_location"`
}

type updateVolumeRequest struct {
	NewName string `json:"new_name"`
	Comment string `json:"comment"`
}

type listVolumesResponse struct {
	Volumes []volumeView `json:"volumes"`
}

func (v *volume) view() volumeView {
	return volumeView{
		CatalogName:     v.catalogName,
		SchemaName:      v.schemaName,
		Name:            v.name,
		FullName:        v.fullName(),
		VolumeID:        "volume-" + v.fullName(),
		VolumeType:      v.volumeType,
		Comment:         v.comment,
		StorageLocation: v.storageLocation,
		MetastoreID:     stubMetastoreID,
		CreatedAt:       v.createdAt,
		UpdatedAt:       v.updatedAt,
	}
}

func (h *Handler) serveVolumes(w http.ResponseWriter, r *http.Request, item string) {
	if item == "" {
		h.serveVolumeCollection(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getVolume(w, item)
	case http.MethodPatch:
		h.updateVolume(w, r, item)
	case http.MethodDelete:
		h.deleteVolume(w, item)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveVolumeCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createVolume(w, r)
	case http.MethodGet:
		h.listVolumes(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createVolume(w http.ResponseWriter, r *http.Request) {
	var in createVolumeRequest
	if !decode(w, r, &in) {
		return
	}

	if !validateVolumeRequest(w, &in) {
		return
	}

	volType := in.VolumeType
	if volType == "" {
		volType = defaultVolumeType
	}

	now := time.Now().UnixMilli()
	v := &volume{
		catalogName:     in.CatalogName,
		schemaName:      in.SchemaName,
		name:            in.Name,
		volumeType:      volType,
		comment:         in.Comment,
		storageLocation: in.StorageLocation,
		createdAt:       now,
		updatedAt:       now,
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.volumes[v.fullName()]; ok {
		writeError(w, http.StatusConflict, codeAlreadyExists, "volume already exists: "+v.fullName())

		return
	}

	h.volumes[v.fullName()] = v

	writeJSON(w, v.view())
}

func validateVolumeRequest(w http.ResponseWriter, in *createVolumeRequest) bool {
	if in.CatalogName == "" {
		missingField(w, "catalog_name")

		return false
	}

	if in.SchemaName == "" {
		missingField(w, "schema_name")

		return false
	}

	if in.Name == "" {
		missingField(w, "name")

		return false
	}

	return true
}

func (h *Handler) getVolume(w http.ResponseWriter, fullName string) {
	h.mu.RLock()
	v, ok := h.volumes[fullName]
	h.mu.RUnlock()

	if !ok {
		notFound(w, "volume", fullName)

		return
	}

	writeJSON(w, v.view())
}

func (h *Handler) listVolumes(w http.ResponseWriter, r *http.Request) {
	catalog := r.URL.Query().Get("catalog_name")
	schema := r.URL.Query().Get("schema_name")

	h.mu.RLock()

	out := make([]volumeView, 0, len(h.volumes))

	for _, v := range h.volumes {
		if catalog != "" && v.catalogName != catalog {
			continue
		}

		if schema != "" && v.schemaName != schema {
			continue
		}

		out = append(out, v.view())
	}
	h.mu.RUnlock()

	writeJSON(w, listVolumesResponse{Volumes: out})
}

func (h *Handler) updateVolume(w http.ResponseWriter, r *http.Request, fullName string) {
	var in updateVolumeRequest
	if !decode(w, r, &in) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	v, ok := h.volumes[fullName]
	if !ok {
		notFound(w, "volume", fullName)

		return
	}

	if in.Comment != "" {
		v.comment = in.Comment
	}

	if in.NewName != "" && in.NewName != v.name {
		v.name = in.NewName

		delete(h.volumes, fullName)
		h.volumes[v.fullName()] = v
	}

	v.updatedAt = time.Now().UnixMilli()

	writeJSON(w, v.view())
}

func (h *Handler) deleteVolume(w http.ResponseWriter, fullName string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.volumes[fullName]; !ok {
		notFound(w, "volume", fullName)

		return
	}

	delete(h.volumes, fullName)

	writeJSON(w, struct{}{})
}
