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
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 1 << 20
)

// Handler serves GCP Cloud Monitoring alert-policy REST requests.
//
// The portable monitoring driver models a threshold Alarm, not GCP's richer
// alert-policy shape (conditions, combiner, notificationChannels, userLabels).
// To avoid dropping those on read, the full policy is held here keyed by name;
// the driver alarm is kept as an existence marker.
type Handler struct {
	mon mondriver.Monitoring

	mu       sync.RWMutex
	policies map[string]alertPolicy // keyed by policy short-name
	seq      atomic.Uint64
}

// New returns a Cloud Monitoring handler.
func New(m mondriver.Monitoring) *Handler {
	return &Handler{mon: m, policies: make(map[string]alertPolicy)}
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
		case http.MethodPatch, http.MethodPut:
			h.patchPolicy(w, r, project, name)
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
		name = "policy-" + strconv.FormatUint(h.seq.Add(1), 10)
	}

	// The driver alarm is only an existence marker; the full policy shape lives
	// in the handler registry so conditions/combiner/labels round-trip.
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

	body.Name = policyResourceName(project, name)

	h.mu.Lock()
	h.policies[name] = body
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, body)
}

func (h *Handler) getPolicy(w http.ResponseWriter, _ *http.Request, project, name string) {
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

	for name := range h.policies {
		pol := h.policies[name]
		pol.Name = policyResourceName(project, name)
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
