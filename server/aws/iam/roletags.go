package iam

import (
	"encoding/xml"
	"net/http"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

type tagMemberXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type tagRoleResponse struct {
	XMLName  xml.Name         `xml:"TagRoleResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type untagRoleResponse struct {
	XMLName  xml.Name         `xml:"UntagRoleResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type listRoleTagsResponse struct {
	XMLName  xml.Name         `xml:"ListRoleTagsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Tags     []tagMemberXML   `xml:"ListRoleTagsResult>Tags>member"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) roleTags() (roleTagManager, bool) {
	m, ok := h.iam.(roleTagManager)

	return m, ok
}

func (h *Handler) tagRole(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.roleTags()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "role tagging not supported"))
		return
	}

	if err := mgr.TagRole(r.Context(), r.Form.Get("RoleName"), awsquery.FlatTags(r.Form, "Tags.member")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, tagRoleResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) untagRole(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.roleTags()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "role tagging not supported"))
		return
	}

	if err := mgr.UntagRole(r.Context(), r.Form.Get("RoleName"), awsquery.ListStrings(r.Form, "TagKeys.member")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, untagRoleResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listRoleTags(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.roleTags()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "role tagging not supported"))
		return
	}

	tags, err := mgr.ListRoleTags(r.Context(), r.Form.Get("RoleName"))
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

	awsquery.WriteXMLResponse(w, listRoleTagsResponse{
		Xmlns: Namespace, Tags: members, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
