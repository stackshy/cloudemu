package kms

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	kmsprov "github.com/stackshy/cloudemu/v2/providers/gcp/kms"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	algorithmUnspecified       = "CRYPTO_KEY_VERSION_ALGORITHM_UNSPECIFIED"
	protectionLevelUnspecified = "PROTECTION_LEVEL_UNSPECIFIED"
	trueValue                  = "true"
	// algorithmSymmetric is the default versionTemplate.algorithm real Cloud KMS
	// assigns a symmetric ENCRYPT_DECRYPT key when the caller omits it.
	algorithmSymmetric = "GOOGLE_SYMMETRIC_ENCRYPTION"
	purposeUnspecified = "CRYPTO_KEY_PURPOSE_UNSPECIFIED"
	// defaultDestroyScheduledDuration is the DESTROY_SCHEDULED dwell time a
	// CryptoKey carries when create omits destroyScheduledDuration (24h).
	defaultDestroyScheduledDuration = "86400s"
	// defaultProtectionLevel is applied when a versionTemplate omits it.
	defaultProtectionLevel = "SOFTWARE"
)

// writeKMSErr maps a canonical error to Cloud KMS's HTTP response. An illegal
// state transition is FAILED_PRECONDITION, which real Cloud KMS reports as HTTP
// 400 (not the 409 the shared gcprest mapping would pick), so it is handled
// locally without changing that cross-cutting mapping.
func writeKMSErr(w http.ResponseWriter, err error) {
	if cerrors.IsFailedPrecondition(err) {
		gcprest.WriteError(w, http.StatusBadRequest, "failedPrecondition", cerrors.Message(err))
		return
	}

	gcprest.WriteCErr(w, err)
}

func invalidArg(w http.ResponseWriter, msg string) {
	gcprest.WriteError(w, http.StatusBadRequest, "invalid", msg)
}

// maskHas reports whether an update mask names any of the given field aliases.
// An empty mask means "update every field present in the body".
func maskHas(mask string, fields ...string) bool {
	if strings.TrimSpace(mask) == "" {
		return true
	}

	for _, f := range strings.Split(mask, ",") {
		f = strings.TrimSpace(f)
		for _, want := range fields {
			if f == want {
				return true
			}
		}
	}

	return false
}

// --- key rings ---

func (h *Handler) createKeyRing(w http.ResponseWriter, r *http.Request, rt *route) {
	id := r.URL.Query().Get("keyRingId")
	if id == "" {
		invalidArg(w, "keyRingId is required")
		return
	}

	rt.keyRing = id

	kr, err := h.kms.CreateKeyRing(rt.ref())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toKeyRingJSON(&kr))
}

func (h *Handler) getKeyRing(w http.ResponseWriter, rt *route) {
	kr, err := h.kms.GetKeyRing(rt.ref())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toKeyRingJSON(&kr))
}

