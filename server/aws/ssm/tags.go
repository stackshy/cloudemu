package ssm

import (
	"context"
	"net/http"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ssmnative "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// parameterTagger is the AWS-specific parameter-tagging surface. It's not part
// of the portable ParameterStore driver, so the handler type-asserts for it.
type parameterTagger interface {
	TagParameter(ctx context.Context, name string, tags map[string]string) error
	UntagParameter(ctx context.Context, name string, keys []string) error
	ListParameterTags(ctx context.Context, name string) (map[string]string, error)
}

// resourceTagger tags one SSM resource type.
type resourceTagger interface {
	tag(ctx context.Context, id string, tags map[string]string) error
	untag(ctx context.Context, id string, keys []string) error
	list(ctx context.Context, id string) (map[string]string, error)
}

type paramTags struct{ t parameterTagger }

func (p paramTags) tag(ctx context.Context, id string, tags map[string]string) error {
	return p.t.TagParameter(ctx, id, tags)
}

func (p paramTags) untag(ctx context.Context, id string, keys []string) error {
	return p.t.UntagParameter(ctx, id, keys)
}

func (p paramTags) list(ctx context.Context, id string) (map[string]string, error) {
	return p.t.ListParameterTags(ctx, id)
}

type documentTags struct{ d ssmnative.Documents }

func (p documentTags) tag(ctx context.Context, id string, tags map[string]string) error {
	return p.d.TagDocument(ctx, id, tags)
}

func (p documentTags) untag(ctx context.Context, id string, keys []string) error {
	return p.d.UntagDocument(ctx, id, keys)
}

func (p documentTags) list(ctx context.Context, id string) (map[string]string, error) {
	return p.d.ListDocumentTags(ctx, id)
}

// noResources stands in for a valid resource type the emulator does not hold
// yet: every id is unknown, as it would be in an account with none of them.
type noResources struct{}

func (noResources) tag(context.Context, string, map[string]string) error { return errNoResource() }
func (noResources) untag(context.Context, string, []string) error        { return errNoResource() }
func (noResources) list(context.Context, string) (map[string]string, error) {
	return nil, errNoResource()
}

func errNoResource() error {
	return cerrors.New(cerrors.NotFound, "The resource ID isn't valid. Verify that you entered the correct ID and try again.")
}

// tagOps is the tagging family, routed by ResourceType.
func tagOps() map[string]handlerFunc {
	return map[string]handlerFunc{
		"AddTagsToResource":      (*Handler).addTagsToResource,
		"RemoveTagsFromResource": (*Handler).removeTagsFromResource,
		"ListTagsForResource":    (*Handler).listTagsForResource,
	}
}

// taggerFor routes a ResourceType to its tagger. A value outside the SSM
// ResourceTypeForTagging enum is InvalidResourceType.
func (h *Handler) taggerFor(w http.ResponseWriter, resourceType string) (resourceTagger, bool) {
	switch resourceType {
	case "Parameter":
		if t, ok := h.store.(parameterTagger); ok {
			return paramTags{t}, true
		}
	case "Document":
		if d, ok := h.store.(ssmnative.Documents); ok {
			return documentTags{d}, true
		}
	case "ManagedInstance", "MaintenanceWindow", "PatchBaseline", "OpsItem", "OpsMetadata",
		"Automation", "Association":
		return noResources{}, true
	default:
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidResourceType",
			"The resource type isn't valid. For example, if you are attempting to tag an EC2 instance, "+
				"the instance must be a registered managed node.")

		return nil, false
	}

	return noResources{}, true
}

type ssmTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// writeTagErr maps a tagging-operation error to its SSM JSON error response. A
// missing resource is InvalidResourceId here, distinct from writeErr's
// ParameterNotFound, which real SSM reserves for parameter-specific reads
// (GetParameter/DeleteParameter/…); the tagging API names its target by
// ResourceType+ResourceId and can point at any taggable SSM resource, not only
// a parameter.
func writeTagErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidResourceId", cerrors.Message(err))
		return
	}

	writeErr(w, err)
}

func (h *Handler) addTagsToResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceType string   `json:"ResourceType"`
		ResourceID   string   `json:"ResourceId"`
		Tags         []ssmTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tagger, ok := h.taggerFor(w, req.ResourceType)
	if !ok {
		return
	}

	tags := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		tags[t.Key] = t.Value
	}

	if err := tagger.tag(r.Context(), req.ResourceID, tags); err != nil {
		writeTagErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) removeTagsFromResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceType string   `json:"ResourceType"`
		ResourceID   string   `json:"ResourceId"`
		TagKeys      []string `json:"TagKeys"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tagger, ok := h.taggerFor(w, req.ResourceType)
	if !ok {
		return
	}

	if err := tagger.untag(r.Context(), req.ResourceID, req.TagKeys); err != nil {
		writeTagErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceType string `json:"ResourceType"`
		ResourceID   string `json:"ResourceId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tagger, ok := h.taggerFor(w, req.ResourceType)
	if !ok {
		return
	}

	tags, err := tagger.list(r.Context(), req.ResourceID)
	if err != nil {
		writeTagErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"TagList": tagList(tags)})
}

// tagList renders tags sorted by key. Go map order is random, and real SSM
// returns a stable order.
func tagList(tags map[string]string) []ssmTag {
	out := make([]ssmTag, 0, len(tags))
	for k, v := range tags {
		out = append(out, ssmTag{Key: k, Value: v})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })

	return out
}
