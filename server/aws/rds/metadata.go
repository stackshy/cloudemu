package rds

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// ---- XML shapes ----

type dbEngineVersionXML struct {
	Engine                 string `xml:"Engine,omitempty"`
	EngineVersion          string `xml:"EngineVersion,omitempty"`
	DBEngineDescription    string `xml:"DBEngineDescription,omitempty"`
	DBParameterGroupFamily string `xml:"DBParameterGroupFamily,omitempty"`
}

type describeDBEngineVersionsResponse struct {
	XMLName  xml.Name             `xml:"DescribeDBEngineVersionsResponse"`
	Xmlns    string               `xml:"xmlns,attr"`
	Result   dbEngineVersionsList `xml:"DescribeDBEngineVersionsResult"`
	Metadata responseMetadata     `xml:"ResponseMetadata"`
}

type dbEngineVersionsList struct {
	DBEngineVersions []dbEngineVersionXML `xml:"DBEngineVersions>DBEngineVersion"`
}

type orderableOptionXML struct {
	Engine          string `xml:"Engine,omitempty"`
	EngineVersion   string `xml:"EngineVersion,omitempty"`
	DBInstanceClass string `xml:"DBInstanceClass,omitempty"`
	StorageType     string `xml:"StorageType,omitempty"`
	MultiAZCapable  bool   `xml:"MultiAZCapable"`
}

type describeOrderableDBInstanceOptionsResponse struct {
	XMLName  xml.Name             `xml:"DescribeOrderableDBInstanceOptionsResponse"`
	Xmlns    string               `xml:"xmlns,attr"`
	Result   orderableOptionsList `xml:"DescribeOrderableDBInstanceOptionsResult"`
	Metadata responseMetadata     `xml:"ResponseMetadata"`
}

type orderableOptionsList struct {
	OrderableDBInstanceOptions []orderableOptionXML `xml:"OrderableDBInstanceOptions>OrderableDBInstanceOption"`
}

type listTagsForResourceResponse struct {
	XMLName  xml.Name         `xml:"ListTagsForResourceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   tagListResult    `xml:"ListTagsForResourceResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type tagListResult struct {
	TagList []tagXML `xml:"TagList>Tag"`
}

type addTagsToResourceResponse struct {
	XMLName  xml.Name         `xml:"AddTagsToResourceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   struct{}         `xml:"AddTagsToResourceResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type removeTagsFromResourceResponse struct {
	XMLName  xml.Name         `xml:"RemoveTagsFromResourceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   struct{}         `xml:"RemoveTagsFromResourceResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// ---- capability gates ----

func (h *Handler) metadataCap() (rdsdriver.Metadata, bool) {
	c, ok := h.db.(rdsdriver.Metadata)

	return c, ok
}

func (h *Handler) taggingCap() (rdsdriver.Tagging, bool) {
	c, ok := h.db.(rdsdriver.Tagging)

	return c, ok
}

// ---- handlers ----

func (h *Handler) describeDBEngineVersions(w http.ResponseWriter, r *http.Request) {
	store, ok := h.metadataCap()
	if !ok {
		writeUnsupported(w, "engine-version metadata")
		return
	}

	versions, err := store.DescribeDBEngineVersions(r.Context(), r.Form.Get("Engine"), r.Form.Get("EngineVersion"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]dbEngineVersionXML, 0, len(versions))
	for _, v := range versions {
		out = append(out, dbEngineVersionXML{
			Engine:                 v.Engine,
			EngineVersion:          v.EngineVersion,
			DBEngineDescription:    v.DBEngineDescription,
			DBParameterGroupFamily: v.DBParameterGroupFamily,
		})
	}

	awsquery.WriteXMLResponse(w, describeDBEngineVersionsResponse{
		Xmlns:    Namespace,
		Result:   dbEngineVersionsList{DBEngineVersions: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeOrderableDBInstanceOptions(w http.ResponseWriter, r *http.Request) {
	store, ok := h.metadataCap()
	if !ok {
		writeUnsupported(w, "orderable-option metadata")
		return
	}

	opts, err := store.DescribeOrderableDBInstanceOptions(r.Context(), r.Form.Get("Engine"), r.Form.Get("EngineVersion"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]orderableOptionXML, 0, len(opts))
	for _, o := range opts {
		out = append(out, orderableOptionXML{
			Engine:          o.Engine,
			EngineVersion:   o.EngineVersion,
			DBInstanceClass: o.DBInstanceClass,
			StorageType:     o.StorageType,
			MultiAZCapable:  o.MultiAZCapable,
		})
	}

	awsquery.WriteXMLResponse(w, describeOrderableDBInstanceOptionsResponse{
		Xmlns:    Namespace,
		Result:   orderableOptionsList{OrderableDBInstanceOptions: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) addTagsToResource(w http.ResponseWriter, r *http.Request) {
	store, ok := h.taggingCap()
	if !ok {
		writeUnsupported(w, "resource tagging")
		return
	}

	if err := store.AddTagsToResource(r.Context(), r.Form.Get("ResourceName"), parseRDSTags(r.Form)); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, addTagsToResourceResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) removeTagsFromResource(w http.ResponseWriter, r *http.Request) {
	store, ok := h.taggingCap()
	if !ok {
		writeUnsupported(w, "resource tagging")
		return
	}

	if err := store.RemoveTagsFromResource(r.Context(), r.Form.Get("ResourceName"),
		awsquery.ListStrings(r.Form, "TagKeys.member")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, removeTagsFromResourceResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	store, ok := h.taggingCap()
	if !ok {
		writeUnsupported(w, "resource tagging")
		return
	}

	tags, err := store.ListTagsForResource(r.Context(), r.Form.Get("ResourceName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	tl := toTagListXML(tags)

	var out []tagXML
	if tl != nil {
		out = tl.Tag
	}

	awsquery.WriteXMLResponse(w, listTagsForResourceResponse{
		Xmlns:    Namespace,
		Result:   tagListResult{TagList: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
