package eventbridge

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// resourceTagger is the AWS-specific EventBridge tagging surface, asserted
// against the provider (not part of the portable EventBus driver).
type resourceTagger interface {
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, keys []string) error
	ListResourceTags(ctx context.Context, arn string) (map[string]string, error)
}

type ebTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func (h *Handler) tagger() (resourceTagger, bool) {
	t, ok := h.bus.(resourceTagger)

	return t, ok
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.tagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceARN string  `json:"ResourceARN"`
		Tags        []ebTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		tags[t.Key] = t.Value
	}

	if err := tagger.TagResource(r.Context(), req.ResourceARN, tags); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.tagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceARN string   `json:"ResourceARN"`
		TagKeys     []string `json:"TagKeys"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := tagger.UntagResource(r.Context(), req.ResourceARN, req.TagKeys); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.tagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceARN string `json:"ResourceARN"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, err := tagger.ListResourceTags(r.Context(), req.ResourceARN)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]ebTag, 0, len(tags))
	for k, v := range tags {
		out = append(out, ebTag{Key: k, Value: v})
	}

	wire.WriteJSON(w, map[string]any{"Tags": out})
}