func (h *Handler) listKeyRings(w http.ResponseWriter, rt *route) {
	rings := h.kms.ListKeyRings(rt.ref())

	out := make([]keyRingJSON, 0, len(rings))
	for i := range rings {
		out = append(out, toKeyRingJSON(&rings[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listKeyRingsResponse{KeyRings: out, TotalSize: len(out)})
}

// --- crypto keys ---

func (h *Handler) createCryptoKey(w http.ResponseWriter, r *http.Request, rt *route) {
	id := r.URL.Query().Get("cryptoKeyId")
	if id == "" {
		invalidArg(w, "cryptoKeyId is required")
		return
	}

	var req createCryptoKeyRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	cfg, ok := buildCryptoKeyConfig(w, id, &req)
	if !ok {
		return
	}

	skip := r.URL.Query().Get("skipInitialVersionCreation") == trueValue

	ck, err := h.kms.CreateCryptoKey(rt.ref(), &cfg, skip)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toCryptoKeyJSON(&ck))
}

// buildCryptoKeyConfig validates and normalizes a create body. On any
// validation failure it writes the error and returns ok=false.
func buildCryptoKeyConfig(w http.ResponseWriter, id string, req *createCryptoKeyRequest) (kmsprov.KeyConfig, bool) {
	purpose, ok, present := req.Purpose.normalize(purposeNames)
	if !present || !ok || purpose == purposeUnspecified {
		invalidArg(w, "purpose is required and must be a valid CryptoKeyPurpose")
		return kmsprov.KeyConfig{}, false
	}

	algo, prot, ok := normalizeVersionTemplate(w, req.VersionTemplate, purpose)
	if !ok {
		return kmsprov.KeyConfig{}, false
	}

	dsd := req.DestroyScheduledDuration
	if dsd == "" {
		dsd = defaultDestroyScheduledDuration
	} else if _, ok := kmsprov.ParseDurationSeconds(dsd); !ok {
		invalidArg(w, "invalid destroyScheduledDuration")
		return kmsprov.KeyConfig{}, false
	}

	if req.RotationPeriod != "" {
		if _, ok := kmsprov.ParseDurationSeconds(req.RotationPeriod); !ok {
			invalidArg(w, "invalid rotationPeriod")
			return kmsprov.KeyConfig{}, false
		}
	}

	return kmsprov.KeyConfig{
		ID:                       id,
		Purpose:                  purpose,
		RotationPeriod:           req.RotationPeriod,
		NextRotationTime:         req.NextRotationTime,
		ProtectionLevel:          prot,
		Algorithm:                algo,
		Labels:                   req.Labels,
		ImportOnly:               req.ImportOnly,
		DestroyScheduledDuration: dsd,
		CryptoKeyBackend:         req.CryptoKeyBackend,
	}, true
}

// normalizeVersionTemplate resolves versionTemplate.algorithm and
// protectionLevel for a create. A symmetric ENCRYPT_DECRYPT key defaults the
// algorithm to GOOGLE_SYMMETRIC_ENCRYPTION (and protectionLevel to SOFTWARE)
// when the caller omits versionTemplate or its algorithm, matching real Cloud
// KMS; every other purpose requires an explicit, valid algorithm. An explicit
// algorithm or protectionLevel is always honored.
func normalizeVersionTemplate(w http.ResponseWriter, vt *versionTemplateJSON, purpose string) (algo, prot string, ok bool) {
	prot, ok = resolveProtectionLevel(w, vt)
	if !ok {
		return "", "", false
	}

	var (
		algoOK, present bool
	)

	if vt != nil {
		algo, algoOK, present = vt.Algorithm.normalize(algorithmNames)
	}

	if !present || algo == algorithmUnspecified {
		// Symmetric ENCRYPT_DECRYPT keys default the algorithm; every other purpose
		// still requires the caller to name one.
		if purpose == kmsprov.PurposeEncryptDecrypt {
			return algorithmSymmetric, prot, true
		}

		invalidArg(w, "versionTemplate.algorithm is required and must be a valid algorithm")

		return "", "", false
	}

	if !algoOK {
		invalidArg(w, "versionTemplate.algorithm is required and must be a valid algorithm")

		return "", "", false
	}

	return algo, prot, true
}

// resolveProtectionLevel validates an optional versionTemplate.protectionLevel,
// defaulting an absent or unspecified value to SOFTWARE.
func resolveProtectionLevel(w http.ResponseWriter, vt *versionTemplateJSON) (string, bool) {
	if vt == nil {
		return defaultProtectionLevel, true
	}

	prot, protOK, protPresent := vt.ProtectionLevel.normalize(protectionLevelNames)
	if protPresent && !protOK {
		invalidArg(w, "invalid versionTemplate.protectionLevel")

		return "", false
	}

	if !protPresent || prot == protectionLevelUnspecified {
		prot = defaultProtectionLevel
	}

	return prot, true
}

func (h *Handler) getCryptoKey(w http.ResponseWriter, rt *route) {
	ck, err := h.kms.GetCryptoKey(rt.ref())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toCryptoKeyJSON(&ck))
}

func (h *Handler) listCryptoKeys(w http.ResponseWriter, rt *route) {
	keys, err := h.kms.ListCryptoKeys(rt.ref())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]cryptoKeyJSON, 0, len(keys))
	for i := range keys {
		out = append(out, toCryptoKeyJSON(&keys[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listCryptoKeysResponse{CryptoKeys: out, TotalSize: len(out)})
}

func (h *Handler) patchCryptoKey(w http.ResponseWriter, r *http.Request, rt *route) {
	var req createCryptoKeyRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	patch, ok := buildCryptoKeyPatch(w, r.URL.Query().Get("updateMask"), &req)
	if !ok {
		return
	}

	ck, err := h.kms.UpdateCryptoKey(rt.ref(), &patch)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toCryptoKeyJSON(&ck))
}

// buildCryptoKeyPatch turns a masked patch body into a KeyPatch.
func buildCryptoKeyPatch(w http.ResponseWriter, mask string, req *createCryptoKeyRequest) (kmsprov.KeyPatch, bool) {
	patch := kmsprov.KeyPatch{}

	if maskHas(mask, "labels") {
		patch.Labels = &req.Labels
	}

	if maskHas(mask, "rotationPeriod", "rotation_period") {
		patch.RotationPeriod = &req.RotationPeriod
	}

	if maskHas(mask, "nextRotationTime", "next_rotation_time") {
		patch.NextRotationTime = &req.NextRotationTime
	}

	if req.VersionTemplate != nil {
		if !applyVersionTemplatePatch(w, mask, req.VersionTemplate, &patch) {
			return kmsprov.KeyPatch{}, false
		}
	}

	return patch, true
}

func applyVersionTemplatePatch(w http.ResponseWriter, mask string, vt *versionTemplateJSON, patch *kmsprov.KeyPatch) bool {
	if maskHas(mask, "versionTemplate", "versionTemplate.algorithm", "version_template.algorithm") {
		algo, ok, present := vt.Algorithm.normalize(algorithmNames)
		if present && (!ok || algo == algorithmUnspecified) {
			invalidArg(w, "invalid versionTemplate.algorithm")
			return false
		}

		if present {
			patch.Algorithm = &algo
		}
	}

	if maskHas(mask, "versionTemplate", "versionTemplate.protectionLevel", "version_template.protection_level") {
		prot, ok, present := vt.ProtectionLevel.normalize(protectionLevelNames)
		if present && !ok {
			invalidArg(w, "invalid versionTemplate.protectionLevel")
			return false
		}

		if present {
			patch.ProtectionLevel = &prot
		}
	}

	return true
}

func (h *Handler) updatePrimaryVersion(w http.ResponseWriter, r *http.Request, rt *route) {
	var req updatePrimaryVersionRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	ck, err := h.kms.UpdateCryptoKeyPrimaryVersion(rt.ref(), req.CryptoKeyVersionID)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toCryptoKeyJSON(&ck))
}

