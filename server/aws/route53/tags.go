package route53

import (
	"context"
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// resourceTagger is the AWS-specific Route 53 tagging surface, asserted against
// the provider (not part of the portable DNS driver).
type resourceTagger interface {
	ChangeResourceTags(ctx context.Context, resourceID string, add map[string]string, remove []string) error
	ListResourceTags(ctx context.Context, resourceID string) (map[string]string, error)
}

type r53Tag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type changeTagsRequest struct {
	XMLName       xml.Name `xml:"ChangeTagsForResourceRequest"`
	AddTags       []r53Tag `xml:"AddTags>Tag"`
	RemoveTagKeys []string `xml:"RemoveTagKeys>Key"`
}

type resourceTagSetXML struct {
	ResourceType string   `xml:"ResourceType"`
	ResourceID   string   `xml:"ResourceId"`
	Tags         []r53Tag `xml:"Tags>Tag"`
}

type listTagsForResourceResponse struct {
	XMLName        xml.Name          `xml:"ListTagsForResourceResponse"`
	ResourceTagSet resourceTagSetXML `xml:"ResourceTagSet"`
}

type changeTagsForResourceResponse struct {
	XMLName xml.Name `xml:"ChangeTagsForResourceResponse"`
}

// serveTags runs ChangeTagsForResource (POST) and ListTagsForResource (GET) on
// /2013-04-01/tags/{ResourceType}/{ResourceId}.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) {
	tagger, ok := h.dns.(resourceTagger)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidInput", "tagging not supported")
		return
	}

	if !h.taggedResourceExists(w, r, a) {
		return
	}

	if op == opListTagsForResource {
		h.listTags(w, r, tagger, a)
		return
	}

	var req changeTagsRequest
	if !decodeXML(w, r, &req) {
		return
	}

	add := make(map[string]string, len(req.AddTags))
	for _, t := range req.AddTags {
		add[t.Key] = t.Value
	}

	if err := tagger.ChangeResourceTags(r.Context(), a.id, add, req.RemoveTagKeys); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, changeTagsForResourceResponse{})
}

func (*Handler) listTags(w http.ResponseWriter, r *http.Request, tagger resourceTagger, a *opArgs) {
	tags, err := tagger.ListResourceTags(r.Context(), a.id)
	if err != nil {
		writeErr(w, err)
		return
	}

	set := resourceTagSetXML{ResourceType: a.tagType, ResourceID: a.id}
	for k, v := range tags {
		set.Tags = append(set.Tags, r53Tag{Key: k, Value: v})
	}

	wire.WriteXML(w, http.StatusOK, listTagsForResourceResponse{ResourceTagSet: set})
}

// taggedResourceExists reports whether the hosted zone or health check a
// tagging request names exists, and writes NoSuchHostedZone or
// NoSuchHealthCheck when it does not. The tag store is keyed by id alone, so
// without this a request naming one resource type would tag a resource of the
// other type, or an id that names nothing.
func (h *Handler) taggedResourceExists(w http.ResponseWriter, r *http.Request, a *opArgs) bool {
	if a.tagType == tagTypeHealthCheck {
		if _, err := h.dns.GetHealthCheck(r.Context(), a.id); err != nil {
			writeHealthCheckErr(w, err)
			return false
		}

		return true
	}

	if _, err := h.dns.GetZone(r.Context(), a.id); err != nil {
		writeErr(w, err)
		return false
	}

	return true
}

// writeTagsFailure answers a tagging request classify cannot name: no
// resource id, or a method other than GET and POST.
func (h *Handler) writeTagsFailure(w http.ResponseWriter, a *opArgs) {
	if _, ok := h.dns.(resourceTagger); !ok {
		writeError(w, http.StatusNotImplemented, "InvalidInput", "tagging not supported")
		return
	}

	if a.id == "" {
		writeError(w, http.StatusBadRequest, "InvalidInput", "resource id is required")
		return
	}

	writeMethodNotAllowed(w)
}
