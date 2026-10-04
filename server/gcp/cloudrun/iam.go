package cloudrun

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcpiam"
)

// existsFn reports whether the IAM target resource exists (returning the
// driver's NotFound error otherwise), so IAM verbs 404 like real GCP.
type existsFn func(r *http.Request, name string) error

// serveIam routes the IAM verbs (getIamPolicy / setIamPolicy /
// testIamPermissions) for a job or service through the shared resource IAM
// store, so etags follow the real read-modify-write contract. CloudEmu does
// not enforce IAM. key is the resource's canonical name used both to 404 and
// to index the stored policy.
func (h *Handler) serveIam(w http.ResponseWriter, r *http.Request, p *crPath, key string, exists existsFn) {
	if err := exists(r, p.name); err != nil {
		writeErr(w, err)
		return
	}

	gcpiam.Serve(w, r, p.action, key, h.iam)
}
