package eventbridgescheduler

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

// createGroupRequest is the CreateScheduleGroup request body. Tags use the AWS
// tag-list shape ([{Key,Value}]).
type createGroupRequest struct {
	ClientToken string    `json:"ClientToken"`
	Tags        []tagPair `json:"Tags"`
}

func (h *Handler) createScheduleGroup(w http.ResponseWriter, r *http.Request, name string) {
	var req createGroupRequest
	if !decodeBody(w, r, &req) {
		return
	}

	out, err := h.s.CreateScheduleGroup(r.Context(), name, tagListToMap(req.Tags))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"ScheduleGroupArn": out.Arn})
}

func (h *Handler) getScheduleGroup(w http.ResponseWriter, r *http.Request, name string) {
	out, err := h.s.GetScheduleGroup(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, groupToWire(out))
}

func (h *Handler) deleteScheduleGroup(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.s.DeleteScheduleGroup(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listScheduleGroups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	items, next, err := h.s.ListScheduleGroups(r.Context(), driver.GroupFilter{
		NamePrefix: q.Get("NamePrefix"),
		Page: driver.Page{
			NextToken:  q.Get("NextToken"),
			MaxResults: atoiDefault(q.Get("MaxResults")),
		},
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	summaries := make([]map[string]any, 0, len(items))
	for i := range items {
		summaries = append(summaries, groupToWire(&items[i]))
	}

	body := map[string]any{"ScheduleGroups": summaries}
	if next != "" {
		body["NextToken"] = next
	}

	writeJSON(w, body)
}
