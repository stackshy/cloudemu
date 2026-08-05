package ecr

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// repositoryTagger is the AWS-specific ECR tagging surface, asserted against
// the provider (not part of the portable ContainerRegistry driver).
type repositoryTagger interface {
	TagRepository(ctx context.Context, name string, tags map[string]string) error
	UntagRepository(ctx context.Context, name string, keys []string) error
	ListRepositoryTags(ctx context.Context, name string) (map[string]string, error)
}

type ecrTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// repoFromARN resolves an ECR ResourceArn
// ("arn:aws:ecr:<region>:<account>:repository/<name>") to the bare repository
// name. A non-ARN value is returned unchanged.
func repoFromARN(arn string) string {
	const marker = ":repository/"

	if i := strings.LastIndex(arn, marker); i >= 0 {
		return arn[i+len(marker):]
	}

	return arn
}

func (h *Handler) repoTagger() (repositoryTagger, bool) {
	t, ok := h.registry.(repositoryTagger)

	return t, ok
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.repoTagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceArn string   `json:"resourceArn"`
		Tags        []ecrTag `json:"tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		tags[t.Key] = t.Value
	}

	if err := tagger.TagRepository(r.Context(), repoFromARN(req.ResourceArn), tags); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.repoTagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceArn string   `json:"resourceArn"`
		TagKeys     []string `json:"tagKeys"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := tagger.UntagRepository(r.Context(), repoFromARN(req.ResourceArn), req.TagKeys); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.repoTagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceArn string `json:"resourceArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, err := tagger.ListRepositoryTags(r.Context(), repoFromARN(req.ResourceArn))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]ecrTag, 0, len(tags))
	for k, v := range tags {
		out = append(out, ecrTag{Key: k, Value: v})
	}

	wire.WriteJSON(w, map[string]any{"tags": out})
}
