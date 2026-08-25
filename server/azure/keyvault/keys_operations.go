package keyvault

import (
	"encoding/base64"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

func keyAttrsFromRequest(a *setKeyAttributesJSON) secretsdriver.KVKeyAttributes {
	attrs := secretsdriver.KVKeyAttributes{Enabled: true}
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

func (h *KeysHandler) createKey(w http.ResponseWriter, r *http.Request, name string) {
	var req createKeyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	key, err := h.kv.CreateKey(r.Context(), vaultFromRequest(r), name, &secretsdriver.KVCreateKeyParams{
		Kty:            req.Kty,
		KeySize:        req.KeySize,
		Curve:          req.Crv,
		PublicExponent: req.PublicExponent,
		KeyOps:         req.KeyOps,
		Tags:           req.Tags,
		Attributes:     keyAttrsFromRequest(req.KeyAttributes),
	})
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	writeJSON(w, toKeyBundle(r, key))
}

func (h *KeysHandler) importKey(w http.ResponseWriter, r *http.Request, name string) {
	var req importKeyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	jwk, err := decodeImportJWK(&req.Key)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BadParameter", err.Error())
		return
	}

	key, err := h.kv.ImportKey(r.Context(), vaultFromRequest(r), name, &secretsdriver.KVImportKeyParams{
		Key:        jwk,
		HSM:        req.HSM,
		Tags:       req.Tags,
		Attributes: keyAttrsFromRequest(req.KeyAttributes),
	})
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	writeJSON(w, toKeyBundle(r, key))
}

func (h *KeysHandler) getKey(w http.ResponseWriter, r *http.Request, name, version string) {
	key, err := h.kv.GetKey(r.Context(), vaultFromRequest(r), name, version)
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	writeJSON(w, toKeyBundle(r, key))
}

func (h *KeysHandler) updateKey(w http.ResponseWriter, r *http.Request, name, version string) {
	var req updateKeyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	patch := secretsdriver.KVKeyPatch{}
	if req.Tags != nil {
		patch.Tags = req.Tags
		patch.SetTags = true
	}

	if req.KeyOps != nil {
		patch.KeyOps = req.KeyOps
		patch.SetKeyOps = true
	}

	if a := req.KeyAttributes; a != nil {
		patch.Enabled = a.Enabled
		patch.Expires = a.Expires
		patch.NotBefore = a.NotBefore
	}

	key, err := h.kv.UpdateKey(r.Context(), vaultFromRequest(r), name, version, patch)
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	writeJSON(w, toKeyBundle(r, key))
}

func (h *KeysHandler) deleteKey(w http.ResponseWriter, r *http.Request, name string) {
	deleted, err := h.kv.DeleteKey(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	writeJSON(w, toDeletedKeyBundle(r, deleted))
}

func (h *KeysHandler) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.kv.ListKeys(r.Context(), vaultFromRequest(r))
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	items := make([]keyItemJSON, 0, len(keys))
	for i := range keys {
		items = append(items, toKeyItem(r, &keys[i]))
	}

	writeJSON(w, keyListResponseJSON{Value: items})
}

func (h *KeysHandler) listKeyVersions(w http.ResponseWriter, r *http.Request, name string) {
	versions, err := h.kv.ListKeyVersions(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	items := make([]keyItemJSON, 0, len(versions))
	for i := range versions {
		items = append(items, toKeyItem(r, &versions[i]))
	}

	writeJSON(w, keyListResponseJSON{Value: items})
}

func (h *KeysHandler) listDeletedKeys(w http.ResponseWriter, r *http.Request) {
	deleted, err := h.kv.ListDeletedKeys(r.Context(), vaultFromRequest(r))
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	items := make([]deletedKeyItemJSON, 0, len(deleted))
	for i := range deleted {
		items = append(items, toDeletedKeyItem(r, &deleted[i]))
	}

	writeJSON(w, deletedKeyListResponseJSON{Value: items})
}

func (h *KeysHandler) getDeletedKey(w http.ResponseWriter, r *http.Request, name string) {
	deleted, err := h.kv.GetDeletedKey(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	writeJSON(w, toDeletedKeyBundle(r, deleted))
}

func (h *KeysHandler) recoverDeletedKey(w http.ResponseWriter, r *http.Request, name string) {
	key, err := h.kv.RecoverDeletedKey(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	writeJSON(w, toKeyBundle(r, key))
}

func (h *KeysHandler) purgeDeletedKey(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.kv.PurgeDeletedKey(r.Context(), vaultFromRequest(r), name); err != nil {
		writeKeyErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *KeysHandler) getKeyRotationPolicy(w http.ResponseWriter, r *http.Request, name string) {
	policy, err := h.kv.GetKeyRotationPolicy(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	writeJSON(w, toRotationPolicyJSON(r, name, policy))
}

func (h *KeysHandler) updateKeyRotationPolicy(w http.ResponseWriter, r *http.Request, name string) {
	var req keyRotationPolicyJSON
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policy, err := h.kv.UpdateKeyRotationPolicy(r.Context(), vaultFromRequest(r), name, fromRotationPolicyJSON(&req))
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	writeJSON(w, toRotationPolicyJSON(r, name, policy))
}

// decodeImportJWK decodes the base64url components of an inbound JWK.
func decodeImportJWK(in *jsonWebKeyImport) (secretsdriver.KVImportJWK, error) {
	out := secretsdriver.KVImportJWK{Kty: in.Kty, Curve: in.Crv, KeyOps: in.KeyOps}

	fields := []struct {
		src string
		dst *[]byte
	}{
		{in.N, &out.N}, {in.E, &out.E}, {in.D, &out.D}, {in.P, &out.P}, {in.Q, &out.Q},
		{in.DP, &out.DP}, {in.DQ, &out.DQ}, {in.QI, &out.QI}, {in.X, &out.X}, {in.Y, &out.Y}, {in.K, &out.K},
	}

	for _, f := range fields {
		if f.src == "" {
			continue
		}

		b, err := base64.RawURLEncoding.DecodeString(f.src)
		if err != nil {
			return out, err
		}

		*f.dst = b
	}

	return out, nil
}
