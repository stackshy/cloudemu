package memorydb

import (
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	"github.com/aws/aws-sdk-go-v2/service/memorydb/types"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var in memorydb.TagResourceInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	tags, err := h.db.TagResource(r.Context(), aws.ToString(in.ResourceArn), tagMap(in.Tags))
	if err != nil {
		writeErr(w, "Tag", err)
		return
	}

	wire.WriteJSON(w, memorydb.TagResourceOutput{TagList: toWireTags(tags)})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var in memorydb.UntagResourceInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	tags, err := h.db.UntagResource(r.Context(), aws.ToString(in.ResourceArn), in.TagKeys)
	if err != nil {
		writeErr(w, "Tag", err)
		return
	}

	wire.WriteJSON(w, memorydb.UntagResourceOutput{TagList: toWireTags(tags)})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	var in memorydb.ListTagsInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	tags, err := h.db.ListTags(r.Context(), aws.ToString(in.ResourceArn))
	if err != nil {
		writeErr(w, "Tag", err)
		return
	}

	wire.WriteJSON(w, memorydb.ListTagsOutput{TagList: toWireTags(tags)})
}

func (h *Handler) describeEngineVersions(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DescribeEngineVersionsInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	versions, err := h.db.DescribeEngineVersions(r.Context(), aws.ToString(in.Engine), aws.ToString(in.EngineVersion))
	if err != nil {
		writeErr(w, "Cluster", err)
		return
	}

	out := memorydb.DescribeEngineVersionsOutput{}
	for _, v := range versions {
		out.EngineVersions = append(out.EngineVersions, types.EngineVersionInfo{
			Engine: aws.String(v.Engine), EngineVersion: aws.String(v.EngineVersion),
			EnginePatchVersion: aws.String(v.EnginePatchVersion), ParameterGroupFamily: aws.String(v.ParameterGroupFamily),
		})
	}

	wire.WriteJSON(w, out)
}

func (h *Handler) describeEvents(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DescribeEventsInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	events, err := h.db.DescribeEvents(r.Context())
	if err != nil {
		writeErr(w, "Cluster", err)
		return
	}

	out := memorydb.DescribeEventsOutput{}
	for _, e := range events {
		out.Events = append(out.Events, types.Event{
			SourceName: aws.String(e.SourceName), SourceType: types.SourceType(e.SourceType),
			Message: aws.String(e.Message),
			// Date omitted: AWS JSON 1.1 encodes timestamps as epoch numbers,
			// which encoding/json cannot emit for a time.Time.
		})
	}

	wire.WriteJSON(w, out)
}
