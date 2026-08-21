package vault

import (
	"encoding/base64"
	"net/http"

	vaultprovider "github.com/stackshy/cloudemu/v2/providers/oci/vault"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// Work request operations the secret mutations record.
const (
	opCreateSecret                 = "CREATE_SECRET"
	opUpdateSecret                 = "UPDATE_SECRET"
	opScheduleSecretDeletion       = "SCHEDULE_SECRET_DELETION"
	opCancelSecretDeletion         = "CANCEL_SECRET_DELETION"
	opChangeSecretCompartment      = "CHANGE_SECRET_COMPARTMENT"
	opScheduleSecretVersionDeleted = "SCHEDULE_SECRET_VERSION_DELETION"
	opCancelSecretVersionDeleted   = "CANCEL_SECRET_VERSION_DELETION"
)

// Resource kinds a secret work request names.
const (
	entitySecret        = "secret"
	entitySecretVersion = "secretversion"
)

// serveSecrets routes the secret collection, its versions and their actions.
func (h *Handler) serveSecrets(w http.ResponseWriter, r *http.Request, rt route) {
	switch {
	case rt.count() == lenCollection:
		h.serveSecretCollection(w, r)
	case rt.count() == lenResource && rt.seg(1) != segActions:
		h.serveSecretResource(w, r, rt.seg(1))
	case rt.count() == lenSub && rt.seg(1) == segActions:
		h.secretCollectionAction(w, r, rt.seg(2))
	case isAction(rt):
		h.secretAction(w, r, rt.seg(1), rt.seg(3))
	case rt.count() == lenSub && rt.seg(2) == segVersions:
		h.listSecretVersions(w, r, rt.seg(1))
	case rt.count() == lenSubID && rt.seg(2) == segVersions:
		h.getSecretVersion(w, r, rt.seg(1), rt.seg(3))
	case rt.count() == lenSubAction && rt.seg(2) == segVersions && rt.seg(4) == segActions:
		h.secretVersionAction(w, r, rt.seg(1), rt.seg(3), rt.seg(5))
	default:
		notFound(w, r)
	}
}

func (h *Handler) serveSecretCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createSecret(w, r)
	case http.MethodGet:
		h.listSecrets(w, r)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) serveSecretResource(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getSecret(w, r, id)
	case http.MethodPut:
		h.updateSecret(w, r, id)
	default:
		methodNotAllowed(w, r)
	}
}

