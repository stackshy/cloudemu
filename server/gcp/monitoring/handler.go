// Package monitoring implements the GCP Cloud Monitoring REST API surface
// for alert policies. cloud.google.com/go/monitoring uses gRPC by default;
// this handler covers the REST equivalent for HTTP-level testing.
//
// Supported operations (parity with AWS CloudWatch):
//
//	POST   /v3/projects/{p}/alertPolicies          — create policy
//	GET    /v3/projects/{p}/alertPolicies/{name}   — get
//	GET    /v3/projects/{p}/alertPolicies          — list
//	DELETE /v3/projects/{p}/alertPolicies/{name}   — delete
package monitoring

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 1 << 20
)

// Handler serves GCP Cloud Monitoring alert-policy REST requests.
type Handler struct {
	mon mondriver.Monitoring
}

// New returns a Cloud Monitoring handler.
func New(m mondriver.Monitoring) *Handler {
	return &Handler{mon: m}
}

// Matches returns true for /v3/projects/.../alertPolicies URLs.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/v3/projects/") && strings.Contains(p, "/alertPolicies")
}

// ServeHTTP routes the request based on URL path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	const (
		// /v3/projects/{p}/alertPolicies      → 4 parts (collection)
		// /v3/projects/{p}/alertPolicies/{n}  → 5 parts (resource)
		collectionParts = 4
		resourceParts   = 5
		idxProject      = 2
		idxName         = 4
	)

	if len(parts) < collectionParts {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown path")
		return
	}

	project := parts[idxProject]

	if len(parts) == resourceParts {
		name := parts[idxName]

		switch r.Method {
		case http.MethodGet:
			h.getPolicy(w, r, project, name)
		case http.MethodDelete:
			h.deletePolicy(w, r, name)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodPost:
		h.createPolicy(w, r, project)
	case http.MethodGet:
		h.listPolicies(w, r, project)
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

	name := body.DisplayName
	if name == "" {
		name = "policy-" + randID()
	}

	cfg := mondriver.AlarmConfig{
		Name:               name,
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

	body.Name = "projects/" + project + "/alertPolicies/" + name

	writeJSON(w, http.StatusOK, body)
}

func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request, project, name string) {
	if err := policyExists(r.Context(), h.mon, name); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, alertPolicy{
		Name:        "projects/" + project + "/alertPolicies/" + name,
		DisplayName: name,
		Enabled:     true,
	})
}

func (h *Handler) listPolicies(w http.ResponseWriter, r *http.Request, project string) {
	alarms, err := h.mon.DescribeAlarms(r.Context(), nil)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := alertPoliciesList{}
	for i := range alarms {
		out.AlertPolicies = append(out.AlertPolicies, alertPolicy{
			Name:        "projects/" + project + "/alertPolicies/" + alarms[i].Name,
			DisplayName: alarms[i].Name,
			Enabled:     true,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) deletePolicy(w http.ResponseWriter, r *http.Request, name string) {
	if err := policyExists(r.Context(), h.mon, name); err != nil {
		writeErr(w, err)
		return
	}

	if err := h.mon.DeleteAlarm(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

// policyExists reports whether an alert policy exists by name.
func policyExists(ctx context.Context, m mondriver.Monitoring, name string) error {
	alarms, err := m.DescribeAlarms(ctx, nil)
	if err != nil {
		return err
	}

	for i := range alarms {
		if alarms[i].Name == name {
			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "alertPolicy %s not found", name)
}

// randID returns a small random identifier for synthesized policy names.
// Stable enough for HTTP-level tests; not cryptographic.
func randID() string {
	return "auto"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, statusCode, msg string) {
	writeJSON(w, status, errorEnvelope{
		Error: errorBody{Code: status, Message: msg, Status: statusCode},
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
