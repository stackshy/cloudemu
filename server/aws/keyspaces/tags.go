package keyspaces

import (
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.TagResourceInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	if rejectTagListTooLong(w, len(in.Tags)) {
		return
	}

	if err := h.db.TagResource(r.Context(), aws.ToString(in.ResourceArn), tagMap(in.Tags)); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.TagResourceOutput{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.UntagResourceInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	if rejectTagListTooLong(w, len(in.Tags)) {
		return
	}

	keys := make([]string, 0, len(in.Tags))
	for i := range in.Tags {
		keys = append(keys, aws.ToString(in.Tags[i].Key))
	}

	if err := h.db.UntagResource(r.Context(), aws.ToString(in.ResourceArn), keys); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.UntagResourceOutput{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.ListTagsForResourceInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	tags, err := h.db.ListTagsForResource(r.Context(), aws.ToString(in.ResourceArn))
	if err != nil {
		writeErr(w, err)
		return
	}

	page, next, err := paginate(tags, in.MaxResults, in.NextToken)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.ListTagsForResourceOutput{Tags: toWireTags(page), NextToken: next})
}

// maxTagListLen is the documented TagList maximum (Array Members: Maximum
// number of 60 items) on TagResource, UntagResource, CreateKeyspace,
// CreateTable and RestoreTable.
const maxTagListLen = 60

// rejectTagListTooLong writes a ValidationException and returns true when a
// request's tag list exceeds the documented TagList size.
func rejectTagListTooLong(w http.ResponseWriter, n int) bool {
	if n <= maxTagListLen {
		return false
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException",
		fmt.Sprintf("tags must contain at most %d items", maxTagListLen))

	return true
}