// secretCollectionAction serves the actions addressing the collection rather
// than one secret; getByName is the only one.
func (h *Handler) secretCollectionAction(w http.ResponseWriter, r *http.Request, action string) {
	if action != actionGetByName {
		unknownAction(w, r, action)
		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	info, err := h.extras.GetOCISecretByName(vaultIDOf(r), r.URL.Query().Get("secretName"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toSecretResponse(info))
}

// secretAction serves the mutations OCI models as actions on a secret.
func (h *Handler) secretAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	switch action {
	case actionScheduleDeletion:
		h.scheduleSecretDeletion(w, r, id)
	case actionCancelDeletion:
		h.cancelSecretDeletion(w, r, id)
	case actionChangeCompartment:
		h.changeSecretCompartment(w, r, id)
	default:
		unknownAction(w, r, action)
	}
}

func (h *Handler) createSecret(w http.ResponseWriter, r *http.Request) {
	var req secretRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if !h.validSecretRequest(w, r, req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if req.SecretContent == nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "secretContent is required")
		return
	}

	content, ok := decodeContent(w, r, req.SecretContent)
	if !ok {
		return
	}

	info, err := h.extras.CreateOCISecret(vaultprovider.SecretSpec{
		CompartmentID: req.CompartmentID,
		VaultID:       req.VaultID,
		KeyID:         req.KeyID,
		Name:          req.SecretName,
		Description:   deref(req.Description),
		Content:       content,
		ContentName:   req.SecretContent.Name,
		FreeformTags:  req.FreeformTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, opCreateSecret, info.CompartmentID, entitySecret, workrequest.ActionCreated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, toSecretResponse(info))
}

// validSecretRequest refuses the secret inputs CloudEmu does not model.
func (h *Handler) validSecretRequest(w http.ResponseWriter, r *http.Request, req secretRequest) bool {
	return rejectDefinedTags(w, r, req.DefinedTags) &&
		rejectUnmodelled(w, r, "secretRules", len(req.SecretRules) > 0) &&
		rejectUnmodelled(w, r, "rotationConfig", req.RotationConfig != nil) &&
		rejectUnmodelled(w, r, "secretGenerationContext", req.SecretGeneration != nil)
}

func (h *Handler) listSecrets(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.extras.ListOCISecrets(compartmentID, vaultIDOf(r), r.URL.Query().Get("name"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	writeList(w, r, infos, toSecretResponse)
}

func (h *Handler) getSecret(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.extras.GetOCISecret(id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toSecretResponse(info))
}

func (h *Handler) updateSecret(w http.ResponseWriter, r *http.Request, id string) {
	var req secretRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if !h.validSecretRequest(w, r, req) {
		return
	}

	upd := vaultprovider.SecretUpdate{
		Description:          req.Description,
		KeyID:                req.KeyID,
		CurrentVersionNumber: req.CurrentVersionNumber,
		FreeformTags:         req.FreeformTags,
	}

	if req.SecretContent != nil {
		content, ok := decodeContent(w, r, req.SecretContent)
		if !ok {
			return
		}

		upd.Content = content
		upd.ContentName = req.SecretContent.Name
		upd.Stage = req.SecretContent.Stage
		upd.ContentGiven = true
	}

	info, err := h.extras.UpdateOCISecret(id, upd)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, opUpdateSecret, info.CompartmentID, entitySecret, workrequest.ActionUpdated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, toSecretResponse(info))
}

// scheduleSecretDeletion moves a secret to PENDING_DELETION. OCI answers with
// no body here, only the headers a waiter needs.
func (h *Handler) scheduleSecretDeletion(w http.ResponseWriter, r *http.Request, id string) {
	at, ok := decodeDeletion(w, r)
	if !ok {
		return
	}

	info, err := h.extras.ScheduleOCISecretDeletion(id, at)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, opScheduleSecretDeletion, info.CompartmentID, entitySecret, workrequest.ActionUpdated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) cancelSecretDeletion(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.extras.CancelOCISecretDeletion(id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, opCancelSecretDeletion, info.CompartmentID, entitySecret, workrequest.ActionUpdated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) changeSecretCompartment(w http.ResponseWriter, r *http.Request, id string) {
	if h.work == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented, "work requests are not configured")
		return
	}

	compartmentID, ok := decodeCompartmentMove(w, r)
	if !ok {
		return
	}

	if err := h.extras.ChangeSecretCompartment(id, compartmentID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, opChangeSecretCompartment, compartmentID, entitySecret, workrequest.ActionUpdated, id)
	ocirest.WriteJSON(w, r, http.StatusAccepted, nil)
}

// listSecretVersions lists a secret's versions. Real OCI takes no
// compartmentId here — the secret in the path already names one.
func (h *Handler) listSecretVersions(w http.ResponseWriter, r *http.Request, secretID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	infos, err := h.extras.ListOCISecretVersions(secretID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	writeList(w, r, infos, toSecretVersionResponse)
}

func (h *Handler) getSecretVersion(w http.ResponseWriter, r *http.Request, secretID, raw string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	number, ok := versionNumber(w, r, raw)
	if !ok {
		return
	}

	info, err := h.extras.GetOCISecretVersion(secretID, number)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toSecretVersionResponse(info))
}

// secretVersionAction serves the scheduled deletion of a single version.
func (h *Handler) secretVersionAction(w http.ResponseWriter, r *http.Request, secretID, raw, action string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	number, ok := versionNumber(w, r, raw)
	if !ok {
		return
	}

	switch action {
	case actionScheduleDeletion:
		h.scheduleSecretVersionDeletion(w, r, secretID, number)
	case actionCancelDeletion:
		h.cancelSecretVersionDeletion(w, r, secretID, number)
	default:
		unknownAction(w, r, action)
	}
}

func (h *Handler) scheduleSecretVersionDeletion(w http.ResponseWriter, r *http.Request, secretID string, n int64) {
	at, ok := decodeDeletion(w, r)
	if !ok {
		return
	}

	info, err := h.extras.ScheduleSecretVersionDeletion(secretID, n, at)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.acceptVersion(w, opScheduleSecretVersionDeleted, secretID, info.VersionNumber)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) cancelSecretVersionDeletion(w http.ResponseWriter, r *http.Request, secretID string, n int64) {
	info, err := h.extras.CancelSecretVersionDeletion(secretID, n)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.acceptVersion(w, opCancelSecretVersionDeleted, secretID, info.VersionNumber)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// acceptVersion records a work request against a secret version, which has no
// OCID of its own: OCI identifies it by the secret and the version number.
func (h *Handler) acceptVersion(w http.ResponseWriter, operation, secretID string, n int64) {
	h.accept(w, operation, h.extras.SecretCompartment(secretID), entitySecretVersion,
		workrequest.ActionUpdated, versionIdentifier(secretID, n))
}

// decodeContent reads a secretContent payload, which OCI base64-encodes.
func decodeContent(w http.ResponseWriter, r *http.Request, sc *secretContent) ([]byte, bool) {
	if sc.ContentType != "" && sc.ContentType != contentTypeBase64 {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"secretContent.contentType "+sc.ContentType+" is not supported; only "+contentTypeBase64+" is")

		return nil, false
	}

	content, err := base64.StdEncoding.DecodeString(sc.Content)
	if err != nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"secretContent.content is not valid base64")

		return nil, false
	}

	return content, true
}

func toSecretResponse(info *vaultprovider.SecretInfo) secretResponse {
	return secretResponse{
		ID:                   info.ID,
		CompartmentID:        info.CompartmentID,
		VaultID:              info.VaultID,
		KeyID:                info.KeyID,
		SecretName:           info.Name,
		Description:          info.Description,
		LifecycleState:       info.LifecycleState,
		CurrentVersionNumber: info.CurrentVersionNumber,
		TimeCreated:          info.TimeCreated,
		TimeOfDeletion:       info.TimeOfDeletion,
		FreeformTags:         info.FreeformTags,
		DefinedTags:          definedTags{},
	}
}

func toSecretVersionResponse(info *vaultprovider.SecretVersionInfo) secretVersionResponse {
	return secretVersionResponse{
		SecretID:       info.SecretID,
		VersionNumber:  info.VersionNumber,
		Name:           info.Name,
		Stages:         info.Stages,
		TimeCreated:    info.TimeCreated,
		TimeOfDeletion: info.TimeOfDeletion,
	}
}
