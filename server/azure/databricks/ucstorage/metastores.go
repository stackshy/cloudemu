package ucstorage

import (
	"net/http"
	"time"
)

const metastorePrivilegeModel = "1.0"

// metastore is the in-memory state for a single Unity Catalog metastore.
type metastore struct {
	id          string
	name        string
	region      string
	storageRoot string
	createdAt   int64
	updatedAt   int64
}

type metastoreView struct {
	MetastoreID           string `json:"metastore_id"`
	Name                  string `json:"name"`
	Region                string `json:"region,omitempty"`
	StorageRoot           string `json:"storage_root,omitempty"`
	PrivilegeModelVersion string `json:"privilege_model_version"`
	CreatedAt             int64  `json:"created_at"`
	UpdatedAt             int64  `json:"updated_at"`
}

type createMetastoreRequest struct {
	Name        string `json:"name"`
	Region      string `json:"region"`
	StorageRoot string `json:"storage_root"`
}

type updateMetastoreRequest struct {
	NewName string `json:"new_name"`
}

type listMetastoresResponse struct {
	Metastores []metastoreView `json:"metastores"`
}

func (m *metastore) view() metastoreView {
	return metastoreView{
		MetastoreID:           m.id,
		Name:                  m.name,
		Region:                m.region,
		StorageRoot:           m.storageRoot,
		PrivilegeModelVersion: metastorePrivilegeModel,
		CreatedAt:             m.createdAt,
		UpdatedAt:             m.updatedAt,
	}
}

func (h *Handler) serveMetastores(w http.ResponseWriter, r *http.Request, item string) {
	if item == "" {
		h.serveMetastoreCollection(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getMetastore(w, item)
	case http.MethodPatch:
		h.updateMetastore(w, r, item)
	case http.MethodDelete:
		h.deleteMetastore(w, item)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveMetastoreCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createMetastore(w, r)
	case http.MethodGet:
		h.listMetastores(w)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createMetastore(w http.ResponseWriter, r *http.Request) {
	var in createMetastoreRequest
	if !decode(w, r, &in) {
		return
	}

	if in.Name == "" {
		missingField(w, "name")

		return
	}

	now := time.Now().UnixMilli()

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.metastores[in.Name]; ok {
		writeError(w, http.StatusConflict, codeAlreadyExists, "metastore already exists: "+in.Name)

		return
	}

	m := &metastore{
		id:          "metastore-" + in.Name,
		name:        in.Name,
		region:      in.Region,
		storageRoot: in.StorageRoot,
		createdAt:   now,
		updatedAt:   now,
	}
	h.metastores[in.Name] = m

	writeJSON(w, m.view())
}

// findMetastore locates a metastore by name or by metastore_id. The SDK
// addresses metastores by id on Get/Update/Delete, while create returns both.
func (h *Handler) findMetastore(key string) *metastore {
	if m, ok := h.metastores[key]; ok {
		return m
	}

	for _, m := range h.metastores {
		if m.id == key {
			return m
		}
	}

	return nil
}

func (h *Handler) getMetastore(w http.ResponseWriter, key string) {
	h.mu.RLock()
	m := h.findMetastore(key)
	h.mu.RUnlock()

	if m == nil {
		notFound(w, "metastore", key)

		return
	}

	writeJSON(w, m.view())
}

func (h *Handler) listMetastores(w http.ResponseWriter) {
	h.mu.RLock()

	out := make([]metastoreView, 0, len(h.metastores))
	for _, m := range h.metastores {
		out = append(out, m.view())
	}
	h.mu.RUnlock()

	writeJSON(w, listMetastoresResponse{Metastores: out})
}

func (h *Handler) updateMetastore(w http.ResponseWriter, r *http.Request, key string) {
	var in updateMetastoreRequest
	if !decode(w, r, &in) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m := h.findMetastore(key)
	if m == nil {
		notFound(w, "metastore", key)

		return
	}

	if in.NewName != "" && in.NewName != m.name {
		delete(h.metastores, m.name)
		m.name = in.NewName
		m.id = "metastore-" + in.NewName
		h.metastores[in.NewName] = m
	}

	m.updatedAt = time.Now().UnixMilli()

	writeJSON(w, m.view())
}

func (h *Handler) deleteMetastore(w http.ResponseWriter, key string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	m := h.findMetastore(key)
	if m == nil {
		notFound(w, "metastore", key)

		return
	}

	delete(h.metastores, m.name)

	writeJSON(w, struct{}{})
}
