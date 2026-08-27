package iam

import (
	"context"
	"encoding/xml"
	"net/http"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// userTagManager is the AWS-specific user-tagging surface, asserted against the
// provider (not part of the portable IAM driver).
type userTagManager interface {
	TagUser(ctx context.Context, userName string, tags map[string]string) error
	UntagUser(ctx context.Context, userName string, keys []string) error
	ListUserTags(ctx context.Context, userName string) (map[string]string, error)
}

type tagUserResponse struct {
	XMLName  xml.Name         `xml:"TagUserResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type untagUserResponse struct {
	XMLName  xml.Name         `xml:"UntagUserResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type listUserTagsResponse struct {
	XMLName  xml.Name         `xml:"ListUserTagsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Tags     []tagMemberXML   `xml:"ListUserTagsResult>Tags>member"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) userTags() (userTagManager, bool) {
	m, ok := h.iam.(userTagManager)

	return m, ok
}

//nolint:dupl // tag handlers share shape but operate on distinct actions and response envelopes.
func (h *Handler) tagUser(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.userTags()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "user tagging not supported"))
		return
	}

	if err := mgr.TagUser(r.Context(), r.Form.Get("UserName"), awsquery.FlatTags(r.Form, "Tags.member")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, tagUserResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // tag handlers share shape but operate on distinct actions and response envelopes.
func (h *Handler) untagUser(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.userTags()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "user tagging not supported"))
		return
	}

	if err := mgr.UntagUser(r.Context(), r.Form.Get("UserName"), awsquery.ListStrings(r.Form, "TagKeys.member")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, untagUserResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listUserTags(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.userTags()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "user tagging not supported"))
		return
	}

	tags, err := mgr.ListUserTags(r.Context(), r.Form.Get("UserName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	members := make([]tagMemberXML, 0, len(keys))
	for _, k := range keys {
		members = append(members, tagMemberXML{Key: k, Value: tags[k]})
	}

	awsquery.WriteXMLResponse(w, listUserTagsResponse{
		Xmlns: Namespace, Tags: members, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
