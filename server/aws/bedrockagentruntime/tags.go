package bedrockagentruntime

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// ownsTagsPath reports whether p is /tags/{arn} for a bedrock session ARN,
// the only resource the runtime tags. Other bedrock ARNs belong to the
// bedrock-agent control plane; every other ARN to its own service.
func ownsTagsPath(p string) bool {
	arn, ok := strings.CutPrefix(p, prefixTags)
	if !ok {
		return false
	}

	parts := strings.SplitN(arn, ":", 6) //nolint:mnd // the 6 fields of an ARN.

	return len(parts) == 6 && parts[0] == "arn" && parts[2] == signingService && strings.HasPrefix(parts[5], "session/")
}

// serveTags handles TagResource (POST), UntagResource (DELETE ?tagKeys=) and
// ListTagsForResource (GET) for sessions.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request) {
	withCapability(h, w, "TagResource", func(c driver.Tagger) {
		arn := strings.TrimPrefix(r.URL.Path, prefixTags)

		switch r.Method {
		case http.MethodPost:
			var in struct {
				Tags map[string]string `json:"tags"`
			}

			if !decodeJSON(w, r, &in) {
				return
			}

			writeTagResult(w, nil, c.TagResource(r.Context(), arn, in.Tags))
		case http.MethodDelete:
			writeTagResult(w, nil, c.UntagResource(r.Context(), arn, r.URL.Query()["tagKeys"]))
		case http.MethodGet:
			tags, err := c.ListTagsForResource(r.Context(), arn)
			writeTagResult(w, map[string]any{"tags": tags}, err)
		default:
			methodNotAllowed(w)
		}
	})
}

func writeTagResult(w http.ResponseWriter, body any, err error) {
	if err != nil {
		writeErr(w, err)

		return
	}

	if body == nil {
		body = struct{}{}
	}

	writeJSON(w, body)
}
