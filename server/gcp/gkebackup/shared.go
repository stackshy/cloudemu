package gkebackup

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/gcp/sharedpath"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// Backup and DR serves the same .../locations/{l}/backupPlans paths. When both
// are mounted, Backup for GKE yields a Backup-and-DR-shaped create and a
// request hinted for Backup and DR, and keeps everything else.

// SetSharedPath turns on the shared backupPlans rules.
func (h *Handler) SetSharedPath() { h.shared = true }

// yieldsShared reports whether a backupPlans request belongs to Backup and DR.
func (h *Handler) yieldsShared(r *http.Request, rt route) bool {
	if !h.shared || rt.resource != backupPlansColl || sharedpath.Is(r, sharedpath.GKEBackup) {
		return false
	}

	if sharedpath.Yield(r, sharedpath.GKEBackup, sharedpath.BackupDR) {
		return true
	}

	return rt.name == "" && r.Method == http.MethodPost && bodyLooksLikeBackupDR(r)
}

// bodyLooksLikeBackupDR reports a Backup and DR BackupPlan body: it names a
// backupVault, backupRules or resourceType and no GKE cluster. The body is
// restored.
func bodyLooksLikeBackupDR(r *http.Request) bool {
	if r.Body == nil {
		return false
	}

	raw, err := wire.PeekBody(r, maxBodyBytes)

	if err != nil {
		return false
	}

	var probe struct {
		Cluster      string          `json:"cluster"`
		BackupVault  string          `json:"backupVault"`
		BackupRules  json.RawMessage `json:"backupRules"`
		ResourceType string          `json:"resourceType"`
	}

	if json.Unmarshal(raw, &probe) != nil || probe.Cluster != "" {
		return false
	}

	return probe.BackupVault != "" || len(probe.BackupRules) > 0 || probe.ResourceType != ""
}
