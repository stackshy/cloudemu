package kms

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

func (h *Handler) createKey(w http.ResponseWriter, r *http.Request) {
	var req createKeyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	spec := req.KeySpec
	if spec == "" {
		spec = req.CustomerMasterKeySpec
	}

	md, err := h.kms.CreateKey(r.Context(), kmsdriver.CreateKeyInput{
		Description: req.Description,
		KeyUsage:    req.KeyUsage,
		KeySpec:     spec,
		Origin:      req.Origin,
		MultiRegion: req.MultiRegion,
		Policy:      req.Policy,
		Tags:        tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, keyMetadataResponse{KeyMetadata: metadataJSON(md)})
}

func (h *Handler) describeKey(w http.ResponseWriter, r *http.Request) {
	var req keyIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	md, err := h.kms.DescribeKey(r.Context(), req.KeyID)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, keyMetadataResponse{KeyMetadata: metadataJSON(md)})
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.kms.ListKeys(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	entries := make([]keyListEntry, 0, len(keys))
	for i := range keys {
		entries = append(entries, keyListEntry{KeyID: keys[i].KeyID, KeyArn: keys[i].ARN})
	}

	wire.WriteJSON(w, listKeysResponse{Keys: entries, Truncated: false})
}

func (h *Handler) enableKey(w http.ResponseWriter, r *http.Request) {
	var req keyIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.kms.EnableKey(r.Context(), req.KeyID); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) disableKey(w http.ResponseWriter, r *http.Request) {
	var req keyIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.kms.DisableKey(r.Context(), req.KeyID); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) updateKeyDescription(w http.ResponseWriter, r *http.Request) {
	var req updateKeyDescriptionRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.kms.UpdateKeyDescription(r.Context(), req.KeyID, req.Description); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) scheduleKeyDeletion(w http.ResponseWriter, r *http.Request) {
	var req scheduleKeyDeletionRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	md, err := h.kms.ScheduleKeyDeletion(r.Context(), req.KeyID, req.PendingWindowDays)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, scheduleKeyDeletionResponse{
		KeyID:        md.KeyID,
		DeletionDate: epochOrNil(md.DeletionDate),
		KeyState:     md.KeyState,
	})
}

func (h *Handler) cancelKeyDeletion(w http.ResponseWriter, r *http.Request) {
	var req keyIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	md, err := h.kms.CancelKeyDeletion(r.Context(), req.KeyID)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, cancelKeyDeletionResponse{KeyID: md.KeyID})
}

func (h *Handler) createAlias(w http.ResponseWriter, r *http.Request) {
	var req aliasRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.kms.CreateAlias(r.Context(), req.AliasName, req.TargetKeyID); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) updateAlias(w http.ResponseWriter, r *http.Request) {
	var req aliasRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.kms.UpdateAlias(r.Context(), req.AliasName, req.TargetKeyID); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) deleteAlias(w http.ResponseWriter, r *http.Request) {
	var req aliasRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.kms.DeleteAlias(r.Context(), req.AliasName); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) listAliases(w http.ResponseWriter, r *http.Request) {
	var req listAliasesRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	aliases, err := h.kms.ListAliases(r.Context(), req.KeyID)
	if err != nil {
		writeErr(w, err)
		return
	}

	entries := make([]aliasListEntry, 0, len(aliases))
	for i := range aliases {
		entries = append(entries, aliasListEntry{
			AliasName:       aliases[i].Name,
			AliasArn:        aliases[i].ARN,
			TargetKeyID:     aliases[i].TargetKeyID,
			CreationDate:    epochOrNil(aliases[i].CreationDate),
			LastUpdatedDate: epochOrNil(aliases[i].UpdatedDate),
		})
	}

	wire.WriteJSON(w, listAliasesResponse{Aliases: entries, Truncated: false})
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var req tagResourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.kms.TagResource(r.Context(), req.KeyID, tagsToMap(req.Tags)); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var req untagResourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.kms.UntagResource(r.Context(), req.KeyID, req.TagKeys); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) listResourceTags(w http.ResponseWriter, r *http.Request) {
	var req keyIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, err := h.kms.ListResourceTags(r.Context(), req.KeyID)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, listResourceTagsResponse{Tags: mapToTags(tags), Truncated: false})
}
