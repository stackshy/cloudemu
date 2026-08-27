package cloudsql

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// rootUserFor returns the fixed default administrator user name Cloud SQL
// assigns for a databaseVersion: "postgres" for PostgreSQL, "root" for MySQL,
// and "sqlserver" for SQL Server. This is the login a client connects with,
// paired with the rootPassword set on insert.
func rootUserFor(databaseVersion string) string {
	switch {
	case strings.HasPrefix(strings.ToUpper(databaseVersion), "MYSQL"):
		return "root"
	case strings.HasPrefix(strings.ToUpper(databaseVersion), "SQLSERVER"):
		return "sqlserver"
	default:
		return "postgres"
	}
}

// instanceFromBody decodes a Cloud SQL instance request and converts it to
// the portable driver shape. databaseVersion + region are top-level; tier and
// activationPolicy live under settings. The root user is fixed by engine and
// its password is the insert body's rootPassword, so a real engine can back the
// instance with a usable login.
func instanceFromBody(body *sqlInstance) rdsdriver.InstanceConfig {
	cfg := rdsdriver.InstanceConfig{
		ID:                 body.Name,
		Engine:             body.DatabaseVersion,
		AvailabilityZone:   body.Region,
		MasterUsername:     rootUserFor(body.DatabaseVersion),
		MasterUserPassword: body.RootPassword,
		MasterInstanceName: body.MasterInstanceName,
	}

	if body.Settings != nil {
		s := body.Settings
		cfg.InstanceClass = s.Tier
		cfg.AllocatedStorage = s.DataDiskSizeGb
		cfg.StorageType = s.DataDiskType
		cfg.Tags = s.UserLabels
		// availabilityType maps to the portable MultiAZ flag (REGIONAL -> true);
		// the three settings sub-objects are stored as opaque JSON so they
		// round-trip on the next Get.
		cfg.MultiAZ = strings.EqualFold(s.AvailabilityType, availabilityRegional)
		if s.DeletionProtectionEnabled != nil {
			cfg.DeletionProtection = *s.DeletionProtectionEnabled
		}
		cfg.GCPDatabaseFlags = string(s.DatabaseFlags)
		cfg.GCPBackupConfig = string(s.BackupConfiguration)
		cfg.GCPIPConfig = string(s.IPConfiguration)
	}

	return cfg
}

// modifyInputFromBody builds the driver ModifyInstanceInput for an instances
// update. With replace=false (PATCH) only fields present in the request body are
// set, so absent fields mean "no change" (Cloud SQL patch merges the request
// onto the current configuration). With replace=true (PUT) omitted settings
// revert to their defaults, matching Cloud SQL's full-resource update semantics.
func modifyInputFromBody(body *sqlInstance, replace bool) rdsdriver.ModifyInstanceInput {
	var input rdsdriver.ModifyInstanceInput

	if body.DatabaseVersion != "" {
		input.EngineVersion = body.DatabaseVersion
	}

	s := body.Settings
	if s == nil {
		if !replace {
			return input
		}

		s = &sqlSettings{}
	}

	input.InstanceClass = orDefaultStr(s.Tier, defaultTier, replace)
	input.AllocatedStorage = orDefaultInt(s.DataDiskSizeGb, defaultStorage, replace)
	input.StorageType = orDefaultStr(s.DataDiskType, defaultStorageType, replace)
	input.GCPDatabaseFlags = string(s.DatabaseFlags)
	input.GCPBackupConfig = string(s.BackupConfiguration)
	input.GCPIPConfig = string(s.IPConfiguration)

	// On replace an absent userLabels clears the labels (empty non-nil map),
	// while on merge an absent map leaves them unchanged (nil).
	switch {
	case s.UserLabels != nil:
		input.Tags = s.UserLabels
	case replace:
		input.Tags = map[string]string{}
	}

	if s.AvailabilityType != "" || replace {
		multiAZ := strings.EqualFold(s.AvailabilityType, availabilityRegional)
		input.MultiAZ = &multiAZ
	}

	if s.DeletionProtectionEnabled != nil {
		input.DeletionProtection = s.DeletionProtectionEnabled
	} else if replace {
		off := false
		input.DeletionProtection = &off
	}

	return input
}

