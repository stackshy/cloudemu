package kinesisvideo

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

// nameCondition is the StreamNameCondition / ChannelNameCondition list filter.
type nameCondition struct {
	ComparisonOperator string `json:"ComparisonOperator"`
	ComparisonValue    string `json:"ComparisonValue"`
}

// beginsWith returns the BEGINS_WITH prefix a name condition selects, or "" when
// the condition is absent or uses another operator.
func (c *nameCondition) beginsWith() string {
	if c != nil && c.ComparisonOperator == driver.ComparisonBeginsWith {
		return c.ComparisonValue
	}

	return ""
}

func (h *Handler) createStream(w http.ResponseWriter, r *http.Request) {
	var in driver.CreateStreamInput
	if !decodeBody(w, r, &in) {
		return
	}

	out, err := h.kv.CreateStream(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"StreamARN": out.StreamARN})
}

func (h *Handler) describeStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
		StreamARN  string `json:"StreamARN"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	out, err := h.kv.DescribeStream(r.Context(), driver.StreamRef{StreamName: req.StreamName, StreamARN: req.StreamARN})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"StreamInfo": streamInfoToWire(out)})
}

func (h *Handler) updateStream(w http.ResponseWriter, r *http.Request) {
	var in driver.UpdateStreamInput
	if !decodeBody(w, r, &in) {
		return
	}

	if err := h.kv.UpdateStream(r.Context(), &in); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) updateDataRetention(w http.ResponseWriter, r *http.Request) {
	var in driver.UpdateDataRetentionInput
	if !decodeBody(w, r, &in) {
		return
	}

	if err := h.kv.UpdateDataRetention(r.Context(), &in); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) deleteStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName     string `json:"StreamName"`
		StreamARN      string `json:"StreamARN"`
		CurrentVersion string `json:"CurrentVersion"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	ref := driver.StreamRef{StreamName: req.StreamName, StreamARN: req.StreamARN}
	if err := h.kv.DeleteStream(r.Context(), ref, req.CurrentVersion); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

//nolint:dupl // parallel to listSignalingChannels by design; the request/response shapes differ.
func (h *Handler) listStreams(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MaxResults          int32          `json:"MaxResults"`
		NextToken           string         `json:"NextToken"`
		StreamNameCondition *nameCondition `json:"StreamNameCondition"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	items, next, err := h.kv.ListStreams(r.Context(), driver.Page{
		NextToken:      req.NextToken,
		MaxResults:     req.MaxResults,
		NameBeginsWith: req.StreamNameCondition.beginsWith(),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	list := make([]map[string]any, 0, len(items))
	for i := range items {
		list = append(list, streamInfoToWire(&items[i]))
	}

	body := map[string]any{"StreamInfoList": list}
	if next != "" {
		body["NextToken"] = next
	}

	writeJSON(w, body)
}

func (h *Handler) tagStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string            `json:"StreamName"`
		StreamARN  string            `json:"StreamARN"`
		Tags       map[string]string `json:"Tags"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	ref := driver.StreamRef{StreamName: req.StreamName, StreamARN: req.StreamARN}
	if err := h.kv.TagStream(r.Context(), ref, req.Tags); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) untagStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string   `json:"StreamName"`
		StreamARN  string   `json:"StreamARN"`
		TagKeyList []string `json:"TagKeyList"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	ref := driver.StreamRef{StreamName: req.StreamName, StreamARN: req.StreamARN}
	if err := h.kv.UntagStream(r.Context(), ref, req.TagKeyList); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listTagsForStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
		StreamARN  string `json:"StreamARN"`
		NextToken  string `json:"NextToken"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	ref := driver.StreamRef{StreamName: req.StreamName, StreamARN: req.StreamARN}

	tags, next, err := h.kv.ListTagsForStream(r.Context(), ref, req.NextToken)
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{"Tags": tags}
	if next != "" {
		body["NextToken"] = next
	}

	writeJSON(w, body)
}
