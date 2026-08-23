package elasticache

import (
	"context"
	"encoding/xml"
	"net/http"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ecprovider "github.com/stackshy/cloudemu/v2/providers/aws/elasticache"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// resourceTagger is the AWS-specific ElastiCache tagging surface. It is not part
// of the portable Cache driver, so the handler type-asserts for it.
type resourceTagger interface {
	AddTags(ctx context.Context, arn string, tags map[string]string) error
	ListTags(ctx context.Context, arn string) (map[string]string, error)
	RemoveTags(ctx context.Context, arn string, keys []string) error
}

// parameterGroupManager is the AWS-specific cache-parameter-group surface.
type parameterGroupManager interface {
	CreateCacheParameterGroup(ctx context.Context, name, family, description string) (*ecprovider.ParameterGroup, error)
	DescribeCacheParameterGroups(ctx context.Context, names []string) ([]ecprovider.ParameterGroup, error)
	ModifyCacheParameterGroup(ctx context.Context, name string) error
	DeleteCacheParameterGroup(ctx context.Context, name string) error
}

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type addTagsToResourceResponse struct {
	XMLName  xml.Name         `xml:"AddTagsToResourceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	TagList  []tagXML         `xml:"AddTagsToResourceResult>TagList>Tag"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type listTagsForResourceResponse struct {
	XMLName  xml.Name         `xml:"ListTagsForResourceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	TagList  []tagXML         `xml:"ListTagsForResourceResult>TagList>Tag"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type removeTagsFromResourceResponse struct {
	XMLName  xml.Name         `xml:"RemoveTagsFromResourceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	TagList  []tagXML         `xml:"RemoveTagsFromResourceResult>TagList>Tag"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// tagListXML converts a tag map to a stable, key-sorted slice of tagXML.
func tagListXML(tags map[string]string) []tagXML {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	list := make([]tagXML, 0, len(keys))
	for _, k := range keys {
		list = append(list, tagXML{Key: k, Value: tags[k]})
	}

	return list
}

func (h *Handler) tagger() (resourceTagger, bool) {
	t, ok := h.cache.(resourceTagger)

	return t, ok
}

func (h *Handler) addTagsToResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.tagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "AddTagsToResource not supported"))
		return
	}

	arn := r.Form.Get("ResourceName")
	if err := tagger.AddTags(r.Context(), arn, parseTags(r.Form)); err != nil {
		writeErr(w, err)
		return
	}

	tags, err := tagger.ListTags(r.Context(), arn)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, addTagsToResourceResponse{
		Xmlns:    Namespace,
		TagList:  tagListXML(tags),
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.tagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "ListTagsForResource not supported"))
		return
	}

	tags, err := tagger.ListTags(r.Context(), r.Form.Get("ResourceName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, listTagsForResourceResponse{
		Xmlns:    Namespace,
		TagList:  tagListXML(tags),
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) removeTagsFromResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.tagger()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "RemoveTagsFromResource not supported"))
		return
	}

	arn := r.Form.Get("ResourceName")
	if err := tagger.RemoveTags(r.Context(), arn, awsquery.ListStrings(r.Form, "TagKeys.member")); err != nil {
		writeErr(w, err)
		return
	}

	tags, err := tagger.ListTags(r.Context(), arn)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, removeTagsFromResourceResponse{
		Xmlns:    Namespace,
		TagList:  tagListXML(tags),
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

type cacheParameterGroupXML struct {
	CacheParameterGroupName   string `xml:"CacheParameterGroupName"`
	CacheParameterGroupFamily string `xml:"CacheParameterGroupFamily"`
	Description               string `xml:"Description"`
}

type createCacheParameterGroupResponse struct {
	XMLName  xml.Name               `xml:"CreateCacheParameterGroupResponse"`
	Xmlns    string                 `xml:"xmlns,attr"`
	Group    cacheParameterGroupXML `xml:"CreateCacheParameterGroupResult>CacheParameterGroup"`
	Metadata responseMetadata       `xml:"ResponseMetadata"`
}

type describeCacheParameterGroupsResponse struct {
	XMLName  xml.Name                 `xml:"DescribeCacheParameterGroupsResponse"`
	Xmlns    string                   `xml:"xmlns,attr"`
	Groups   []cacheParameterGroupXML `xml:"DescribeCacheParameterGroupsResult>CacheParameterGroups>CacheParameterGroup"`
	Metadata responseMetadata         `xml:"ResponseMetadata"`
}

type modifyCacheParameterGroupResponse struct {
	XMLName  xml.Name         `xml:"ModifyCacheParameterGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Name     string           `xml:"ModifyCacheParameterGroupResult>CacheParameterGroupName"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteCacheParameterGroupResponse struct {
	XMLName  xml.Name         `xml:"DeleteCacheParameterGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) paramGroups() (parameterGroupManager, bool) {
	m, ok := h.cache.(parameterGroupManager)

	return m, ok
}

func (h *Handler) createCacheParameterGroup(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.paramGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "cache parameter groups not supported"))
		return
	}

	pg, err := mgr.CreateCacheParameterGroup(r.Context(),
		r.Form.Get("CacheParameterGroupName"), r.Form.Get("CacheParameterGroupFamily"), r.Form.Get("Description"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createCacheParameterGroupResponse{
		Xmlns: Namespace,
		Group: cacheParameterGroupXML{
			CacheParameterGroupName:   pg.Name,
			CacheParameterGroupFamily: pg.Family,
			Description:               pg.Description,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeCacheParameterGroups(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.paramGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "cache parameter groups not supported"))
		return
	}

	var names []string
	if name := r.Form.Get("CacheParameterGroupName"); name != "" {
		names = []string{name}
	}

	groups, err := mgr.DescribeCacheParameterGroups(r.Context(), names)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]cacheParameterGroupXML, 0, len(groups))
	for i := range groups {
		out = append(out, cacheParameterGroupXML{
			CacheParameterGroupName:   groups[i].Name,
			CacheParameterGroupFamily: groups[i].Family,
			Description:               groups[i].Description,
		})
	}

	awsquery.WriteXMLResponse(w, describeCacheParameterGroupsResponse{
		Xmlns:    Namespace,
		Groups:   out,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyCacheParameterGroup(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.paramGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "cache parameter groups not supported"))
		return
	}

	name := r.Form.Get("CacheParameterGroupName")
	if err := mgr.ModifyCacheParameterGroup(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyCacheParameterGroupResponse{
		Xmlns:    Namespace,
		Name:     name,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteCacheParameterGroup(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.paramGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "cache parameter groups not supported"))
		return
	}

	if err := mgr.DeleteCacheParameterGroup(r.Context(), r.Form.Get("CacheParameterGroupName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteCacheParameterGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

type cacheEngineVersionXML struct {
	Engine                    string `xml:"Engine"`
	EngineVersion             string `xml:"EngineVersion"`
	CacheParameterGroupFamily string `xml:"CacheParameterGroupFamily"`
	CacheEngineDescription    string `xml:"CacheEngineDescription"`
	CacheEngineVersionDescrip string `xml:"CacheEngineVersionDescription"`
}

type describeCacheEngineVersionsResponse struct {
	XMLName  xml.Name                `xml:"DescribeCacheEngineVersionsResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Versions []cacheEngineVersionXML `xml:"DescribeCacheEngineVersionsResult>CacheEngineVersions>CacheEngineVersion"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

// staticEngineVersions is the small, representative set of engine versions the
// emulator advertises. Real ElastiCache lists many; this covers what IaC needs
// to validate an EngineVersion choice for redis and memcached.
func staticEngineVersions() []cacheEngineVersionXML {
	return []cacheEngineVersionXML{
		{Engine: "redis", EngineVersion: "7.1", CacheParameterGroupFamily: "redis7",
			CacheEngineDescription: "Redis", CacheEngineVersionDescrip: "redis version 7.1.0"},
		{Engine: "redis", EngineVersion: "6.2", CacheParameterGroupFamily: "redis6.x",
			CacheEngineDescription: "Redis", CacheEngineVersionDescrip: "redis version 6.2.6"},
		{Engine: "memcached", EngineVersion: "1.6.22", CacheParameterGroupFamily: "memcached1.6",
			CacheEngineDescription: "memcached", CacheEngineVersionDescrip: "memcached version 1.6.22"},
	}
}

func (h *Handler) describeCacheEngineVersions(w http.ResponseWriter, r *http.Request) {
	engine := r.Form.Get("Engine")

	all := staticEngineVersions()

	out := make([]cacheEngineVersionXML, 0, len(all))
	for _, v := range all {
		if engine != "" && v.Engine != engine {
			continue
		}

		out = append(out, v)
	}

	awsquery.WriteXMLResponse(w, describeCacheEngineVersionsResponse{
		Xmlns:    Namespace,
		Versions: out,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
