package keyvault

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// writeJSON writes v as a 200 application/json response. Non-200 replies (the
// bearer challenge, errors, and the 204 from purge) are written directly.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(v)
}

// attrsFromRequest maps a request attributes sub-object to driver attributes,
// defaulting Enabled to true (Key Vault's default for a new version).
func attrsFromRequest(a *setSecretAttributesJSON) secretsdriver.KVAttributes {
	attrs := secretsdriver.KVAttributes{Enabled: true}
	if a == nil {
		return attrs
	}

	if a.Enabled != nil {
		attrs.Enabled = *a.Enabled
	}

	if a.Expires != nil {
		attrs.Expires = *a.Expires
	}

	if a.NotBefore != nil {
		attrs.NotBefore = *a.NotBefore
	}

	return attrs
}

func (h *Handler) setSecret(w http.ResponseWriter, r *http.Request, name string) {
	var req setSecretRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	kv, err := h.kv.SetKeyVaultSecret(r.Context(), vaultFromRequest(r), name, secretsdriver.KVSetParams{
		Value:       []byte(req.Value),
		ContentType: req.ContentType,
		Tags:        req.Tags,
		Attributes:  attrsFromRequest(req.SecretAttributes),
	})
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toBundle(r, kv))
}

func (h *Handler) getSecret(w http.ResponseWriter, r *http.Request, name, version string) {
	// Disabled, not-yet-valid and expired gating lives in the driver (against
	// the injected clock, so it is deterministic under a FakeClock) rather than
	// here, so it maps through writeCErr like every other driver error.
	kv, err := h.kv.GetKeyVaultSecret(r.Context(), vaultFromRequest(r), name, version)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toBundle(r, kv))
}

func (h *Handler) updateSecret(w http.ResponseWriter, r *http.Request, name, version string) {
	var req updateSecretRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	patch := secretsdriver.KVPatch{ContentType: req.ContentType}
	if req.Tags != nil {
		patch.Tags = req.Tags
		patch.SetTags = true
	}

	if a := req.SecretAttributes; a != nil {
		patch.Enabled = a.Enabled
		patch.Expires = a.Expires
		patch.NotBefore = a.NotBefore
	}

	kv, err := h.kv.UpdateKeyVaultSecret(r.Context(), vaultFromRequest(r), name, version, patch)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toBundle(r, kv))
}

func (h *Handler) deleteSecret(w http.ResponseWriter, r *http.Request, name string) {
	deleted, err := h.kv.DeleteKeyVaultSecret(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toDeletedBundle(r, deleted))
}

func (h *Handler) listSecrets(w http.ResponseWriter, r *http.Request) {
	secrets, err := h.kv.ListKeyVaultSecrets(r.Context(), vaultFromRequest(r))
	if err != nil {
		writeCErr(w, err)
		return
	}

	items := make([]secretItemJSON, 0, len(secrets))
	for i := range secrets {
		items = append(items, toItem(r, &secrets[i]))
	}

	writeJSON(w, listResponseJSON{Value: items})
}

func (h *Handler) listSecretVersions(w http.ResponseWriter, r *http.Request, name string) {
	versions, err := h.kv.ListKeyVaultSecretVersions(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeCErr(w, err)
		return
	}

	items := make([]secretItemJSON, 0, len(versions))
	for i := range versions {
		items = append(items, toItem(r, &versions[i]))
	}

	writeJSON(w, listResponseJSON{Value: items})
}

func (h *Handler) backupSecret(w http.ResponseWriter, r *http.Request, name string) {
	blob, err := h.kv.BackupKeyVaultSecret(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, backupResultJSON{Value: base64.RawURLEncoding.EncodeToString(blob)})
}

func (h *Handler) restoreSecret(w http.ResponseWriter, r *http.Request) {
	var req restoreRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	blob, err := base64.RawURLEncoding.DecodeString(req.Value)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BadParameter", "invalid secret backup blob")
		return
	}

	kv, err := h.kv.RestoreKeyVaultSecret(r.Context(), vaultFromRequest(r), blob)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toBundle(r, kv))
}

func (h *Handler) listDeletedSecrets(w http.ResponseWriter, r *http.Request) {
	deleted, err := h.kv.ListDeletedKeyVaultSecrets(r.Context(), vaultFromRequest(r))
	if err != nil {
		writeCErr(w, err)
		return
	}

	items := make([]deletedSecretItemJSON, 0, len(deleted))
	for i := range deleted {
		items = append(items, toDeletedItem(r, &deleted[i]))
	}

	writeJSON(w, deletedListResponseJSON{Value: items})
}

func (h *Handler) getDeletedSecret(w http.ResponseWriter, r *http.Request, name string) {
	deleted, err := h.kv.GetDeletedKeyVaultSecret(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toDeletedBundle(r, deleted))
}

func (h *Handler) recoverDeletedSecret(w http.ResponseWriter, r *http.Request, name string) {
	kv, err := h.kv.RecoverDeletedKeyVaultSecret(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, toBundle(r, kv))
}

func (h *Handler) purgeDeletedSecret(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.kv.PurgeDeletedKeyVaultSecret(r.Context(), vaultFromRequest(r), name); err != nil {
		writeCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