// orDefaultStr returns v, or def when v is empty and replace is set (a PUT
// reverts an omitted field to its default; a PATCH leaves it as "no change").
func orDefaultStr(v, def string, replace bool) string {
	if v == "" && replace {
		return def
	}

	return v
}

func orDefaultInt(v, def int, replace bool) int {
	if v == 0 && replace {
		return def
	}

	return v
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

	h.completeOp(w,
		p.project, "create-"+inst.ID, "CREATE", "instances", inst.ID,
	)
}

func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	insts, err := h.db.DescribeInstances(r.Context(), nil)
	if err != nil {
		writeErr(w, err)
		return
	}

	items := make([]sqlInstance, 0, len(insts))
	for i := range insts {
		items = append(items, toSQLInstance(&insts[i], p.project))
	}

	q := r.URL.Query()
	items = filterInstances(items, q.Get("filter"))

	page, err := paginateInstances(items, q.Get("pageToken"), q.Get("maxResults"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid pageToken")
		return
	}

	writeJSON(w, http.StatusOK, sqlInstanceList{
		Kind:          "sql#instancesList",
		Items:         page.Items,
		NextPageToken: page.NextPageToken,
	})
}

func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	insts, err := h.db.DescribeInstances(r.Context(), []string{p.name})
	if err != nil {
		writeErr(w, err)
		return
	}

	if len(insts) == 0 {
		writeErr(w, cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", p.name))
		return
	}

	writeJSON(w, http.StatusOK, toSQLInstance(&insts[0], p.project))
}

// patchInstance handles both instances.patch (replace=false, merge) and
// instances.update / PUT (replace=true, full-resource replace where omitted
// settings revert to their defaults).
func (h *Handler) patchInstance(w http.ResponseWriter, r *http.Request, p *sqlPath, replace bool) {
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

	if _, err := h.db.ModifyInstance(r.Context(), p.name, modifyInputFromBody(&body, replace)); err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w,
		p.project, "patch-"+p.name, "UPDATE", "instances", p.name,
	)
}

func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	if err := h.db.DeleteInstance(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w,
		p.project, "delete-"+p.name, "DELETE", "instances", p.name,
	)
}

func (h *Handler) restartInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	if err := h.db.RebootInstance(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w,
		p.project, "restart-"+p.name, "RESTART", "instances", p.name,
	)
}

func (h *Handler) restoreInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	var body restoreBackupBody
	if !decodeJSON(w, r, &body) {
		return
	}

	// p.name is the *target* instance to restore into. Cloud SQL restores the
	// backup in place onto the existing instance rather than provisioning a new
	// one, so prefer the in-place BackupRestorer capability.
	restorer, ok := h.db.(rdsdriver.BackupRestorer)
	if !ok {
		writeErr(w, cerrors.New(cerrors.FailedPrecondition, "backend does not support restoreBackup"))
		return
	}

	if _, err := restorer.RestoreBackup(r.Context(), p.name, body.RestoreBackupContext.BackupRunID); err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w,
		p.project, "restore-"+p.name, "RESTORE_VOLUME", "instances", p.name,
	)
}

func (h *Handler) insertBackupRun(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	// The backup-run ID is generated deterministically by the mock (clock +
	// counter); leave it empty here.
	snap, err := h.db.CreateSnapshot(r.Context(), rdsdriver.SnapshotConfig{InstanceID: p.name})
	if err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w,
		p.project, "create-backup-"+snap.ID, "BACKUP_VOLUME",
		"instances/"+p.name+"/backupRuns", snap.ID,
	)
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
	if err != nil {
		// Surface the real backend error rather than masking every failure as
		// NOT_FOUND (which would send callers debugging the wrong subsystem).
		writeErr(w, err)
		return
	}

	if len(snaps) == 0 {
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

	h.completeOp(w,
		p.project, "delete-backup-"+p.subName, "DELETE",
		"instances/"+p.name+"/backupRuns", p.subName,
	)
}
