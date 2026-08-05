package elbv2

import (
	"context"
	"encoding/xml"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// tagMutator is the AWS-specific ELBv2 tag-write surface, asserted against the
// provider (not part of the portable LoadBalancer driver).
type tagMutator interface {
	AddResourceTags(ctx context.Context, arn string, tags map[string]string) error
	RemoveResourceTags(ctx context.Context, arn string, keys []string) error
}

// The ELBv2 SDK unmarshaler expects the empty <FooResult/> wrapper element.
type addTagsResponse struct {
	XMLName  xml.Name         `xml:"AddTagsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   struct{}         `xml:"AddTagsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type removeTagsResponse struct {
	XMLName  xml.Name         `xml:"RemoveTagsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   struct{}         `xml:"RemoveTagsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) tagMutator() (tagMutator, bool) {
	m, ok := h.lb.(tagMutator)

	return m, ok
}

func (h *Handler) addTags(w http.ResponseWriter, r *http.Request) {
	mut, ok := h.tagMutator()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	tags := awsquery.FlatTags(r.Form, "Tags.member")
	for _, arn := range awsquery.ListStrings(r.Form, "ResourceArns.member") {
		if err := mut.AddResourceTags(r.Context(), arn, tags); err != nil {
			writeErr(w, err)
			return
		}
	}

	awsquery.WriteXMLResponse(w, addTagsResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) removeTags(w http.ResponseWriter, r *http.Request) {
	mut, ok := h.tagMutator()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	keys := awsquery.ListStrings(r.Form, "TagKeys.member")
	for _, arn := range awsquery.ListStrings(r.Form, "ResourceArns.member") {
		if err := mut.RemoveResourceTags(r.Context(), arn, keys); err != nil {
			writeErr(w, err)
			return
		}
	}

	awsquery.WriteXMLResponse(w, removeTagsResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

type tagMemberXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type tagDescriptionXML struct {
	ResourceArn string         `xml:"ResourceArn"`
	Tags        []tagMemberXML `xml:"Tags>member,omitempty"`
}

type describeTagsResult struct {
	TagDescriptions []tagDescriptionXML `xml:"TagDescriptions>member"`
}

type describeTagsResponse struct {
	XMLName  xml.Name           `xml:"DescribeTagsResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   describeTagsResult `xml:"DescribeTagsResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

// describeTags returns the tags carried by the named resources.
//
// Real DescribeTags accepts load balancers and target groups (and listeners
// and rules) in one call and does not care which is which, so each ARN is
// resolved against both collections rather than assumed to be a load
// balancer. Getting this wrong is not harmless: a caller sweeping for
// orphaned infrastructure identifies its own resources by tag, and both an
// empty answer and a spurious not-found read as "not mine", leaving the
// orphan standing.
func (h *Handler) describeTags(w http.ResponseWriter, r *http.Request) {
	arns := awsquery.ListStrings(r.Form, "ResourceArns.member")

	tagsByARN, err := h.collectTags(r, arns)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]tagDescriptionXML, 0, len(arns))

	for _, arn := range arns {
		tags, ok := tagsByARN[arn]
		if !ok {
			continue
		}

		td := tagDescriptionXML{ResourceArn: arn}
		for k, v := range tags {
			td.Tags = append(td.Tags, tagMemberXML{Key: k, Value: v})
		}

		out = append(out, td)
	}

	awsquery.WriteXMLResponse(w, describeTagsResponse{
		Xmlns:    Namespace,
		Result:   describeTagsResult{TagDescriptions: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// collectTags resolves each ARN against load balancers and target groups.
//
// Both collections are listed unfiltered and matched locally rather than
// queried per ARN, because a by-ARN describe reports not-found for a resource
// of the other kind — which is a correct answer to the wrong question here.
func (h *Handler) collectTags(
	r *http.Request, arns []string,
) (map[string]map[string]string, error) {
	want := make(map[string]bool, len(arns))
	for _, arn := range arns {
		want[arn] = true
	}

	out := make(map[string]map[string]string, len(arns))

	lbs, err := h.lb.DescribeLoadBalancers(r.Context(), nil)
	if err != nil {
		return nil, err
	}

	for i := range lbs {
		if want[lbs[i].ARN] {
			out[lbs[i].ARN] = lbs[i].Tags
		}
	}

	tgs, err := h.lb.DescribeTargetGroups(r.Context(), nil)
	if err != nil {
		return nil, err
	}

	for i := range tgs {
		if want[tgs[i].ARN] {
			out[tgs[i].ARN] = tgs[i].Tags
		}
	}

	return out, nil
}
