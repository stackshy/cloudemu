package vault

import (
	"net/http"

	vaultprovider "github.com/stackshy/cloudemu/v2/providers/oci/vault"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// Work request operations the key mutations record.
const (
	opCreateKey            = "CREATE_KEY"
	opUpdateKey            = "UPDATE_KEY"
	opScheduleKeyDeletion  = "SCHEDULE_KEY_DELETION"
	opCancelKeyDeletion    = "CANCEL_KEY_DELETION"
	opChangeKeyCompartment = "CHANGE_KEY_COMPARTMENT"
	opCreateKeyVersion     = "CREATE_KEY_VERSION"
)

// Resource kinds a key work request names.
const (
	entityKey        = "key"
	entityKeyVersion = "keyversion"
)

// serveKeys routes the key collection and its key versions.
func (h *Handler) serveKeys(w http.ResponseWriter, r *http.Request, rt route) {
	switch {
	case rt.count() == lenCollection:
		h.serveKeyCollection(w, r)
	case rt.count() == lenResource && rt.seg(idxID) != segActions:
		h.serveKeyResource(w, r, rt.seg(idxID))
	case isAction(rt):
		h.keyAction(w, r, rt.seg(idxID), rt.seg(idxSubID))
	case rt.count() == lenSub && rt.seg(idxSub) == segKeyVersions:
		h.serveKeyVersionCollection(w, r, rt.seg(idxID))
	case rt.count() == lenSubID && rt.seg(idxSub) == segKeyVersions:
		h.getKeyVersion(w, r, rt.seg(idxID), rt.seg(idxSubID))
	default:
		notFound(w, r)
	}
}

func (h *Handler) serveKeyCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createKey(w, r)
	case http.MethodGet:
		h.listKeys(w, r)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) serveKeyResource(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getKey(w, r, id)
	case http.MethodPut:
		h.updateKey(w, r, id)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) serveKeyVersionCollection(w http.ResponseWriter, r *http.Request, keyID string) {
	switch r.Method {
	case http.MethodPost:
		h.createKeyVersion(w, r, keyID)
	case http.MethodGet:
		h.listKeyVersions(w, r, keyID)
	default:
		methodNotAllowed(w, r)
	}
}

// keyAction serves the mutations OCI models as actions on a key.
func (h *Handler) keyAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	switch action {
	case actionScheduleDeletion:
		h.scheduleKeyDeletion(w, r, id)
	case actionCancelDeletion:
		h.cancelKeyDeletion(w, r, id)
	case actionChangeCompartment:
		h.changeCompartment(w, r, id, opChangeKeyCompartment, entityKey, h.extras.ChangeKeyCompartment)
	default:
		unknownAction(w, r, action)
	}
}

func (h *Handler) createKey(w http.ResponseWriter, r *http.Request) {
	var req keyRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if !validKeyRequest(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if req.KeyShape == nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "keyShape is required")
		return
	}

	info, err := h.extras.CreateKey(&vaultprovider.KeySpec{
		CompartmentID:  req.CompartmentID,
		VaultID:        vaultIDOf(r),
		DisplayName:    deref(req.DisplayName),
		Shape:          toProviderShape(*req.KeyShape),
		ProtectionMode: req.ProtectionMode,
		FreeformTags:   req.FreeformTags,
	})

	h.writeKey(w, r, info, err, opCreateKey, workrequest.ActionCreated)
}

// validKeyRequest refuses the key inputs CloudEmu does not model.
func validKeyRequest(w http.ResponseWriter, r *http.Request, req *keyRequest) bool {
	return rejectDefinedTags(w, r, req.DefinedTags) &&
		rejectUnmodelled(w, r, "autoKeyRotationDetails", req.AutoKeyRotationDetails != nil) &&
		rejectUnmodelled(w, r, "externalKeyReference", req.ExternalKeyReference != nil) &&
		rejectUnmodelled(w, r, "desiredState", req.DesiredState != "")
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.extras.ListKeys(compartmentID, vaultIDOf(r))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	writeList(w, r, infos, toKeyResponse)
}

func (h *Handler) getKey(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.extras.GetKey(id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toKeyResponse(info))
}

