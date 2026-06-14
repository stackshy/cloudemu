package ucstorage

import (
	"net/http"
	"time"
)

// externalLocation is the in-memory state for a single external location.
type externalLocation struct {
	name           string
	url            string
	credentialName string
	comment        string
	readOnly       bool
	createdAt      int64
	updatedAt      int64
}

type externalLocationView struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	CredentialName string `json:"credential_name,omitempty"`
	Comment        string `json:"comment,omitempty"`
	ReadOnly       bool   `json:"read_only"`
	MetastoreID    string `json:"metastore_id"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

type createExternalLocationRequest struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	CredentialName string `json:"credential_name"`
	Comment        string `json:"comment"`
	ReadOnly       bool   `json:"read_only"`
}

type updateExternalLocationRequest struct {
	NewName        string `json:"new_name"`
	URL            string `json:"url"`
	CredentialName string `json:"credential_name"`
	Comment        string `json:"comment"`
	ReadOnly       *bool  `json:"read_only"`
}

type listExternalLocationsResponse struct {
	ExternalLocations []externalLocationView `json:"external_locations"`
}

func (e *externalLocation) view() externalLocationView {
	return externalLocationView{
		Name:           e.name,
		URL:            e.url,
		CredentialName: e.credentialName,
		Comment:        e.comment,
		ReadOnly:       e.readOnly,
		MetastoreID:    stubMetastoreID,
		CreatedAt:      e.createdAt,
		UpdatedAt:      e.updatedAt,
	}
}

func (h *Handler) serveExternalLocations(w http.ResponseWriter, r *http.Request, item string) {
	if item == "" {
		h.serveExternalLocationCollection(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getExternalLocation(w, item)
	case http.MethodPatch:
		updateResource(h, w, r, item, externalLocationUpdateSpec(h))
	case http.MethodDelete:
		h.deleteExternalLocation(w, item)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveExternalLocationCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createExternalLocation(w, r)
	case http.MethodGet:
		h.listExternalLocations(w)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createExternalLocation(w http.ResponseWriter, r *http.Request) {
	var in createExternalLocationRequest
	if !decode(w, r, &in) {
		return
	}

	if in.Name == "" {
		missingField(w, "name")

		return
	}

	if in.URL == "" {
		missingField(w, "url")

		return
	}

	now := time.Now().UnixMilli()

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.extLocs[in.Name]; ok {
		writeError(w, http.StatusConflict, codeAlreadyExists, "external location already exists: "+in.Name)

		return
	}

	e := &externalLocation{
		name:           in.Name,
		url:            in.URL,
		credentialName: in.CredentialName,
		comment:        in.Comment,
		readOnly:       in.ReadOnly,
		createdAt:      now,
		updatedAt:      now,
	}
	h.extLocs[in.Name] = e

	writeJSON(w, e.view())
}

func (h *Handler) getExternalLocation(w http.ResponseWriter, name string) {
	h.mu.RLock()
	e, ok := h.extLocs[name]
	h.mu.RUnlock()

	if !ok {
		notFound(w, "external location", name)

		return
	}

	writeJSON(w, e.view())
}

func (h *Handler) listExternalLocations(w http.ResponseWriter) {
	h.mu.RLock()

	out := make([]externalLocationView, 0, len(h.extLocs))
	for _, e := range h.extLocs {
		out = append(out, e.view())
	}
	h.mu.RUnlock()

	writeJSON(w, listExternalLocationsResponse{ExternalLocations: out})
}

func externalLocationUpdateSpec(h *Handler) updateSpec[updateExternalLocationRequest, externalLocation] {
	return updateSpec[updateExternalLocationRequest, externalLocation]{
		kind:  "external location",
		store: h.extLocs,
		apply: applyExternalLocationUpdate,
		setAt: func(e *externalLocation, t int64) { e.updatedAt = t },
		view:  func(e *externalLocation) any { return e.view() },
	}
}

func applyExternalLocationUpdate(e *externalLocation, in *updateExternalLocationRequest) string {
	if in.URL != "" {
		e.url = in.URL
	}

	if in.CredentialName != "" {
		e.credentialName = in.CredentialName
	}

	if in.Comment != "" {
		e.comment = in.Comment
	}

	if in.ReadOnly != nil {
		e.readOnly = *in.ReadOnly
	}

	if in.NewName != "" {
		e.name = in.NewName
	}

	return e.name
}

func (h *Handler) deleteExternalLocation(w http.ResponseWriter, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.extLocs[name]; !ok {
		notFound(w, "external location", name)

		return
	}

	delete(h.extLocs, name)

	writeJSON(w, struct{}{})
}
