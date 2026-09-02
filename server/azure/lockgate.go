package azure

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/azure/locks"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// preDispatch is the shape of a server pre-dispatch hook: it may rewrite the
// request and, by returning proceed=false after writing a response, stop
// dispatch.
type preDispatch = func(http.ResponseWriter, *http.Request) (*http.Request, bool)

// newLockGate builds the management-lock enforcement pre-dispatch hook. It is
// the single chokepoint that applies Azure lock semantics to every resource
// type at once, before handler matching — no per-handler edits.
//
// Classification (see the lock design):
//   - Non-control-plane paths (not under /subscriptions/) are data plane and
//     exempt — locks are control-plane only.
//   - GET/HEAD (reads) are always allowed.
//   - The locks API itself is self-exempt (h.Matches), so a caller can always
//     create, read or delete a lock — including to remove a lock and unlock a
//     scope it would otherwise cover.
//   - Any other mutating method (DELETE/PUT/PATCH/POST) is checked against the
//     covering locks; a blocked request gets a 409 ScopeLocked and stops.
//
// The gate reads only method and path, never the body, so it composes cleanly
// with the auth gate in either order.
func newLockGate(h *locks.Handler) preDispatch {
	return func(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
		if !isControlPlane(r.URL.Path) {
			return r, true
		}

		switch r.Method {
		case http.MethodGet, http.MethodHead:
			return r, true
		}

		// The locks management API is always allowed, so a scope can be unlocked.
		if h.Matches(r) {
			return r, true
		}

		lockedScope, _, blocked := h.Enforce(r.URL.Path, r.Method)
		if !blocked {
			return r, true
		}

		azurearm.WriteError(w, http.StatusConflict, "ScopeLocked", fmt.Sprintf(
			"The scope '%s' cannot perform '%s' operation because the following scope(s) are locked: "+
				"'%s'. Please remove the lock and try again.",
			r.URL.Path, operationNoun(r.Method), lockedScope))

		return r, false
	}
}

// isControlPlane reports whether a request path is an ARM control-plane path
// (under /subscriptions/). Data-plane URLs (blob/queue/table/Cosmos data) do
// not carry this prefix, giving the control-plane-only exemption for free.
func isControlPlane(urlPath string) bool {
	return strings.HasPrefix(strings.ToLower(urlPath), "/subscriptions/")
}

// RBAC-style operation nouns Azure uses in the ScopeLocked message.
const (
	opDelete = "delete"
	opAction = "action"
	opWrite  = "write"
)

// operationNoun maps an HTTP method to the RBAC-style operation noun Azure uses
// in the ScopeLocked message: delete, write (PUT/PATCH) or action (POST).
func operationNoun(method string) string {
	switch strings.ToUpper(method) {
	case http.MethodDelete:
		return opDelete
	case http.MethodPost:
		return opAction
	default:
		return opWrite
	}
}

// composePreDispatch chains pre-dispatch hooks left to right: each may rewrite
// the request (the next hook sees the rewrite) and any may stop dispatch. Nil
// hooks are skipped, so an absent auth gate adds nothing. Mirrors the AWS
// server's helper so the auth gate and the lock gate coexist on the single
// SetPreDispatch slot.
func composePreDispatch(hooks ...preDispatch) preDispatch {
	return func(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
		for _, hook := range hooks {
			if hook == nil {
				continue
			}

			var proceed bool
			if r, proceed = hook(w, r); !proceed {
				return r, false
			}
		}

		return r, true
	}
}