func (h *Handler) updateKey(w http.ResponseWriter, r *http.Request, id string) {
	var req keyRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if !validKeyRequest(w, r, &req) {
		return
	}

	if req.KeyShape != nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"keyShape cannot be changed after a key is created")

		return
	}

	info, err := h.extras.UpdateKey(id, vaultprovider.Update{
		DisplayName:  req.DisplayName,
		FreeformTags: req.FreeformTags,
	})

	h.writeKey(w, r, info, err, opUpdateKey, workrequest.ActionUpdated)
}

func (h *Handler) scheduleKeyDeletion(w http.ResponseWriter, r *http.Request, id string) {
	at, ok := decodeDeletion(w, r)
	if !ok {
		return
	}

	info, err := h.extras.ScheduleKeyDeletion(id, at)

	h.writeKey(w, r, info, err, opScheduleKeyDeletion, workrequest.ActionUpdated)
}

func (h *Handler) cancelKeyDeletion(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.extras.CancelKeyDeletion(id)

	h.writeKey(w, r, info, err, opCancelKeyDeletion, workrequest.ActionUpdated)
}

// writeKey records the work request for a key mutation and writes the key it
// produced.
func (h *Handler) writeKey(
	w http.ResponseWriter, r *http.Request, info *vaultprovider.KeyInfo, err error, operation, actionType string,
) {
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, operation, info.CompartmentID, entityKey, actionType, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, toKeyResponse(info))
}

// createKeyVersion rotates a key. OCI has no separate rotate operation: a new
// key version becomes the key's current one.
func (h *Handler) createKeyVersion(w http.ResponseWriter, r *http.Request, keyID string) {
	info, err := h.extras.CreateKeyVersion(keyID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, opCreateKeyVersion, info.CompartmentID, entityKeyVersion, workrequest.ActionCreated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, toKeyVersionResponse(info))
}

// listKeyVersions lists a key's versions. Real OCI takes no compartmentId
// here — the key in the path already names one.
func (h *Handler) listKeyVersions(w http.ResponseWriter, r *http.Request, keyID string) {
	infos, err := h.extras.ListKeyVersions(keyID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	writeList(w, r, infos, toKeyVersionResponse)
}

func (h *Handler) getKeyVersion(w http.ResponseWriter, r *http.Request, keyID, versionID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	info, err := h.extras.GetKeyVersion(keyID, versionID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toKeyVersionResponse(info))
}

// vaultIDOf reads the vaultId query parameter. The KMS management endpoint is
// per-vault in real OCI, so the vault is addressed by host there; CloudEmu
// serves every vault from one host and takes it as a parameter instead.
func vaultIDOf(r *http.Request) string {
	return r.URL.Query().Get("vaultId")
}

func toProviderShape(shape keyShape) vaultprovider.KeyShape {
	return vaultprovider.KeyShape{
		Algorithm: shape.Algorithm,
		Length:    shape.Length,
		CurveID:   shape.CurveID,
	}
}

func toKeyResponse(info *vaultprovider.KeyInfo) keyResponse {
	return keyResponse{
		ID:            info.ID,
		CompartmentID: info.CompartmentID,
		VaultID:       info.VaultID,
		DisplayName:   info.DisplayName,
		KeyShape: keyShape{
			Algorithm: info.Shape.Algorithm,
			Length:    info.Shape.Length,
			CurveID:   info.Shape.CurveID,
		},
		ProtectionMode:    info.ProtectionMode,
		LifecycleState:    info.LifecycleState,
		CurrentKeyVersion: info.CurrentKeyVersion,
		TimeCreated:       info.TimeCreated,
		TimeOfDeletion:    info.TimeOfDeletion,
		FreeformTags:      info.FreeformTags,
		DefinedTags:       definedTags{},
	}
}

func toKeyVersionResponse(info *vaultprovider.KeyVersionInfo) keyVersionResponse {
	return keyVersionResponse{
		ID:             info.ID,
		KeyID:          info.KeyID,
		VaultID:        info.VaultID,
		CompartmentID:  info.CompartmentID,
		LifecycleState: info.LifecycleState,
		TimeCreated:    info.TimeCreated,
	}
}
