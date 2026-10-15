// Package resourcemanager implements the cloudresourcemanager.googleapis.com v1
// project and organization IAM policy surface as a server.Handler:
//
//	GET  /v1/projects/{project}
//	POST /v1/projects/{project}:{getIamPolicy|setIamPolicy|testIamPermissions}
//	POST /v1/organizations/{org}:{getIamPolicy|setIamPolicy|testIamPermissions}
//
// These are the endpoints Terraform's google_project_iam_* and
// google_organization_iam_* resources drive via read-modify-write with an
// etag, and the ones google.golang.org/api/cloudresourcemanager/v1 clients call.
//
// Projects and organizations have no portable driver: the emulator accepts
// every project and organization id, the same stance the iam handler takes for
// organization custom roles. Policies live in the shared resource IAM store
// under "projects/{p}" and "organizations/{o}", so etag, updateMask and
// conditional-binding rules match every other GCP resource policy.
package resourcemanager

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/gcp/resourceiam"
	"github.com/stackshy/cloudemu/v2/server/wire/gcpiam"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	projectsColl   = "projects"
	orgsColl       = "organizations"
	projectsPrefix = "/v1/" + projectsColl + "/"
	orgsPrefix     = "/v1/" + orgsColl + "/"

	// projectNumber is the number every project reports. GCS reports the same
	// one on its buckets, so a client that resolves the number (as Terraform's
	// google_service_networking_connection does) sees a consistent value.
	projectNumber = "123456789012"
)

// Handler serves the project and organization IAM policy verbs.
type Handler struct {
	iam gcpiam.Store
}

// New returns a handler with its own policy store. The assembled server swaps
// in the shared store with SetIAMStore.
func New() *Handler {
	return &Handler{iam: resourceiam.New()}
}

// SetIAMStore wires the shared resource IAM store.
func (h *Handler) SetIAMStore(s gcpiam.Store) { h.iam = s }

// split returns the collection and the single trailing segment of p, or
// ok=false for any other path.
func split(p string) (coll, tail string, ok bool) {
	switch {
	case strings.HasPrefix(p, projectsPrefix):
		coll, tail = projectsColl, strings.TrimPrefix(p, projectsPrefix)
	case strings.HasPrefix(p, orgsPrefix):
		coll, tail = orgsColl, strings.TrimPrefix(p, orgsPrefix)
	default:
		return "", "", false
	}

	return coll, tail, tail != "" && !strings.Contains(tail, "/")
}

// Matches claims GET /v1/projects/{p} and POSTs of an IAM verb on a single
// project or organization segment. The no-'/' guard keeps it disjoint from the
// iam handler (serviceAccounts, roles, organization roles), Firestore and
// every other /v1/projects/ handler, but it must register ahead of Firestore,
// whose permissive prefix would otherwise swallow the colon verb.
func (*Handler) Matches(r *http.Request) bool {
	coll, tail, ok := split(r.URL.Path)
	if !ok {
		return false
	}

	if r.Method == http.MethodGet && coll == projectsColl {
		return !strings.Contains(tail, ":")
	}

	_, verb := gcpiam.SplitVerb(tail)

	return r.Method == http.MethodPost && verb != ""
}

// ServeHTTP answers projects.get or hands the IAM verb to the shared helper.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	coll, tail, _ := split(r.URL.Path)

	id, verb := gcpiam.SplitVerb(tail)
	if verb == "" {
		// projects.get: every project id exists and is ACTIVE.
		gcprest.WriteJSON(w, http.StatusOK, map[string]any{
			"projectId":      id,
			"projectNumber":  projectNumber,
			"name":           id,
			"lifecycleState": "ACTIVE",
		})

		return
	}

	gcpiam.Serve(w, r, verb, coll+"/"+id, h.iam)
}