// --- versions ---

func (h *Handler) createVersion(w http.ResponseWriter, r *http.Request, rt *route) {
	var req createVersionRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	state := ""

	if s, ok, present := req.State.normalize(stateNames); present {
		if !ok || (s != kmsprov.StateEnabled && s != kmsprov.StateDisabled) {
			invalidArg(w, "state must be ENABLED or DISABLED on create")
			return
		}

		state = s
	}

	keyName, v, err := h.kms.CreateCryptoKeyVersion(rt.ref(), state)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(keyName, &v))
}

func (h *Handler) getVersion(w http.ResponseWriter, rt *route) {
	keyName, v, err := h.kms.GetCryptoKeyVersion(rt.ref())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(keyName, &v))
}

func (h *Handler) listVersions(w http.ResponseWriter, rt *route) {
	keyName, versions, err := h.kms.ListCryptoKeyVersions(rt.ref())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]versionJSON, 0, len(versions))
	for i := range versions {
		out = append(out, toVersionJSON(keyName, &versions[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listVersionsResponse{CryptoKeyVersions: out, TotalSize: len(out)})
}

func (h *Handler) patchVersion(w http.ResponseWriter, r *http.Request, rt *route) {
	var req patchVersionRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	state := ""

	if maskHas(r.URL.Query().Get("updateMask"), "state") {
		if s, ok, present := req.State.normalize(stateNames); present {
			if !ok {
				invalidArg(w, "invalid state")
				return
			}

			state = s
		}
	}

	keyName, v, err := h.kms.UpdateCryptoKeyVersion(rt.ref(), state)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(keyName, &v))
}

func (h *Handler) destroyVersion(w http.ResponseWriter, rt *route) {
	keyName, v, err := h.kms.DestroyCryptoKeyVersion(rt.ref())
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(keyName, &v))
}

func (h *Handler) restoreVersion(w http.ResponseWriter, rt *route) {
	keyName, v, err := h.kms.RestoreCryptoKeyVersion(rt.ref())
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(keyName, &v))
}
