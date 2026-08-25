package monitoring

import (
	"encoding/json"
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// serveAlertPolicies routes /v3/projects/{p}/alertPolicies[/{id}].
func (h *Handler) serveAlertPolicies(w http.ResponseWriter, r *http.Request, project string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createPolicy(w, r, project)
		case http.MethodGet:
			h.listPolicies(w, r, project)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}

		return
	}

	name := rest[0]

	switch r.Method {
	case http.MethodGet:
		h.getPolicy(w, project, name)
	case http.MethodPatch, http.MethodPut:
		h.patchPolicy(w, r, project, name)
	case http.MethodDelete:
		h.deletePolicy(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) createPolicy(w http.ResponseWriter, r *http.Request, project string) {
	var body alertPolicy

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}

	// Real Cloud Monitoring assigns an opaque numeric id, not one derived from
	// displayName — two policies sharing a displayName must not collapse.
	id := strconv.FormatUint(h.seq.Add(1), 10)

	// The driver alarm is only an existence marker; the full policy shape lives
	// in the handler registry so conditions/combiner/labels round-trip.
	cfg := mondriver.AlarmConfig{
		Name:               id,
		Namespace:          "gcp",
		MetricName:         "metric",
		ComparisonOperator: "GreaterThanThreshold",
		Threshold:          0,
		Period:             60,
		EvaluationPeriods:  1,
		Stat:               "Average",
	}

	if err := h.mon.CreateAlarm(r.Context(), cfg); err != nil {
		writeErr(w, err)
		return
	}

	body.Name = policyResourceName(project, id)
	now := nowRFC3339()
	rec := &mutationRecord{MutateTime: now, MutatedBy: "cloudemu"}
	body.CreationRecord = rec
	body.MutationRecord = rec
	nameConditions(&body, project, id)

	h.mu.Lock()
	h.policies[id] = body
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, body)
}

// nameConditions assigns each condition its canonical resource name, matching
// real Cloud Monitoring which populates conditions[].name on create.
func nameConditions(pol *alertPolicy, project, policyID string) {
	for i := range pol.Conditions {
		if pol.Conditions[i].Name == "" {
			pol.Conditions[i].Name = policyResourceName(project, policyID) +
				"/conditions/" + strconv.Itoa(i)
		}
	}
}

func (h *Handler) getPolicy(w http.ResponseWriter, project, name string) {
	h.mu.RLock()
	pol, ok := h.policies[name]
	h.mu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "alertPolicy "+name+" not found")
		return
	}

	pol.Name = policyResourceName(project, name)

	writeJSON(w, http.StatusOK, pol)
}

func (h *Handler) listPolicies(w http.ResponseWriter, _ *http.Request, project string) {
	h.mu.RLock()
	out := alertPoliciesList{AlertPolicies: make([]alertPolicy, 0, len(h.policies))}

	for id := range h.policies {
		pol := h.policies[id]
		pol.Name = policyResourceName(project, id)
		out.AlertPolicies = append(out.AlertPolicies, pol)
	}
	h.mu.RUnlock()

	writeJSON(w, http.StatusOK, out)
}

// patchPolicy applies a partial update. GCP scopes changes by updateMask; the
// pragmatic emulation overwrites any field the caller supplied (non-zero),
// which covers displayName/combiner/enabled/conditions/labels/channels.
func (h *Handler) patchPolicy(w http.ResponseWriter, r *http.Request, project, name string) {
	// Decode with a pointer Enabled so an omitted "enabled" is distinguishable
	// from an explicit false — a partial PATCH must leave it unchanged, not
	// silently disable the policy (real GCP applies only the updateMask paths).
	var body struct {
		DisplayName          string            `json:"displayName"`
		Combiner             string            `json:"combiner"`
		Conditions           []alertCondition  `json:"conditions"`
		UserLabels           map[string]string `json:"userLabels"`
		NotificationChannels []string          `json:"notificationChannels"`
		Enabled              *bool             `json:"enabled"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}

	h.mu.Lock()

	cur, ok := h.policies[name]
	if !ok {
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, "NOT_FOUND", "alertPolicy "+name+" not found")

		return
	}

	if body.DisplayName != "" {
		cur.DisplayName = body.DisplayName
	}

	if body.Combiner != "" {
		cur.Combiner = body.Combiner
	}

	if body.Conditions != nil {
		cur.Conditions = body.Conditions
		nameConditions(&cur, project, name)
	}

	if body.UserLabels != nil {
		cur.UserLabels = body.UserLabels
	}

	if body.NotificationChannels != nil {
		cur.NotificationChannels = body.NotificationChannels
	}

	if body.Enabled != nil {
		cur.Enabled = *body.Enabled
	}

	cur.MutationRecord = &mutationRecord{MutateTime: nowRFC3339(), MutatedBy: "cloudemu"}
	h.policies[name] = cur
	h.mu.Unlock()

	cur.Name = policyResourceName(project, name)

	writeJSON(w, http.StatusOK, cur)
}

func (h *Handler) deletePolicy(w http.ResponseWriter, r *http.Request, name string) {
	h.mu.Lock()
	_, ok := h.policies[name]
	delete(h.policies, name)
	h.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "alertPolicy "+name+" not found")
		return
	}

	if err := h.mon.DeleteAlarm(r.Context(), name); err != nil && !cerrors.IsNotFound(err) {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

func policyResourceName(project, name string) string {
	return "projects/" + project + "/alertPolicies/" + name
}
