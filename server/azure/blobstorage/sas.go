package blobstorage

import (
	"net/http"
	"strings"
	"time"
)

// SAS (service shared-access-signature) query parameter names. See
// https://learn.microsoft.com/en-us/rest/api/storageservices/create-service-sas.
const (
	sasSig    = "sig" // signature — its presence marks a SAS-authenticated request.
	sasPerm   = "sp"  // signed permissions (r/w/d/l/a/c/…).
	sasExpiry = "se"  // signed expiry time.
	sasStart  = "st"  // signed start time (optional).
)

// Azure SAS permission characters relevant to blob data-plane ops.
const (
	sasPermRead   = "r"
	sasPermAdd    = "a"
	sasPermCreate = "c"
	sasPermWrite  = "w"
	sasPermDelete = "d"
	sasPermList   = "l"
)

// enforceSAS applies SAS permission + validity-window scoping to a request that
// carries a SAS signature (a `sig` query param). cloudemu does not verify the
// SAS signature cryptographically — the whole wire layer accepts any
// credentials — but it honors the permission set (sp) and the validity window
// (st/se) so SAS-based least-privilege access control is testable: a read-only
// SAS can't delete or overwrite, and an expired SAS is rejected. It writes the
// Azure error and returns true when the request must be rejected.
func enforceSAS(w http.ResponseWriter, r *http.Request, blob string) bool {
	q := r.URL.Query()
	if q.Get(sasSig) == "" {
		return false // not a SAS-authenticated request; nothing to enforce.
	}

	now := time.Now().UTC()

	if st := q.Get(sasStart); st != "" {
		if t, ok := parseSASTime(st); ok && now.Before(t) {
			writeError(w, http.StatusForbidden, "AuthenticationFailed",
				"Server failed to authenticate the request: the SAS start time (st) is in the future.")
			return true
		}
	}

	if se := q.Get(sasExpiry); se != "" {
		if t, ok := parseSASTime(se); ok && !now.Before(t) {
			writeError(w, http.StatusForbidden, "AuthenticationFailed",
				"Server failed to authenticate the request: the SAS expiry time (se) is in the past.")
			return true
		}
	}

	if allowed := sasPermissionsFor(r, blob); len(allowed) > 0 && !hasAnySASPermission(q.Get(sasPerm), allowed) {
		writeError(w, http.StatusForbidden, "AuthorizationPermissionMismatch",
			"This request is not authorized to perform this operation using this permission.")
		return true
	}

	return false
}

// sasPermissionsFor returns the SAS permission characters that would authorize
// the request, any one of which is sufficient. An empty result means the op is
// not permission-gated by a service SAS here (e.g. account/container-level
// management), and is left to run.
func sasPermissionsFor(r *http.Request, blob string) []string {
	comp := r.URL.Query().Get("comp")

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if comp == compList {
			return []string{sasPermList}
		}

		if blob == "" {
			return nil // container property reads are not gated here.
		}

		return []string{sasPermRead}
	case http.MethodDelete:
		if blob == "" {
			return nil // container delete is account/management-level.
		}

		return []string{sasPermDelete}
	case http.MethodPut:
		if blob == "" {
			return nil // container create/config is account/management-level.
		}

		if comp == compAppendBlock {
			return []string{sasPermAdd, sasPermWrite}
		}

		return []string{sasPermWrite, sasPermCreate}
	default:
		return nil
	}
}

// hasAnySASPermission reports whether the signed-permission string sp grants at
// least one of the acceptable permission characters.
func hasAnySASPermission(sp string, allowed []string) bool {
	for _, p := range allowed {
		if strings.Contains(sp, p) {
			return true
		}
	}

	return false
}

// parseSASTime parses an Azure SAS st/se timestamp, which may carry date-only,
// second, or sub-second precision. It reports ok=false for an unparseable value
// so a malformed time is treated leniently (not enforced) rather than blocking.
func parseSASTime(v string) (t time.Time, ok bool) {
	for _, layout := range []string{
		"2006-01-02T15:04:05.0000000Z07:00",
		time.RFC3339,
		"2006-01-02T15:04Z07:00",
		time.DateOnly,
	} {
		if parsed, err := time.Parse(layout, v); err == nil {
			return parsed.UTC(), true
		}
	}

	return time.Time{}, false
}
