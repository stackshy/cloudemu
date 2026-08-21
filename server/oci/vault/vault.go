package vault

import (
	"net/http"

	vaultprovider "github.com/stackshy/cloudemu/v2/providers/oci/vault"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// Work request operations the vault mutations record.
const (
	opCreateVault           = "CREATE_VAULT"
	opUpdateVault           = "UPDATE_VAULT"
	opScheduleVaultDeletion = "SCHEDULE_VAULT_DELETION"
	opCancelVaultDeletion   = "CANCEL_VAULT_DELETION"
	opChangeVaultCompartment = "CHANGE_VAULT_COMPARTMENT"
)

// entityVault is the resource kind a vault work request names.
const entityVault = "vault"

// serveVaults routes the vault collection.
func (h *Handler) serveVaults(w http.ResponseWriter, r *http.Request, rt route) {
	switch {
	case rt.count() == lenCollection:
		h.serveVaultCollection(w, r)
	case rt.count() == lenResource && rt.seg(1) != segActions:
		h.serveVaultResource(w, r, rt.seg(1))
	case isAction(rt):
		h.vaultAction(w, r, rt.seg(1), rt.seg(3))
	default:
		notFound(w, r)
	}
}

func (h *Handler) serveVaultCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createVault(w, r)
	case http.MethodGet:
		h.listVaults(w, r)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) serveVaultResource(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getVault(w, r, id)
	case http.MethodPut:
		h.updateVault(w, r, id)
	default:
		methodNotAllowed(w, r)
	}
}

// vaultAction serves the mutations OCI models as actions on a vault. All three
// are asynchronous in real OCI.
func (h *Handler) vaultAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	switch action {
	case actionScheduleDeletion:
		h.scheduleVaultDeletion(w, r, id)
	case actionCancelDeletion:
		h.cancelVaultDeletion(w, r, id)
	case actionChangeCompartment:
		h.changeVaultCompartment(w, r, id)
	default:
		unknownAction(w, r, action)
	}
}

func (h *Handler) createVault(w http.ResponseWriter, r *http.Request) {
	var req vaultRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if !h.validVaultRequest(w, r, req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	info, err := h.extras.CreateVault(vaultprovider.VaultSpec{
		CompartmentID: req.CompartmentID,
		DisplayName:   deref(req.DisplayName),
		VaultType:     req.VaultType,
		FreeformTags:  req.FreeformTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, opCreateVault, info.CompartmentID, entityVault, workrequest.ActionCreated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, toVaultResponse(info))
}

// validVaultRequest refuses the vault inputs CloudEmu does not model.
func (h *Handler) validVaultRequest(w http.ResponseWriter, r *http.Request, req vaultRequest) bool {
	return rejectDefinedTags(w, r, req.DefinedTags) &&
		rejectUnmodelled(w, r, "restoreFromFile", req.RestoreFromFile != nil) &&
		rejectUnmodelled(w, r, "restoreFromObjectStore", req.RestoreFromObjectStore != nil) &&
		rejectUnmodelled(w, r, "externalKeyManagerMetadata", req.ExternalKeyManagerMetadata != nil)
}

func (h *Handler) listVaults(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.extras.ListVaults(compartmentID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	writeList(w, r, infos, toVaultResponse)
}

func (h *Handler) getVault(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.extras.GetVault(id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toVaultResponse(info))
}

func (h *Handler) updateVault(w http.ResponseWriter, r *http.Request, id string) {
	var req vaultRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if !h.validVaultRequest(w, r, req) {
		return
	}

	info, err := h.extras.UpdateVault(id, vaultprovider.Update{
		DisplayName:  req.DisplayName,
		FreeformTags: req.FreeformTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, opUpdateVault, info.CompartmentID, entityVault, workrequest.ActionUpdated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, toVaultResponse(info))
}

func (h *Handler) scheduleVaultDeletion(w http.ResponseWriter, r *http.Request, id string) {
	at, ok := decodeDeletion(w, r)
	if !ok {
		return
	}

	info, err := h.extras.ScheduleVaultDeletion(id, at)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, opScheduleVaultDeletion, info.CompartmentID, entityVault, workrequest.ActionUpdated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, toVaultResponse(info))
}

func (h *Handler) cancelVaultDeletion(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.extras.CancelVaultDeletion(id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, opCancelVaultDeletion, info.CompartmentID, entityVault, workrequest.ActionUpdated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, toVaultResponse(info))
}

func (h *Handler) changeVaultCompartment(w http.ResponseWriter, r *http.Request, id string) {
	if h.work == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented, "work requests are not configured")
		return
	}

	compartmentID, ok := decodeCompartmentMove(w, r)
	if !ok {
		return
	}

	if err := h.extras.ChangeVaultCompartment(id, compartmentID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, opChangeVaultCompartment, compartmentID, entityVault, workrequest.ActionUpdated, id)
	ocirest.WriteJSON(w, r, http.StatusAccepted, nil)
}

func toVaultResponse(info *vaultprovider.VaultInfo) vaultResponse {
	return vaultResponse{
		ID:                 info.ID,
		CompartmentID:      info.CompartmentID,
		DisplayName:        info.DisplayName,
		VaultType:          info.VaultType,
		CryptoEndpoint:     info.CryptoEndpoint,
		ManagementEndpoint: info.ManagementEndpoint,
		LifecycleState:     info.LifecycleState,
		TimeCreated:        info.TimeCreated,
		TimeOfDeletion:     info.TimeOfDeletion,
		FreeformTags:       info.FreeformTags,
		DefinedTags:        definedTags{},
	}
}

// deref reads an optional string field, which is absent rather than empty when
// an update leaves it alone.
func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
