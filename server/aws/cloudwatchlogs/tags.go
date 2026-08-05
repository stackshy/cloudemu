package cloudwatchlogs

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// logGroupTagger is the AWS-specific log-group tagging surface, asserted
// against the provider (not part of the portable Logging driver).
type logGroupTagger interface {
	TagLogGroup(ctx context.Context, name string, tags map[string]string) error
	UntagLogGroup(ctx context.Context, name string, keys []string) error
	ListLogGroupTags(ctx context.Context, name string) (map[string]string, error)
}

// logGroupName resolves either a log-group ARN (modern TagResource) or a bare
// name (legacy TagLogGroup) to the name the driver keys on.
func logGroupName(resourceArn, name string) string {
	if name != "" {
		return name
	}

	const marker = ":log-group:"

	if i := strings.LastIndex(resourceArn, marker); i >= 0 {
		return strings.TrimSuffix(resourceArn[i+len(marker):], ":*")
	}

	return resourceArn
}

func (h *Handler) logGroupTags() (logGroupTagger, bool) {
	t, ok := h.logs.(logGroupTagger)

	return t, ok
}

func (h *Handler) tagLogGroup(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.logGroupTags()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceArn  string            `json:"resourceArn"`
		LogGroupName string            `json:"logGroupName"`
		Tags         map[string]string `json:"tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name := logGroupName(req.ResourceArn, req.LogGroupName)

	if err := tagger.TagLogGroup(r.Context(), name, req.Tags); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) untagLogGroup(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.logGroupTags()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceArn  string   `json:"resourceArn"`
		LogGroupName string   `json:"logGroupName"`
		TagKeys      []string `json:"tagKeys"`
		Tags         []string `json:"tags"` // legacy UntagLogGroup uses "tags"
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name := logGroupName(req.ResourceArn, req.LogGroupName)

	keys := req.TagKeys
	if len(keys) == 0 {
		keys = req.Tags
	}

	if err := tagger.UntagLogGroup(r.Context(), name, keys); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.logGroupTags()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceArn  string `json:"resourceArn"`
		LogGroupName string `json:"logGroupName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name := logGroupName(req.ResourceArn, req.LogGroupName)

	tags, err := tagger.ListLogGroupTags(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"tags": tags})
}
