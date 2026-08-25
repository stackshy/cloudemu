package monitoring

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// serveMetricDescriptors routes /v3/projects/{p}/metricDescriptors[/{type...}].
// A metric type contains slashes, so the resource id is the remaining path.
func (h *Handler) serveMetricDescriptors(w http.ResponseWriter, r *http.Request, project string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			h.listDescriptors(w, project)
		case http.MethodPost:
			h.createDescriptor(w, r, project)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}

		return
	}

	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	h.getDescriptor(w, project, strings.Join(rest, "/"))
}

func (h *Handler) listDescriptors(w http.ResponseWriter, project string) {
	byType := map[string]metricDescriptor{}

	// Descriptors synthesized from series that currently hold data.
	if reader, ok := h.mon.(seriesReader); ok {
		for _, key := range reader.GCPSeriesKeys() {
			t := metricType(key.Namespace, key.MetricName)
			byType[t] = synthDescriptor(project, t)
		}
	}

	// Custom descriptors created via metricDescriptors.create take precedence.
	h.mu.RLock()
	for t := range h.descriptors {
		d := h.descriptors[t]
		d.Name = descriptorResourceName(project, t)
		byType[t] = d
	}
	h.mu.RUnlock()

	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}

	sort.Strings(types)

	out := metricDescriptorsList{MetricDescriptors: make([]metricDescriptor, 0, len(types))}
	for _, t := range types {
		out.MetricDescriptors = append(out.MetricDescriptors, byType[t])
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getDescriptor(w http.ResponseWriter, project, mtype string) {
	h.mu.RLock()
	d, ok := h.descriptors[mtype]
	h.mu.RUnlock()

	if ok {
		d.Name = descriptorResourceName(project, mtype)
		writeJSON(w, http.StatusOK, d)

		return
	}

	if reader, rok := h.mon.(seriesReader); rok {
		for _, key := range reader.GCPSeriesKeys() {
			if metricType(key.Namespace, key.MetricName) == mtype {
				writeJSON(w, http.StatusOK, synthDescriptor(project, mtype))
				return
			}
		}
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "metricDescriptor "+mtype+" not found")
}

func (h *Handler) createDescriptor(w http.ResponseWriter, r *http.Request, project string) {
	var body metricDescriptor

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}

	if body.Type == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "type is required")
		return
	}

	if body.MetricKind == "" {
		body.MetricKind = "GAUGE"
	}

	if body.ValueType == "" {
		body.ValueType = "DOUBLE"
	}

	body.Name = descriptorResourceName(project, body.Type)

	h.mu.Lock()
	h.descriptors[body.Type] = body
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, body)
}

func synthDescriptor(project, mtype string) metricDescriptor {
	return metricDescriptor{
		Name:       descriptorResourceName(project, mtype),
		Type:       mtype,
		MetricKind: "GAUGE",
		ValueType:  "DOUBLE",
		Labels:     []descriptorLabel{{Key: "instance_id", ValueType: "STRING"}},
	}
}

func descriptorResourceName(project, mtype string) string {
	return "projects/" + project + "/metricDescriptors/" + mtype
}
