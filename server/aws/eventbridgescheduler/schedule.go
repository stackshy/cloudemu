package eventbridgescheduler

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

func (h *Handler) createSchedule(w http.ResponseWriter, r *http.Request, name string) {
	var in driver.ScheduleInput
	if !decodeBody(w, r, &in) {
		return
	}

	in.Name = name

	out, err := h.s.CreateSchedule(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"ScheduleArn": out.Arn})
}

func (h *Handler) getSchedule(w http.ResponseWriter, r *http.Request, name string) {
	group := r.URL.Query().Get("groupName")

	out, err := h.s.GetSchedule(r.Context(), group, name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, scheduleToWire(out))
}

func (h *Handler) updateSchedule(w http.ResponseWriter, r *http.Request, name string) {
	var in driver.ScheduleInput
	if !decodeBody(w, r, &in) {
		return
	}

	in.Name = name

	out, err := h.s.UpdateSchedule(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"ScheduleArn": out.Arn})
}

func (h *Handler) deleteSchedule(w http.ResponseWriter, r *http.Request, name string) {
	group := r.URL.Query().Get("groupName")

	if err := h.s.DeleteSchedule(r.Context(), group, name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listSchedules(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	group := q.Get("ScheduleGroup")
	if group == "" {
		group = q.Get("groupName")
	}

	items, next, err := h.s.ListSchedules(r.Context(), driver.ScheduleFilter{
		GroupName:  group,
		NamePrefix: q.Get("NamePrefix"),
		State:      q.Get("State"),
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
		summaries = append(summaries, scheduleSummaryToWire(&items[i]))
	}

	body := map[string]any{"Schedules": summaries}
	if next != "" {
		body["NextToken"] = next
	}

	writeJSON(w, body)
}
