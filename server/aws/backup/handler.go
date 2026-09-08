// Package backup implements the AWS Backup control-plane API (restJson1,
// backup-2018-11-15) as a server.Handler. Point the real
// aws-sdk-go-v2/service/backup client (or the `aws backup` CLI, or the
// aws_backup_vault / aws_backup_plan / aws_backup_selection Terraform
// resources) at a Server registered with this handler and the vault, plan,
// selection, Vault Lock, access-policy, notification and tagging operations
// work end-to-end against an in-memory driver.
//
// AWS Backup routes by HTTP verb + path (e.g. PUT /backup-vaults/{name},
// POST /backup/plans, GET /backup/plans/{id}/versions); there is no
// X-Amz-Target header and no version prefix. Matches claims the /backup-vaults
// and /backup/plans trees — distinctive to AWS Backup — and the shared /tags
// and /untag paths only when the ARN names a Backup (:backup:) resource, so it
// runs before the S3 catch-all and never shadows a sibling service's tag
// operations.
//
// This is a control-plane-only surface: there is NO backup-job / recovery-point
// data plane (StartBackupJob, StartRestoreJob and recovery points are out of
// scope).
package backup

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

// Path roots at the service root.
const (
	rootVaults = "backup-vaults"
	rootBackup = "backup"
	rootTags   = "tags"
	rootUntag  = "untag"
)

// segPlans/segSelections/segVersions are sub-path segments below /backup.
const (
	segPlans      = "plans"
	segSelections = "selections"
	segVersions   = "versions"
)

// Vault sub-resource segments.
const (
	subAccessPolicy  = "access-policy"
	subNotifications = "notification-configuration"
	subVaultLock     = "vault-lock"
)

// arnMarker scopes the shared /tags and /untag roots to AWS Backup ARNs.
const arnMarker = ":backup:"

// Handler serves AWS Backup requests against a driver.
type Handler struct {
	backup driver.Backup
}

// New returns an AWS Backup handler backed by d.
func New(d driver.Backup) *Handler {
	return &Handler{backup: d}
}

// Matches claims the AWS Backup path shapes. The /backup-vaults and
// /backup/plans trees are distinctive to Backup. The /tags and /untag roots are
// shared with other restJson1 services, so they are claimed only for Backup
// ARNs; a non-Backup ARN falls through.
func (*Handler) Matches(r *http.Request) bool {
	segs := splitPath(r.URL.Path)
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootVaults:
		return true
	case rootBackup:
		return len(segs) >= 2 && segs[1] == segPlans
	case rootTags, rootUntag:
		return len(segs) >= 2 && strings.Contains(strings.Join(segs[1:], "/"), arnMarker)
	default:
		return false
	}
}

// ServeHTTP dispatches an AWS Backup request on its path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(r.URL.Path)
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootVaults:
		h.serveVaults(w, r, segs[1:])
	case rootBackup:
		h.serveBackup(w, r, segs[1:])
	case rootTags:
		h.serveTags(w, r, segs[1:])
	case rootUntag:
		h.serveUntag(w, r, segs[1:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveBackup routes the /backup/plans tree.
func (h *Handler) serveBackup(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 || rest[0] != segPlans {
		notFoundPath(w, r.URL.Path)

		return
	}

	h.servePlans(w, r, rest[1:])
}

// splitPath splits a decoded URL path into its non-empty segments. AWS Backup
// resource ARNs use only colons (no slashes) in their resource part, so an ARN
// on the /tags or /untag path arrives as a single segment.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	return strings.Split(p, "/")
}

// pageFromQuery reads maxResults/nextToken from the query string.
func pageFromQuery(r *http.Request) driver.Page {
	q := r.URL.Query()

	return driver.Page{
		NextToken:  q.Get("nextToken"),
		MaxResults: atoiDefault(q.Get("maxResults")),
	}
}

// atoiDefault parses s as an int32, returning 0 when s is empty or invalid.
func atoiDefault(s string) int32 {
	if s == "" {
		return 0
	}

	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}

	return int32(n) //nolint:gosec // bounded by request query length; overflow not reachable in practice.
}
