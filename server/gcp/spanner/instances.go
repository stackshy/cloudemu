package spanner

import (
	"net/http"
	"strings"
	"time"

	sp "google.golang.org/api/spanner/v1"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	spdriver "github.com/stackshy/cloudemu/v2/services/spanner/driver"
)

// formatTime renders t as RFC3339Nano; a zero time renders as the empty string.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}

func toWireInstance(in *spdriver.Instance) *sp.Instance {
	return &sp.Instance{
		Name:            in.Name,
		Config:          in.Config,
		DisplayName:     in.DisplayName,
		NodeCount:       in.NodeCount,
		ProcessingUnits: in.ProcessingUnits,
		State:           in.State,
		Labels:          in.Labels,
		CreateTime:      formatTime(in.CreateTime),
		UpdateTime:      formatTime(in.UpdateTime),
	}
}

func (h *Handler) serveInstanceCollection(w http.ResponseWriter, r *http.Request, project string) {
	switch r.Method {
	case http.MethodPost:
		h.createInstance(w, r, project)
	case http.MethodGet:
		h.listInstances(w, r, project)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createInstance(w http.ResponseWriter, r *http.Request, project string) {
	var body sp.CreateInstanceRequest
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	if body.InstanceId == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "instanceId is required")
		return
	}

	cfg := spdriver.CreateInstanceConfig{Name: instanceName(project, body.InstanceId)}

	if body.Instance != nil {
		cfg.Config = body.Instance.Config
		cfg.DisplayName = body.Instance.DisplayName
		cfg.NodeCount = body.Instance.NodeCount
		cfg.ProcessingUnits = body.Instance.ProcessingUnits
		cfg.Labels = body.Instance.Labels
	}

	inst, op, err := h.db.CreateInstance(r.Context(), cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(op.Name, toWireInstance(inst)))
}

func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request, project string) {
	instances, err := h.db.ListInstances(r.Context(), project)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, &sp.ListInstancesResponse{Instances: mapWire(instances, toWireInstance)})
}

func (h *Handler) serveInstanceItem(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getInstance(w, r, name)
	case http.MethodPatch:
		h.patchInstance(w, r, name)
	case http.MethodDelete:
		h.deleteInstance(w, r, name)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request, name string) {
	inst, err := h.db.GetInstance(r.Context(), name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireInstance(inst))
}

func (h *Handler) patchInstance(w http.ResponseWriter, r *http.Request, name string) {
	var body sp.UpdateInstanceRequest
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	cfg := spdriver.UpdateInstanceConfig{FieldMask: normalizeMask(body.FieldMask)}

	if body.Instance != nil {
		cfg.DisplayName = body.Instance.DisplayName
		cfg.NodeCount = body.Instance.NodeCount
		cfg.ProcessingUnits = body.Instance.ProcessingUnits
		cfg.Labels = body.Instance.Labels
	}

	inst, op, err := h.db.UpdateInstance(r.Context(), name, cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(op.Name, toWireInstance(inst)))
}

func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.db.DeleteInstance(r.Context(), name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

// normalizeMask splits a comma-separated field mask into lowercase, underscore-
// stripped tokens so "displayName", "display_name", and "displayname" all match.
func normalizeMask(mask string) []string {
	if mask == "" {
		return nil
	}

	parts := strings.Split(mask, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		tok := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(p)), "_", "")
		if tok != "" {
			out = append(out, tok)
		}
	}

	return out
}
