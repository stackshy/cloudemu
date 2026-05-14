package cloudsql

import (
	"net/http"
	"strconv"
	"time"

	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
)

// instanceFromBody decodes a Cloud SQL instance request and converts it to
// the portable driver shape. databaseVersion + region are top-level; tier and
// activationPolicy live under settings.
func instanceFromBody(body *sqlInstance) rdsdriver.InstanceConfig {
	cfg := rdsdriver.InstanceConfig{
		ID:               body.Name,
		Engine:           body.DatabaseVersion,
		AvailabilityZone: body.Region,
		MasterUsername:   body.RootPassword, // SDKs use rootPassword on insert.
	}

	if body.Settings != nil {
		cfg.InstanceClass = body.Settings.Tier
		cfg.AllocatedStorage = body.Settings.DataDiskSizeGb
		cfg.StorageType = body.Settings.DataDiskType
		cfg.Tags = body.Settings.UserLabels
	}

	return cfg
}

func (h *Handler) insertInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	var body sqlInstance
	if !decodeJSON(w, r, &body) {
		return
	}

	cfg := instanceFromBody(&body)

	inst, err := h.db.CreateInstance(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, doneOperationWithTarget(
		p.project, "create-"+inst.ID, "CREATE", "instances", inst.ID,
	))
}

func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	insts, err := h.db.DescribeInstances(r.Context(), nil)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := sqlInstanceList{Kind: "sql#instancesList", Items: make([]sqlInstance, 0, len(insts))}
	for i := range insts {
		out.Items = append(out.Items, toSQLInstance(&insts[i], p.project))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	insts, err := h.db.DescribeInstances(r.Context(), []string{p.name})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toSQLInstance(&insts[0], p.project))
}

//nolint:gocyclo // sequential field handling for activationPolicy + class + storage + version.
func (h *Handler) patchInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	var body sqlInstance
	if !decodeJSON(w, r, &body) {
		return
	}

	// Cloud SQL emulates start/stop by patching settings.activationPolicy.
	if body.Settings != nil && body.Settings.ActivationPolicy != "" {
		switch body.Settings.ActivationPolicy {
		case activationAlways:
			if err := h.db.StartInstance(r.Context(), p.name); err != nil {
				writeErr(w, err)
				return
			}
		case activationNever:
			if err := h.db.StopInstance(r.Context(), p.name); err != nil {
				writeErr(w, err)
				return
			}
		}
	}

	input := rdsdriver.ModifyInstanceInput{
		Tags: nil,
	}

	if body.Settings != nil {
		input.InstanceClass = body.Settings.Tier
		input.AllocatedStorage = body.Settings.DataDiskSizeGb
		input.Tags = body.Settings.UserLabels
	}

	if body.DatabaseVersion != "" {
		input.EngineVersion = body.DatabaseVersion
	}

	if _, err := h.db.ModifyInstance(r.Context(), p.name, input); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, doneOperationWithTarget(
		p.project, "patch-"+p.name, "UPDATE", "instances", p.name,
	))
}

func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	if err := h.db.DeleteInstance(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, doneOperationWithTarget(
		p.project, "delete-"+p.name, "DELETE", "instances", p.name,
	))
}

func (h *Handler) restartInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	if err := h.db.RebootInstance(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, doneOperationWithTarget(
		p.project, "restart-"+p.name, "RESTART", "instances", p.name,
	))
}

func (h *Handler) restoreInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	var body restoreBackupBody
	if !decodeJSON(w, r, &body) {
		return
	}

	// p.name is the *target* instance to restore into.
	input := rdsdriver.RestoreInstanceInput{
		NewInstanceID: p.name,
		SnapshotID:    body.RestoreBackupContext.BackupRunID,
	}

	if _, err := h.db.RestoreInstanceFromSnapshot(r.Context(), input); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, doneOperationWithTarget(
		p.project, "restore-"+p.name, "RESTORE_VOLUME", "instances", p.name,
	))
}

func (h *Handler) insertBackupRun(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	// SDK lets the caller omit ID and we generate one.
	id := strconv.FormatInt(time.Now().UnixNano(), 10)

	cfg := rdsdriver.SnapshotConfig{
		ID:         id,
		InstanceID: p.name,
	}

	snap, err := h.db.CreateSnapshot(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, doneOperationWithTarget(
		p.project, "create-backup-"+snap.ID, "BACKUP_VOLUME",
		"instances/"+p.name+"/backupRuns", snap.ID,
	))
}

func (h *Handler) listBackupRuns(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	snaps, err := h.db.DescribeSnapshots(r.Context(), nil, p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := backupRunList{Kind: "sql#backupRunsList", Items: make([]backupRun, 0, len(snaps))}
	for i := range snaps {
		out.Items = append(out.Items, toBackupRun(&snaps[i]))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getBackupRun(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	snaps, err := h.db.DescribeSnapshots(r.Context(), []string{p.subName}, p.name)
	if err != nil || len(snaps) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "backup run "+p.subName+" not found")
		return
	}

	writeJSON(w, http.StatusOK, toBackupRun(&snaps[0]))
}

func (h *Handler) deleteBackupRun(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	if err := h.db.DeleteSnapshot(r.Context(), p.subName); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, doneOperationWithTarget(
		p.project, "delete-backup-"+p.subName, "DELETE",
		"instances/"+p.name+"/backupRuns", p.subName,
	))
}
