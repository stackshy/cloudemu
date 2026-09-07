package fis

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

// templateBody is the shared request shape for create/update. The driver types
// carry the restJson1 JSON tags, so nested action/target/stop-condition blocks
// decode directly.
type templateBody struct {
	ClientToken       string                    `json:"clientToken"`
	Description       *string                   `json:"description"`
	RoleArn           *string                   `json:"roleArn"`
	Actions           map[string]driver.Action  `json:"actions"`
	Targets           map[string]driver.Target  `json:"targets"`
	StopConditions    []driver.StopCondition    `json:"stopConditions"`
	LogConfiguration  *driver.LogConfiguration  `json:"logConfiguration"`
	ExperimentOptions *driver.ExperimentOptions `json:"experimentOptions"`
	Tags              map[string]string         `json:"tags"`
}

func (h *Handler) createExperimentTemplate(w http.ResponseWriter, r *http.Request) {
	var body templateBody
	if !decodeBody(w, r, &body) {
		return
	}

	in := &driver.CreateExperimentTemplateInput{
		ClientToken:       body.ClientToken,
		Actions:           body.Actions,
		Targets:           body.Targets,
		StopConditions:    body.StopConditions,
		LogConfiguration:  body.LogConfiguration,
		ExperimentOptions: body.ExperimentOptions,
		Tags:              body.Tags,
	}
	if body.Description != nil {
		in.Description = *body.Description
	}

	if body.RoleArn != nil {
		in.RoleArn = *body.RoleArn
	}

	t, err := h.fis.CreateExperimentTemplate(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"experimentTemplate": templateToWire(t)})
}

func (h *Handler) getExperimentTemplate(w http.ResponseWriter, r *http.Request, id string) {
	t, err := h.fis.GetExperimentTemplate(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"experimentTemplate": templateToWire(t)})
}

func (h *Handler) updateExperimentTemplate(w http.ResponseWriter, r *http.Request, id string) {
	var body templateBody
	if !decodeBody(w, r, &body) {
		return
	}

	in := &driver.UpdateExperimentTemplateInput{
		ID:                id,
		Description:       body.Description,
		RoleArn:           body.RoleArn,
		LogConfiguration:  body.LogConfiguration,
		ExperimentOptions: body.ExperimentOptions,
	}
	if body.Actions != nil {
		in.Actions = &body.Actions
	}

	if body.Targets != nil {
		in.Targets = &body.Targets
	}

	if body.StopConditions != nil {
		in.StopConditions = &body.StopConditions
	}

	t, err := h.fis.UpdateExperimentTemplate(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"experimentTemplate": templateToWire(t)})
}

func (h *Handler) deleteExperimentTemplate(w http.ResponseWriter, r *http.Request, id string) {
	t, err := h.fis.DeleteExperimentTemplate(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"experimentTemplate": templateToWire(t)})
}

func (h *Handler) listExperimentTemplates(w http.ResponseWriter, r *http.Request) {
	page := pageFromQuery(r)

	templates, next, err := h.fis.ListExperimentTemplates(r.Context(), page)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeListPage(w, "experimentTemplates", templates, next, templateSummaryToWire)
}

// templateToWire renders the full ExperimentTemplate response object.
func templateToWire(t *driver.ExperimentTemplate) map[string]any {
	out := map[string]any{
		"id":                               t.ID,
		"arn":                              t.Arn,
		"description":                      t.Description,
		"roleArn":                          t.RoleArn,
		"creationTime":                     epochSeconds(t.CreationTime),
		"lastUpdateTime":                   epochSeconds(t.LastUpdateTime),
		"stopConditions":                   stopConditionsToWire(t.StopConditions),
		"experimentOptions":                experimentOptionsToWire(t.ExperimentOptions, false),
		"targetAccountConfigurationsCount": 0,
	}

	if actions := templateActionsToWire(t.Actions); actions != nil {
		out["actions"] = actions
	}

	if targets := targetsToWire(t.Targets); targets != nil {
		out["targets"] = targets
	}

	if lc := logConfigurationToWire(t.LogConfiguration); lc != nil {
		out["logConfiguration"] = lc
	}

	putStringMap(out, "tags", t.Tags)

	return out
}

// templateSummaryToWire renders a ListExperimentTemplates response entry.
func templateSummaryToWire(t *driver.ExperimentTemplate) map[string]any {
	out := map[string]any{
		"id":             t.ID,
		"arn":            t.Arn,
		"description":    t.Description,
		"creationTime":   epochSeconds(t.CreationTime),
		"lastUpdateTime": epochSeconds(t.LastUpdateTime),
	}

	putStringMap(out, "tags", t.Tags)

	return out
}
