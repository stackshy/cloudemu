package keyvault

import (
	"encoding/base64"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// cryptoOp decodes a keyOperationRequest, runs op, and writes the result.
func cryptoOp(
	w http.ResponseWriter, r *http.Request, name, version string,
	op func(vault, name, version string, p secretsdriver.KVCryptoParams) (*secretsdriver.KVCryptoResult, error),
) {
	var req keyOperationRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	value, err := base64.RawURLEncoding.DecodeString(req.Value)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BadParameter", "invalid base64url value")
		return
	}

	res, err := op(vaultFromRequest(r), name, version, secretsdriver.KVCryptoParams{Algorithm: req.Alg, Value: value})
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	writeJSON(w, keyOperationResultJSON{
		KID:   keyID(r, name, res.Version),
		Value: base64.RawURLEncoding.EncodeToString(res.Value),
	})
}

func (h *KeysHandler) encrypt(w http.ResponseWriter, r *http.Request, name, version string) {
	cryptoOp(w, r, name, version, func(vault, n, v string, p secretsdriver.KVCryptoParams) (*secretsdriver.KVCryptoResult, error) {
		return h.kv.EncryptKey(r.Context(), vault, n, v, p)
	})
}

func (h *KeysHandler) decrypt(w http.ResponseWriter, r *http.Request, name, version string) {
	cryptoOp(w, r, name, version, func(vault, n, v string, p secretsdriver.KVCryptoParams) (*secretsdriver.KVCryptoResult, error) {
		return h.kv.DecryptKey(r.Context(), vault, n, v, p)
	})
}

func (h *KeysHandler) wrapKey(w http.ResponseWriter, r *http.Request, name, version string) {
	cryptoOp(w, r, name, version, func(vault, n, v string, p secretsdriver.KVCryptoParams) (*secretsdriver.KVCryptoResult, error) {
		return h.kv.WrapKey(r.Context(), vault, n, v, p)
	})
}

func (h *KeysHandler) unwrapKey(w http.ResponseWriter, r *http.Request, name, version string) {
	cryptoOp(w, r, name, version, func(vault, n, v string, p secretsdriver.KVCryptoParams) (*secretsdriver.KVCryptoResult, error) {
		return h.kv.UnwrapKey(r.Context(), vault, n, v, p)
	})
}

func (h *KeysHandler) sign(w http.ResponseWriter, r *http.Request, name, version string) {
	cryptoOp(w, r, name, version, func(vault, n, v string, p secretsdriver.KVCryptoParams) (*secretsdriver.KVCryptoResult, error) {
		return h.kv.SignKey(r.Context(), vault, n, v, p)
	})
}

func (h *KeysHandler) verify(w http.ResponseWriter, r *http.Request, name, version string) {
	var req verifyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	digest, err := base64.RawURLEncoding.DecodeString(req.Digest)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BadParameter", "invalid base64url digest")
		return
	}

	sig, err := base64.RawURLEncoding.DecodeString(req.Value)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BadParameter", "invalid base64url signature")
		return
	}

	ok, err := h.kv.VerifyKey(r.Context(), vaultFromRequest(r), name, version, secretsdriver.KVCryptoParams{
		Algorithm: req.Alg,
		Value:     digest,
		Signature: sig,
	})
	if err != nil {
		writeKeyErr(w, err)
		return
	}

	writeJSON(w, keyVerifyResultJSON{Value: ok})
}
