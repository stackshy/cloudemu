package bedrockagent

import (
	"net/http"
	"strings"
)

// prefixTags roots the tagging API: /tags/{resourceArn}. Other REST services
// (EKS, GuardDuty, VPC Lattice, ...) share the path, so it is only claimed for
// bedrock-agent ARNs.
const prefixTags = "/tags/"

// taggedResourceTypes are the bedrock ARN resource types bedrock-agent tags.
// Other bedrock ARNs (runtime sessions, guardrails, ...) fall through.
//
//nolint:gochecknoglobals // fixed lookup table of claimed ARN resource types.
var taggedResourceTypes = []string{"agent/", "agent-alias/", "knowledge-base/", "flow/", "prompt/"}

// ownsTagsPath reports whether p is /tags/{arn} for a bedrock-agent ARN.
func ownsTagsPath(p string) bool {
	arn, ok := strings.CutPrefix(p, prefixTags)
	if !ok || !strings.HasPrefix(arn, "arn:") {
		return false
	}

	parts := strings.SplitN(arn, ":", arnFields)
	if len(parts) != arnFields || parts[2] != "bedrock" {
		return false
	}

	for _, t := range taggedResourceTypes {
		if strings.HasPrefix(parts[arnFields-1], t) {
			return true
		}
	}

	return false
}

// arnFields is the number of colon-separated fields in an ARN.
const arnFields = 6

type tagResourceRequest struct {
	Tags map[string]string `json:"tags"`
}

type listTagsResponse struct {
	Tags map[string]string `json:"tags"`
}

// serveTags handles TagResource (POST), UntagResource (DELETE ?tagKeys=) and
// ListTagsForResource (GET).
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request) {
	arn := strings.TrimPrefix(r.URL.Path, prefixTags)

	switch r.Method {
	case http.MethodPost:
		var in tagResourceRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if err := h.agent.TagResource(r.Context(), arn, in.Tags); err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, struct{}{})
	case http.MethodDelete:
		keys := r.URL.Query()["tagKeys"]

		if err := h.agent.UntagResource(r.Context(), arn, keys); err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, struct{}{})
	case http.MethodGet:
		tags, err := h.agent.ListTagsForResource(r.Context(), arn)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, listTagsResponse{Tags: tags})
	default:
		methodNotAllowed(w)
	}
}
