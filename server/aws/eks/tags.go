package eks

import (
	"context"
	"net/http"
)

// clusterTagger is the AWS-specific EKS tagging surface, asserted against the
// provider (not part of the portable EKS driver).
type clusterTagger interface {
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, keys []string) error
	ListResourceTags(ctx context.Context, arn string) (map[string]string, error)
}

// serveTags handles the EKS tagging API at /tags/{resourceArn}:
// POST=TagResource, DELETE=UntagResource (?tagKeys=...), GET=ListTagsForResource.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, arn string) {
	tagger, ok := h.eks.(clusterTagger)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "tagging not supported")
		return
	}

	switch r.Method {
	case http.MethodPost:
		var req struct {
			Tags map[string]string `json:"tags"`
		}

		if !decodeJSON(w, r, &req) {
			return
		}

		if err := tagger.TagResource(r.Context(), arn, req.Tags); err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, struct{}{})
	case http.MethodDelete:
		if err := tagger.UntagResource(r.Context(), arn, r.URL.Query()["tagKeys"]); err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, struct{}{})
	case http.MethodGet:
		tags, err := tagger.ListResourceTags(r.Context(), arn)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, map[string]any{"tags": tags})
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}
