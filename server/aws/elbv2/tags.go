package elbv2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

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
