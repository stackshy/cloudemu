package spanner

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/sharedpath"
)

// Cloud SQL Admin v1 serves the same /v1/projects/{p}/instances[/{i}[/databases]]
// paths. When both are mounted, Spanner claims a list only when it is hinted,
// paged the Spanner way, or Spanner owns an instance in the project, and an item
// only when it owns the instance or the path shape exists only on Spanner.

const pageSizeParam = "pageSize" // Cloud SQL pages with maxResults

// SetSharedPath turns on the shared instances rules. Off (the default),
// Spanner claims every instance list, as a standalone server must.
func (h *Handler) SetSharedPath() { h.shared = true }

// matchesSharedList decides GET /v1/projects/{p}/instances.
func (h *Handler) matchesSharedList(r *http.Request, project string) bool {
	if !h.shared || sharedpath.Is(r, sharedpath.Spanner) || r.URL.Query().Has(pageSizeParam) {
		return true
	}

	all, err := h.db.ListInstances(r.Context(), project)

	return err == nil && len(all) > 0
}

// spannerOnlySub reports whether the path below instances/{i} exists only on
// Spanner. parts is the path below /v1/projects/.
func spannerOnlySub(r *http.Request, parts []string) bool {
	const idxInstance, idxSub, idxDBSub = 2, 3, 5

	if strings.Contains(parts[idxInstance], ":") {
		return true // instances/{i}:verb
	}

	if len(parts) <= idxSub {
		return false
	}

	switch parts[idxSub] {
	case segOperations, "backups", "backups:copy", "backupOperations", "databaseOperations",
		"instancePartitions", "instancePartitionOperations", "databases:restore":
		return true
	case segDatabases:
	default:
		return false
	}

	if len(parts) > idxDBSub {
		switch strings.SplitN(parts[idxDBSub], ":", 2)[0] { //nolint:mnd // seg[:verb]
		case segDdl, "sessions", segOperations, "databaseRoles", "backupSchedules", "scans":
			return true
		}
	}

	return len(parts) == idxSub+1 && r.Method == http.MethodPost && bodyHasCreateStatement(r)
}

// bodyHasCreateStatement reports whether a databases POST body is a Spanner
// CreateDatabaseRequest. The body is restored.
func bodyHasCreateStatement(r *http.Request) bool {
	if r.Body == nil {
		return false
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	if err != nil {
		return false
	}

	var probe struct {
		CreateStatement string `json:"createStatement"`
	}

	return json.Unmarshal(raw, &probe) == nil && probe.CreateStatement != ""
}
