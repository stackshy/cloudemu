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

// serveTags runs the EKS tagging API at /tags/{resourceArn}:
// TagResource (POST), UntagResource (DELETE, ?tagKeys=...) and
// ListTagsForResource (GET).
//
// The ARN must be an EKS resource ARN of this account and region. The provider
// resolves a resource by the names in the ARN alone, so without this check an
// ARN of another account would act on the resource of the same name here, and
// a bare name would act on a cluster.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) {
	tagger, ok := h.eks.(clusterTagger)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "tagging not supported")
		return
	}

	if a.tag.kind == "" {
		writeError(w, http.StatusBadRequest, "BadRequestException", "Invalid ARN: "+a.tagARN)
		return
	}

	if a.tag.foreign(h.accountID, h.region) {
		writeError(w, http.StatusNotFound, "NotFoundException", "Resource not found: "+a.tagARN)
		return
	}

	arn := a.tagARN

	switch op {
	case opTagResource:
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
	case opUntagResource:
		if err := tagger.UntagResource(r.Context(), arn, r.URL.Query()["tagKeys"]); err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, struct{}{})
	default:
		tags, err := tagger.ListResourceTags(r.Context(), arn)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, map[string]any{"tags": tags})
	}
}
