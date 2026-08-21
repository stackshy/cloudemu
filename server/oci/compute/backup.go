package compute

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// backupOps serves /volumeBackups and /bootVolumeBackups, which are the same
// resource over two collections: boot says which one the caller reached for.
func (h *Handler) backupOps(boot bool) crud {
	return crud{
		create: func(w http.ResponseWriter, r *http.Request) { h.createBackup(w, r, boot) },
		list:   func(w http.ResponseWriter, r *http.Request) { h.listBackups(w, r, boot) },
		get:    func(w http.ResponseWriter, r *http.Request, id string) { h.getBackup(w, r, id, boot) },
		update: h.updateBackup,
		remove: h.deleteBackup,
	}
}

func (h *Handler) createBackup(w http.ResponseWriter, r *http.Request, boot bool) {
	var req backupRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	volumeID := req.VolumeID
	if boot {
		volumeID = req.BootVolumeID
	}

	if volumeID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			backupSourceField(boot)+" is required")

		return
	}

	info, err := h.extras.CreateVolumeBackup(r.Context(), volumeID, req.DisplayName, req.Type, boot,
		freeformOf(req.FreeformTags))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	compartmentID := h.compartmentOf(volumeID)
	h.place(info.ID, compartmentID)
	h.accept(w, "CREATE_VOLUME_BACKUP", compartmentID, "volumebackup", workrequest.ActionCreated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toBackupResponse(info))
}

func (h *Handler) listBackups(w http.ResponseWriter, r *http.Request, boot bool) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.compute.DescribeSnapshots(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]backupResponse, 0, len(infos))

	for i := range infos {
		if !h.inCompartment(infos[i].ID, compartmentID) || !h.isBootBackup(infos[i].ID) == boot {
			continue
		}

		out = append(out, h.toBackupResponse(&infos[i]))
	}

	writePage(w, r, out)
}

func (h *Handler) getBackup(w http.ResponseWriter, r *http.Request, id string, boot bool) {
	info, err := h.findBackup(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if h.isBootBackup(id) != boot {
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound,
			"backup "+id+" is not a "+backupCollection(boot)+" member")

		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toBackupResponse(info))
}

func (h *Handler) updateBackup(w http.ResponseWriter, r *http.Request, id string) {
	var req backupRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.extras.UpdateVolumeBackup(r.Context(), id,
		displayNameUpdate(req.DisplayName, req.FreeformTags))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toBackupResponse(info))
}

func (h *Handler) deleteBackup(w http.ResponseWriter, r *http.Request, id string) {
	compartmentID := h.compartmentOf(id)

	if err := h.compute.DeleteSnapshot(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "DELETE_VOLUME_BACKUP", compartmentID, "volumebackup", workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// findBackup reads one backup, reporting OCI's not-found for an unknown OCID.
func (h *Handler) findBackup(ctx context.Context, id string) (*computedriver.SnapshotInfo, error) {
	infos, err := h.compute.DescribeSnapshots(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "volume backup %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) isBootBackup(id string) bool {
	_, boot, _ := h.extras.VolumeBackupDetails(id)

	return boot
}

func (h *Handler) toBackupResponse(info *computedriver.SnapshotInfo) backupResponse {
	backupType, boot, _ := h.extras.VolumeBackupDetails(info.ID)

	out := backupResponse{
		ID:              info.ID,
		CompartmentID:   h.compartmentOf(info.ID),
		DisplayName:     info.Description,
		Type:            backupType,
		SizeInGBs:       info.Size,
		UniqueSizeInGBs: info.Size,
		SourceType:      "MANUAL",
		LifecycleState:  storageLifecycle(info.State),
		TimeCreated:     info.CreatedAt,
		FreeformTags:    freeformOf(info.Tags),
		DefinedTags:     definedTags{},
	}

	if boot {
		out.BootVolumeID = info.VolumeID
	} else {
		out.VolumeID = info.VolumeID
	}

	return out
}

func backupSourceField(boot bool) string {
	if boot {
		return "bootVolumeId"
	}

	return "volumeId"
}

func backupCollection(boot bool) string {
	if boot {
		return segBootVolumeBackups
	}

	return segVolumeBackups
}
