package redshift

import (
	"encoding/xml"
	"net/http"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

type taggedResourceXML struct {
	ResourceName string `xml:"ResourceName"`
	Key          string `xml:"Tag>Key"`
	Value        string `xml:"Tag>Value"`
}

type createTagsResponse struct {
	XMLName  xml.Name         `xml:"CreateTagsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteTagsResponse struct {
	XMLName  xml.Name         `xml:"DeleteTagsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeTagsResponse struct {
	XMLName   xml.Name            `xml:"DescribeTagsResponse"`
	Xmlns     string              `xml:"xmlns,attr"`
	Resources []taggedResourceXML `xml:"DescribeTagsResult>TaggedResources>TaggedResource"`
	Metadata  responseMetadata    `xml:"ResponseMetadata"`
}

func (h *Handler) resourceTagger() (resourceTagger, bool) {
	t, ok := h.db.(resourceTagger)

	return t, ok
}

func (h *Handler) createTags(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.resourceTagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	if err := tagger.CreateTags(r.Context(), r.Form.Get("ResourceName"), awsquery.FlatTags(r.Form, "Tags.Tag")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createTagsResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteTags(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.resourceTagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	if err := tagger.DeleteTags(r.Context(), r.Form.Get("ResourceName"), awsquery.ListStrings(r.Form, "TagKeys.TagKey")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteTagsResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeTags(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.resourceTagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	resourceName := r.Form.Get("ResourceName")

	tags, err := tagger.DescribeTags(r.Context(), resourceName)
	if err != nil {
		writeErr(w, err)
		return
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]taggedResourceXML, 0, len(keys))
	for _, k := range keys {
		out = append(out, taggedResourceXML{ResourceName: resourceName, Key: k, Value: tags[k]})
	}

	awsquery.WriteXMLResponse(w, describeTagsResponse{
		Xmlns: Namespace, Resources: out, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
