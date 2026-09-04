package gcs

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

// hmacKeyStore is the optional capability that persists project-scoped
// service-account HMAC keys (the S3-interoperability credentials GCS mints at
// /storage/v1/projects/{project}/hmacKeys). It exchanges metadata as JSON so the
// wire layer stays decoupled from the driver's record type; the create secret is
// returned separately, surfaced to the client exactly once.
type hmacKeyStore interface {
	CreateHMACKeyGCS(ctx context.Context, projectID, serviceAccountEmail string) (metadata []byte, secret string, err error)
	ListHMACKeysGCS(ctx context.Context, projectID, serviceAccountEmail string, showDeleted bool) ([]byte, error)
	GetHMACKeyGCS(ctx context.Context, projectID, accessID string) ([]byte, error)
	UpdateHMACKeyStateGCS(ctx context.Context, projectID, accessID, state string) ([]byte, error)
	DeleteHMACKeyGCS(ctx context.Context, projectID, accessID string) error
}

// subresHMACKeys is the /projects/{project}/hmacKeys sub-collection segment.
const subresHMACKeys = "hmacKeys"

// hmacKeyMetadata is the storage#hmacKeyMetadata resource. The data fields carry
// json tags matching the driver's exchange struct so a provider metadata blob
// unmarshals straight into it; the handler then stamps kind/id/selfLink.
type hmacKeyMetadata struct {
	Kind                string `json:"kind"`
	ID                  string `json:"id,omitempty"`
	AccessID            string `json:"accessId"`
	ProjectID           string `json:"projectId"`
	ServiceAccountEmail string `json:"serviceAccountEmail"`
	State               string `json:"state"`
	TimeCreated         string `json:"timeCreated,omitempty"`
	Updated             string `json:"updated,omitempty"`
	Etag                string `json:"etag,omitempty"`
	SelfLink            string `json:"selfLink,omitempty"`
}

// hmacKeyResource is the storage#hmacKey create response: the metadata plus the
// secret, which GCS returns only here.
type hmacKeyResource struct {
	Kind     string          `json:"kind"`
	Metadata hmacKeyMetadata `json:"metadata"`
	Secret   string          `json:"secret"`
}

type hmacKeysListResponse struct {
	Kind  string            `json:"kind"`
	Items []hmacKeyMetadata `json:"items,omitempty"`
}

// projectRoute dispatches /projects/{project}/hmacKeys[/{accessId}] requests.
func (h *Handler) projectRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	// parts is ["projects", project, "hmacKeys", accessId?].
	if len(parts) < pathBO || parts[2] != subresHMACKeys {
		writeError(w, http.StatusNotFound, "notFound", "unknown project resource")
		return
	}

	project := parts[1]

	switch len(parts) {
	case pathBO:
		h.hmacCollection(w, r, project)
	case pathBOObj:
		h.hmacResource(w, r, project, parts[3])
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown hmacKeys resource")
	}
}

// hmacCollection serves /projects/{project}/hmacKeys — POST creates a key, GET
// lists keys.
func (h *Handler) hmacCollection(w http.ResponseWriter, r *http.Request, project string) {
	if h.hmac == nil {
		writeError(w, http.StatusNotImplemented, "notImplemented", "HMAC keys not supported")
		return
	}

	switch r.Method {
	case http.MethodPost:
		h.createHMACKey(w, r, project)
	case http.MethodGet:
		h.listHMACKeys(w, r, project)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// hmacResource serves /projects/{project}/hmacKeys/{accessId} — GET metadata,
// PUT state update, DELETE.
func (h *Handler) hmacResource(w http.ResponseWriter, r *http.Request, project, accessID string) {
	if h.hmac == nil {
		writeError(w, http.StatusNotImplemented, "notImplemented", "HMAC keys not supported")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getHMACKey(w, r, project, accessID)
	case http.MethodPut, http.MethodPost:
		h.updateHMACKey(w, r, project, accessID)
	case http.MethodDelete:
		h.deleteHMACKey(w, r, project, accessID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createHMACKey(w http.ResponseWriter, r *http.Request, project string) {
	email := r.URL.Query().Get("serviceAccountEmail")
	if email == "" {
		writeError(w, http.StatusBadRequest, "invalid", "serviceAccountEmail query parameter required")
		return
	}

	metaRaw, secret, err := h.hmac.CreateHMACKeyGCS(r.Context(), project, email)
	if err != nil {
		writeErr(w, err)
		return
	}

	meta, err := renderHMACMeta(metaRaw, r, project)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, hmacKeyResource{Kind: "storage#hmacKey", Metadata: meta, Secret: secret})
}

func (h *Handler) listHMACKeys(w http.ResponseWriter, r *http.Request, project string) {
	q := r.URL.Query()
	showDeleted, _ := strconv.ParseBool(q.Get("showDeletedKeys"))

	raw, err := h.hmac.ListHMACKeysGCS(r.Context(), project, q.Get("serviceAccountEmail"), showDeleted)
	if err != nil {
		writeErr(w, err)
		return
	}

	var metas []hmacKeyMetadata
	if uErr := json.Unmarshal(raw, &metas); uErr != nil {
		writeErr(w, uErr)
		return
	}

	for i := range metas {
		stampHMACMeta(&metas[i], r, project)
	}

	writeJSON(w, http.StatusOK, hmacKeysListResponse{Kind: "storage#hmacKeysMetadata", Items: metas})
}

func (h *Handler) getHMACKey(w http.ResponseWriter, r *http.Request, project, accessID string) {
	raw, err := h.hmac.GetHMACKeyGCS(r.Context(), project, accessID)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeHMACMeta(w, r, project, raw)
}

func (h *Handler) updateHMACKey(w http.ResponseWriter, r *http.Request, project, accessID string) {
	var body struct {
		State string `json:"state"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	raw, err := h.hmac.UpdateHMACKeyStateGCS(r.Context(), project, accessID, body.State)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeHMACMeta(w, r, project, raw)
}

func (h *Handler) deleteHMACKey(w http.ResponseWriter, r *http.Request, project, accessID string) {
	if err := h.hmac.DeleteHMACKeyGCS(r.Context(), project, accessID); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeHMACMeta renders a provider metadata blob into the wire resource and
// writes it.
func writeHMACMeta(w http.ResponseWriter, r *http.Request, project string, raw []byte) {
	meta, err := renderHMACMeta(raw, r, project)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, meta)
}

// renderHMACMeta unmarshals a driver metadata blob and stamps the wire-only
// fields (kind/id/selfLink).
func renderHMACMeta(raw []byte, r *http.Request, project string) (hmacKeyMetadata, error) {
	var meta hmacKeyMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return hmacKeyMetadata{}, err
	}

	stampHMACMeta(&meta, r, project)

	return meta, nil
}

// stampHMACMeta fills the wire-derived kind/id/selfLink fields on a metadata
// value whose data fields were populated from the driver.
func stampHMACMeta(meta *hmacKeyMetadata, r *http.Request, project string) {
	meta.Kind = "storage#hmacKeyMetadata"
	meta.ID = meta.ProjectID + "/" + meta.AccessID
	meta.SelfLink = selfLink(r, "/storage/v1/projects/"+project+"/hmacKeys/"+meta.AccessID)
}
