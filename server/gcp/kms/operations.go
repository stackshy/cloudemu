package kms

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	algorithmUnspecified       = "CRYPTO_KEY_VERSION_ALGORITHM_UNSPECIFIED"
	protectionLevelUnspecified = "PROTECTION_LEVEL_UNSPECIFIED"
	trueValue                  = "true"
	// algorithmSymmetric is the default versionTemplate.algorithm real Cloud KMS
	// assigns a symmetric ENCRYPT_DECRYPT key when the caller omits it.
	algorithmSymmetric = "GOOGLE_SYMMETRIC_ENCRYPTION"
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

	kr, err := h.store.createKeyRing(rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, kr)
}

func (h *Handler) getKeyRing(w http.ResponseWriter, rt *route) {
	kr, err := h.store.getKeyRing(rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, kr)
}

func (h *Handler) listKeyRings(w http.ResponseWriter, rt *route) {
	gcprest.WriteJSON(w, http.StatusOK, h.store.listKeyRings(rt))
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

	ck, err := h.store.createCryptoKey(rt, &cfg, skip)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, ck)
}

// buildCryptoKeyConfig validates and normalizes a create body. On any
// validation failure it writes the error and returns ok=false.
func buildCryptoKeyConfig(w http.ResponseWriter, id string, req *createCryptoKeyRequest) (cryptoKeyConfig, bool) {
	purpose, ok, present := req.Purpose.normalize(purposeNames)
	if !present || !ok || purpose == purposeUnspecified {
		invalidArg(w, "purpose is required and must be a valid CryptoKeyPurpose")
		return cryptoKeyConfig{}, false
	}

	algo, prot, ok := normalizeVersionTemplate(w, req.VersionTemplate, purpose)
	if !ok {
		return cryptoKeyConfig{}, false
	}

	dsd := req.DestroyScheduledDuration
	if dsd == "" {
		dsd = defaultDestroyScheduledDuration
	} else if _, ok := parseDurationSeconds(dsd); !ok {
		invalidArg(w, "invalid destroyScheduledDuration")
		return cryptoKeyConfig{}, false
	}

	if req.RotationPeriod != "" {
		if _, ok := parseDurationSeconds(req.RotationPeriod); !ok {
			invalidArg(w, "invalid rotationPeriod")
			return cryptoKeyConfig{}, false
		}
	}

	return cryptoKeyConfig{
		id:                       id,
		purpose:                  purpose,
		rotationPeriod:           req.RotationPeriod,
		nextRotationTime:         req.NextRotationTime,
		protectionLevel:          prot,
		algorithm:                algo,
		labels:                   req.Labels,
		importOnly:               req.ImportOnly,
		destroyScheduledDuration: dsd,
		cryptoKeyBackend:         req.CryptoKeyBackend,
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
		if purpose == purposeEncryptDecrypt {
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
	ck, err := h.store.getCryptoKey(rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, ck)
}

func (h *Handler) listCryptoKeys(w http.ResponseWriter, rt *route) {
	resp, err := h.store.listCryptoKeys(rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, resp)
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

	ck, err := h.store.patchCryptoKey(rt, &patch)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, ck)
}

// buildCryptoKeyPatch turns a masked patch body into a normalized cryptoKeyPatch.
func buildCryptoKeyPatch(w http.ResponseWriter, mask string, req *createCryptoKeyRequest) (cryptoKeyPatch, bool) {
	patch := cryptoKeyPatch{}

	if maskHas(mask, "labels") {
		patch.labels, patch.setLabels = req.Labels, true
	}

	if maskHas(mask, "rotationPeriod", "rotation_period") {
		patch.rotationPeriod, patch.setRotationPeriod = req.RotationPeriod, true
	}

	if maskHas(mask, "nextRotationTime", "next_rotation_time") {
		patch.nextRotationTime, patch.setNextRotationTime = req.NextRotationTime, true
	}

	if req.VersionTemplate != nil {
		if !applyVersionTemplatePatch(w, mask, req.VersionTemplate, &patch) {
			return cryptoKeyPatch{}, false
		}
	}

	return patch, true
}

func applyVersionTemplatePatch(w http.ResponseWriter, mask string, vt *versionTemplateJSON, patch *cryptoKeyPatch) bool {
	if maskHas(mask, "versionTemplate", "versionTemplate.algorithm", "version_template.algorithm") {
		algo, ok, present := vt.Algorithm.normalize(algorithmNames)
		if present && (!ok || algo == algorithmUnspecified) {
			invalidArg(w, "invalid versionTemplate.algorithm")
			return false
		}

		if present {
			patch.algorithm, patch.setAlgorithm = algo, true
		}
	}

	if maskHas(mask, "versionTemplate", "versionTemplate.protectionLevel", "version_template.protection_level") {
		prot, ok, present := vt.ProtectionLevel.normalize(protectionLevelNames)
		if present && !ok {
			invalidArg(w, "invalid versionTemplate.protectionLevel")
			return false
		}

		if present {
			patch.protectionLevel, patch.setProtectionLevel = prot, true
		}
	}

	return true
}

func (h *Handler) updatePrimaryVersion(w http.ResponseWriter, r *http.Request, rt *route) {
	var req updatePrimaryVersionRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	ck, err := h.store.updatePrimaryVersion(rt, req.CryptoKeyVersionID)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, ck)
}

// --- versions ---

func (h *Handler) createVersion(w http.ResponseWriter, r *http.Request, rt *route) {
	var req createVersionRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	state := ""

	if s, ok, present := req.State.normalize(stateNames); present {
		if !ok || (s != stateEnabled && s != stateDisabled) {
			invalidArg(w, "state must be ENABLED or DISABLED on create")
			return
		}

		state = s
	}

	v, err := h.store.createVersion(rt, state)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) getVersion(w http.ResponseWriter, rt *route) {
	v, err := h.store.getVersion(rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listVersions(w http.ResponseWriter, rt *route) {
	resp, err := h.store.listVersions(rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, resp)
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

	v, err := h.store.patchVersion(rt, state)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) destroyVersion(w http.ResponseWriter, rt *route) {
	v, err := h.store.destroyVersion(rt)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) restoreVersion(w http.ResponseWriter, rt *route) {
	v, err := h.store.restoreVersion(rt)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, v)
}

// --- IAM ---

func (h *Handler) getIamPolicy(w http.ResponseWriter, rt *route) {
	pol, err := h.store.getIAMPolicy(rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, pol)
}

func (h *Handler) setIamPolicy(w http.ResponseWriter, r *http.Request, rt *route) {
	var req setIamPolicyRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	pol, err := h.store.setIAMPolicy(rt, req.Policy)
	if err != nil {
		if cerrors.IsFailedPrecondition(err) {
			gcprest.WriteError(w, http.StatusConflict, "aborted", cerrors.Message(err))
			return
		}

		gcprest.WriteCErr(w, err)

		return
	}

	gcprest.WriteJSON(w, http.StatusOK, pol)
}

func (*Handler) testIamPermissions(w http.ResponseWriter, r *http.Request) {
	var req testIamPermissionsRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	// The emulator has no request principal, so every requested permission is
	// reported as held (the stance the iam / resourcemanager handlers take too).
	gcprest.WriteJSON(w, http.StatusOK, testIamPermissionsResponse(req))
}
