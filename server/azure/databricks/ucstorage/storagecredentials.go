package ucstorage

import (
	"encoding/json"
	"net/http"
	"time"
)

// storageCredential is the in-memory state for a single storage credential. The
// azure identity blocks are preserved as raw JSON so any of the documented
// shapes (managed identity / service principal) round-trips unchanged.
type storageCredential struct {
	name                  string
	comment               string
	readOnly              bool
	azureManagedIdentity  json.RawMessage
	azureServicePrincipal json.RawMessage
	createdAt             int64
	updatedAt             int64
}

type storageCredentialView struct {
	Name                  string          `json:"name"`
	FullName              string          `json:"full_name"`
	ID                    string          `json:"id"`
	Comment               string          `json:"comment,omitempty"`
	ReadOnly              bool            `json:"read_only"`
	AzureManagedIdentity  json.RawMessage `json:"azure_managed_identity,omitempty"`
	AzureServicePrincipal json.RawMessage `json:"azure_service_principal,omitempty"`
	MetastoreID           string          `json:"metastore_id"`
	CreatedAt             int64           `json:"created_at"`
	UpdatedAt             int64           `json:"updated_at"`
}

type createStorageCredentialRequest struct {
	Name                  string          `json:"name"`
	Comment               string          `json:"comment"`
	ReadOnly              bool            `json:"read_only"`
	AzureManagedIdentity  json.RawMessage `json:"azure_managed_identity"`
	AzureServicePrincipal json.RawMessage `json:"azure_service_principal"`
}

type updateStorageCredentialRequest struct {
	NewName               string          `json:"new_name"`
	Comment               string          `json:"comment"`
	ReadOnly              *bool           `json:"read_only"`
	AzureManagedIdentity  json.RawMessage `json:"azure_managed_identity"`
	AzureServicePrincipal json.RawMessage `json:"azure_service_principal"`
}

type listStorageCredentialsResponse struct {
	StorageCredentials []storageCredentialView `json:"storage_credentials"`
}

func (c *storageCredential) view() storageCredentialView {
	return storageCredentialView{
		Name:                  c.name,
		FullName:              c.name,
		ID:                    "credential-" + c.name,
		Comment:               c.comment,
		ReadOnly:              c.readOnly,
		AzureManagedIdentity:  c.azureManagedIdentity,
		AzureServicePrincipal: c.azureServicePrincipal,
		MetastoreID:           stubMetastoreID,
		CreatedAt:             c.createdAt,
		UpdatedAt:             c.updatedAt,
	}
}

func (h *Handler) serveStorageCredentials(w http.ResponseWriter, r *http.Request, item string) {
	if item == "" {
		h.serveStorageCredentialCollection(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getStorageCredential(w, item)
	case http.MethodPatch:
		updateResource(h, w, r, item, storageCredentialUpdateSpec(h))
	case http.MethodDelete:
		h.deleteStorageCredential(w, item)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveStorageCredentialCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createStorageCredential(w, r)
	case http.MethodGet:
		h.listStorageCredentials(w)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createStorageCredential(w http.ResponseWriter, r *http.Request) {
	var in createStorageCredentialRequest
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

	if _, ok := h.credentials[in.Name]; ok {
		writeError(w, http.StatusConflict, codeAlreadyExists, "storage credential already exists: "+in.Name)

		return
	}

	c := &storageCredential{
		name:                  in.Name,
		comment:               in.Comment,
		readOnly:              in.ReadOnly,
		azureManagedIdentity:  in.AzureManagedIdentity,
		azureServicePrincipal: in.AzureServicePrincipal,
		createdAt:             now,
		updatedAt:             now,
	}
	h.credentials[in.Name] = c

	writeJSON(w, c.view())
}

func (h *Handler) getStorageCredential(w http.ResponseWriter, name string) {
	h.mu.RLock()
	c, ok := h.credentials[name]
	h.mu.RUnlock()

	if !ok {
		notFound(w, "storage credential", name)

		return
	}

	writeJSON(w, c.view())
}

func (h *Handler) listStorageCredentials(w http.ResponseWriter) {
	h.mu.RLock()

	out := make([]storageCredentialView, 0, len(h.credentials))
	for _, c := range h.credentials {
		out = append(out, c.view())
	}
	h.mu.RUnlock()

	writeJSON(w, listStorageCredentialsResponse{StorageCredentials: out})
}

func storageCredentialUpdateSpec(h *Handler) updateSpec[updateStorageCredentialRequest, storageCredential] {
	return updateSpec[updateStorageCredentialRequest, storageCredential]{
		kind:  "storage credential",
		store: h.credentials,
		apply: applyStorageCredentialUpdate,
		setAt: func(c *storageCredential, t int64) { c.updatedAt = t },
		view:  func(c *storageCredential) any { return c.view() },
	}
}

func applyStorageCredentialUpdate(c *storageCredential, in *updateStorageCredentialRequest) string {
	if in.Comment != "" {
		c.comment = in.Comment
	}

	if in.ReadOnly != nil {
		c.readOnly = *in.ReadOnly
	}

	if len(in.AzureManagedIdentity) > 0 {
		c.azureManagedIdentity = in.AzureManagedIdentity
	}

	if len(in.AzureServicePrincipal) > 0 {
		c.azureServicePrincipal = in.AzureServicePrincipal
	}

	if in.NewName != "" {
		c.name = in.NewName
	}

	return c.name
}

func (h *Handler) deleteStorageCredential(w http.ResponseWriter, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.credentials[name]; !ok {
		notFound(w, "storage credential", name)

		return
	}

	delete(h.credentials, name)

	writeJSON(w, struct{}{})
}
