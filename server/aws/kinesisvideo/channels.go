package kinesisvideo

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

func (h *Handler) createSignalingChannel(w http.ResponseWriter, r *http.Request) {
	var in driver.CreateChannelInput
	if !decodeBody(w, r, &in) {
		return
	}

	out, err := h.kv.CreateSignalingChannel(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"ChannelARN": out.ChannelARN})
}

func (h *Handler) describeSignalingChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelName string `json:"ChannelName"`
		ChannelARN  string `json:"ChannelARN"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	ref := driver.ChannelRef{ChannelName: req.ChannelName, ChannelARN: req.ChannelARN}

	out, err := h.kv.DescribeSignalingChannel(r.Context(), ref)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"ChannelInfo": channelInfoToWire(out)})
}

func (h *Handler) updateSignalingChannel(w http.ResponseWriter, r *http.Request) {
	var in driver.UpdateChannelInput
	if !decodeBody(w, r, &in) {
		return
	}

	if err := h.kv.UpdateSignalingChannel(r.Context(), &in); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) deleteSignalingChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelARN     string `json:"ChannelARN"`
		CurrentVersion string `json:"CurrentVersion"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.kv.DeleteSignalingChannel(r.Context(), req.ChannelARN, req.CurrentVersion); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

//nolint:dupl // parallel to listStreams by design; the request/response shapes differ.
func (h *Handler) listSignalingChannels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MaxResults           int32          `json:"MaxResults"`
		NextToken            string         `json:"NextToken"`
		ChannelNameCondition *nameCondition `json:"ChannelNameCondition"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	items, next, err := h.kv.ListSignalingChannels(r.Context(), driver.Page{
		NextToken:      req.NextToken,
		MaxResults:     req.MaxResults,
		NameBeginsWith: req.ChannelNameCondition.beginsWith(),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	list := make([]map[string]any, 0, len(items))
	for i := range items {
		list = append(list, channelInfoToWire(&items[i]))
	}

	body := map[string]any{"ChannelInfoList": list}
	if next != "" {
		body["NextToken"] = next
	}

	writeJSON(w, body)
}
