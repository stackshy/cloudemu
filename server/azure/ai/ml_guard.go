package ai

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// mlChildShapeOK reports whether a workspace child path (rest starts at
// "workspaces") has a shape its collection routes. Below {coll}/{name} only
// compute actions, endpoint deployments, job cancel and asset versions exist.
func mlChildShapeOK(rest []string) bool {
	if len(rest) <= mlLenChild {
		return true
	}

	coll, sub := rest[2], rest[4]

	switch {
	case coll == collComputes:
		return len(rest) == mlLenSub
	case coll == collOnlineEndpoints || coll == collBatchEndpoints:
		return sub == collDeployments && len(rest) <= mlLenSub+1
	case coll == collJobs:
		return sub == subCancel && len(rest) == mlLenSub
	case assetTypes[coll]:
		return sub == subVersions && len(rest) <= mlLenSub+1
	default:
		return false
	}
}

// rejectMLChild answers an ML path no route serves: 501 for an extension
// resource, otherwise 404 InvalidResourceType. The parent is never touched.
func rejectMLChild(w http.ResponseWriter, r *http.Request) {
	rp, _ := azurearm.ParsePath(r.URL.Path)
	azurearm.RejectChild(w, r, &rp)
}
