package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// paginateARNs applies ECS maxResults/nextToken to an already stably-ordered
// ARN slice (the driver's list ops return SortedValues, so offset tokens stay
// meaningful across calls). It writes an InvalidParameterException and returns
// ok=false on a malformed token.
func paginateARNs(
	w http.ResponseWriter, arns []string, maxResults int, nextToken string,
) (items []string, next string, ok bool) {
	page, err := pagination.Paginate(arns, nextToken, maxResults)
	if err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", "invalid nextToken: "+err.Error())

		return nil, "", false
	}

	return page.Items, page.NextPageToken, true
}

// listResponse builds an ECS list response body under arnKey, adding nextToken
// only when there are more results (real ECS omits it on the last page).
func listResponse(arnKey string, arns []string, nextToken string) map[string]any {
	out := map[string]any{arnKey: arns}
	if nextToken != "" {
		out["nextToken"] = nextToken
	}

	return out
}
