package fis

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

func (h *Handler) startExperiment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientToken          string `json:"clientToken"`
		ExperimentTemplateID string `json:"experimentTemplateId"`
		ExperimentOptions    *struct {
			ActionsMode string `json:"actionsMode"`
		} `json:"experimentOptions"`
		Tags map[string]string `json:"tags"`
	}

	if !decodeBody(w, r, &body) {
		return
	}

	in := &driver.StartExperimentInput{
		ClientToken:          body.ClientToken,
		ExperimentTemplateID: body.ExperimentTemplateID,
		Tags:                 body.Tags,
	}
	if body.ExperimentOptions != nil {
		in.ActionsMode = body.ExperimentOptions.ActionsMode
	}

	e, err := h.fis.StartExperiment(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"experiment": experimentToWire(e)})
}

func (h *Handler) stopExperiment(w http.ResponseWriter, r *http.Request, id string) {
	e, err := h.fis.StopExperiment(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"experiment": experimentToWire(e)})
}

func (h *Handler) getExperiment(w http.ResponseWriter, r *http.Request, id string) {
	e, err := h.fis.GetExperiment(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"experiment": experimentToWire(e)})
}

func (h *Handler) listExperiments(w http.ResponseWriter, r *http.Request) {
	page := pageFromQuery(r)

	experiments, next, err := h.fis.ListExperiments(r.Context(), page)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeListPage(w, "experiments", experiments, next, experimentSummaryToWire)
}

// experimentToWire renders the full Experiment response object.
func experimentToWire(e *driver.Experiment) map[string]any {
	out := map[string]any{
		"id":                               e.ID,
		"arn":                              e.Arn,
		"experimentTemplateId":             e.ExperimentTemplateID,
		"roleArn":                          e.RoleArn,
		"state":                            stateToWire(e.State),
		"creationTime":                     epochSeconds(e.CreationTime),
		"startTime":                        epochSeconds(e.StartTime),
		"stopConditions":                   stopConditionsToWire(e.StopConditions),
		"experimentOptions":                experimentOptionsToWire(e.ExperimentOptions, true),
		"targetAccountConfigurationsCount": 0,
	}

	if v := epochSeconds(e.EndTime); v != nil {
		out["endTime"] = v
	}

	if targets := targetsToWire(e.Targets); targets != nil {
		out["targets"] = targets
	}

	if actions := experimentActionsToWire(e.Actions); actions != nil {
		out["actions"] = actions
	}

	if lc := logConfigurationToWire(e.LogConfiguration); lc != nil {
		out["logConfiguration"] = lc
	}

	putStringMap(out, "tags", e.Tags)

	return out
}

// experimentSummaryToWire renders a ListExperiments response entry.
func experimentSummaryToWire(e *driver.Experiment) map[string]any {
	out := map[string]any{
		"id":                   e.ID,
		"arn":                  e.Arn,
		"experimentTemplateId": e.ExperimentTemplateID,
		"state":                stateToWire(e.State),
		"creationTime":         epochSeconds(e.CreationTime),
		"experimentOptions":    experimentOptionsToWire(e.ExperimentOptions, true),
	}

	putStringMap(out, "tags", e.Tags)

	return out
}

// stateToWire renders an experiment state block.
func stateToWire(s driver.ExperimentState) map[string]any {
	out := map[string]any{"status": s.Status}
	putNonEmpty(out, "reason", s.Reason)

	return out
}

// experimentActionsToWire renders the experiment actions map, or nil when empty.
func experimentActionsToWire(in map[string]driver.ExperimentAction) map[string]any {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]any, len(in))

	for name := range in {
		a := in[name]
		item := map[string]any{}
		putNonEmpty(item, "actionId", a.ActionID)
		putNonEmpty(item, "description", a.Description)
		putStringMap(item, "parameters", a.Parameters)
		putStringMap(item, "targets", a.Targets)
		putStrings(item, "startAfter", a.StartAfter)

		state := map[string]any{"status": a.State.Status}
		putNonEmpty(state, "reason", a.State.Reason)
		item["state"] = state

		if v := epochSeconds(a.StartTime); v != nil {
			item["startTime"] = v
		}

		if v := epochSeconds(a.EndTime); v != nil {
			item["endTime"] = v
		}

		out[name] = item
	}

	return out
}

// pageFromQuery reads the shared maxResults and nextToken query parameters.
func pageFromQuery(r *http.Request) driver.Page {
	page := driver.Page{NextToken: r.URL.Query().Get("nextToken")}

	if mr := r.URL.Query().Get("maxResults"); mr != "" {
		if n, err := strconv.ParseInt(mr, 10, 32); err == nil {
			page.MaxResults = int32(n)
		}
	}

	return page
}
