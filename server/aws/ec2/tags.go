package ec2

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// computeTagger is the AWS-specific compute-resource tagging surface
// (instances/volumes/snapshots/images). It's not part of the portable Compute
// driver (Azure/GCP also implement it), so the handler type-asserts for it.
type computeTagger interface {
	TagResource(ctx context.Context, id string, tags map[string]string) error
	UntagResource(ctx context.Context, id string, keys []string) error
}

type tagsResponseXML struct {
	XMLName   xml.Name `xml:"CreateTagsResponse"`
	Return    bool     `xml:"return"`
	RequestID string   `xml:"requestId"`
}

type deleteTagsResponseXML struct {
	XMLName   xml.Name `xml:"DeleteTagsResponse"`
	Return    bool     `xml:"return"`
	RequestID string   `xml:"requestId"`
}

func (h *Handler) routeTags(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateTags":
		h.createTags(w, r)
	case "DeleteTags":
		h.deleteTags(w, r)
	default:
		return false
	}

	return true
}

// createTags applies tags to one or more resources, dispatching each resource
// ID by prefix to the owning provider (VPC-family IDs to the networking
// provider, compute IDs to the compute tagger).
func (h *Handler) createTags(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "ResourceId")
	tags := awsquery.FlatTags(r.Form, "Tag")

	for _, id := range ids {
		if err := h.tagResource(r.Context(), id, tags); err != nil {
			writeErrWithNotFound(w, err, "InvalidID.NotFound", "IncorrectState")
			return
		}
	}

	awsquery.WriteXMLResponse(w, tagsResponseXML{Return: true, RequestID: "cloudemu"})
}

// deleteTags removes tags (by key) from one or more resources.
func (h *Handler) deleteTags(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "ResourceId")
	tags := awsquery.FlatTags(r.Form, "Tag")

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	for _, id := range ids {
		if err := h.untagResource(r.Context(), id, keys); err != nil {
			writeErrWithNotFound(w, err, "InvalidID.NotFound", "IncorrectState")
			return
		}
	}

	awsquery.WriteXMLResponse(w, deleteTagsResponseXML{Return: true, RequestID: "cloudemu"})
}

func (h *Handler) tagResource(ctx context.Context, id string, tags map[string]string) error {
	switch {
	case strings.HasPrefix(id, "vpc-"):
		return h.vpc.UpdateVPCTags(ctx, id, tags)
	case strings.HasPrefix(id, "subnet-"):
		return h.vpc.UpdateSubnetTags(ctx, id, tags)
	case strings.HasPrefix(id, "sg-"):
		return h.vpc.UpdateSecurityGroupTags(ctx, id, tags)
	default:
		if tagger, ok := h.compute.(computeTagger); ok {
			return tagger.TagResource(ctx, id, tags)
		}

		return nil
	}
}

func (h *Handler) untagResource(ctx context.Context, id string, keys []string) error {
	switch {
	case strings.HasPrefix(id, "vpc-"):
		return h.vpc.RemoveVPCTags(ctx, id, keys)
	case strings.HasPrefix(id, "subnet-"):
		return h.vpc.RemoveSubnetTags(ctx, id, keys)
	case strings.HasPrefix(id, "sg-"):
		return h.vpc.RemoveSecurityGroupTags(ctx, id, keys)
	default:
		if tagger, ok := h.compute.(computeTagger); ok {
			return tagger.UntagResource(ctx, id, keys)
		}

		return nil
	}
}
