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

// describeTags returns the tags carried by the named load balancers.
//
// Callers sweeping for orphaned infrastructure identify their own load
// balancers by tag, so an empty answer here reads as "not mine" and the
// orphan is left standing.
func (h *Handler) describeTags(w http.ResponseWriter, r *http.Request) {
	arns := awsquery.ListStrings(r.Form, "ResourceArns.member")

	lbs, err := h.lb.DescribeLoadBalancers(r.Context(), arns)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]tagDescriptionXML, 0, len(lbs))

	for i := range lbs {
		td := tagDescriptionXML{ResourceArn: lbs[i].ARN}
		for k, v := range lbs[i].Tags {
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
