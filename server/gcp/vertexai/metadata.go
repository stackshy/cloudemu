package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

// --- Metadata stores ---

func metadataStoreJSON(s *driver.MetadataStore) map[string]any {
	return map[string]any{"name": s.Name, "createTime": s.CreateTime}
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveMetadataStores(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listMetadataStores(w, r, p.location)
		case http.MethodPost:
			h.createMetadataStore(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getMetadataStore(w, r, p.name)
	case http.MethodDelete:
		h.deleteMetadataStore(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createMetadataStore(w http.ResponseWriter, r *http.Request, location string) {
	if !decode(w, r, &struct{}{}) {
		return
	}

	op, s, err := h.svc.CreateMetadataStore(r.Context(), location, r.URL.Query().Get("metadataStoreId"))
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, metadataStoreJSON(s), "MetadataStore")
}

func (h *Handler) getMetadataStore(w http.ResponseWriter, r *http.Request, name string) {
	s, err := h.svc.GetMetadataStore(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, metadataStoreJSON(s))
}

func (h *Handler) listMetadataStores(w http.ResponseWriter, r *http.Request, location string) {
	ss, err := h.svc.ListMetadataStores(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(ss))
	for i := range ss {
		out = append(out, metadataStoreJSON(&ss[i]))
	}

	writeJSON(w, map[string]any{"metadataStores": out})
}

func (h *Handler) deleteMetadataStore(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteMetadataStore(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

// --- Tensorboards ---

func tensorboardJSON(t *driver.Tensorboard) map[string]any {
	return map[string]any{"name": t.Name, "displayName": t.DisplayName, "createTime": t.CreateTime}
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveTensorboards(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listTensorboards(w, r, p.location)
		case http.MethodPost:
			h.createTensorboard(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getTensorboard(w, r, p.name)
	case http.MethodDelete:
		h.deleteTensorboard(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createTensorboard(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName string `json:"displayName"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, tb, err := h.svc.CreateTensorboard(r.Context(), location, req.DisplayName)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, tensorboardJSON(tb), "Tensorboard")
}

func (h *Handler) getTensorboard(w http.ResponseWriter, r *http.Request, name string) {
	t, err := h.svc.GetTensorboard(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, tensorboardJSON(t))
}

func (h *Handler) listTensorboards(w http.ResponseWriter, r *http.Request, location string) {
	ts, err := h.svc.ListTensorboards(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(ts))
	for i := range ts {
		out = append(out, tensorboardJSON(&ts[i]))
	}

	writeJSON(w, map[string]any{"tensorboards": out})
}

func (h *Handler) deleteTensorboard(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteTensorboard(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

// --- Schedules ---

func scheduleJSON(s *driver.Schedule) map[string]any {
	return map[string]any{
		"name": s.Name, "displayName": s.DisplayName, "cron": s.Cron,
		"state": s.State, "createTime": s.CreateTime,
	}
}

func (h *Handler) serveSchedules(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listSchedules(w, r, p.location)
		case http.MethodPost:
			h.createSchedule(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action != "" {
		h.scheduleAction(w, r, p.name, p.action)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getSchedule(w, r, p.name)
	case http.MethodDelete:
		h.deleteSchedule(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) scheduleAction(w http.ResponseWriter, r *http.Request, name, action string) {
	var err error

	switch action {
	case "pause":
		err = h.svc.PauseSchedule(r.Context(), name)
	case "resume":
		err = h.svc.ResumeSchedule(r.Context(), name)
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown schedule action: "+action)

		return
	}

	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) createSchedule(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName string `json:"displayName"`
		Cron        string `json:"cron"`
	}

	if !decode(w, r, &req) {
		return
	}

	s, err := h.svc.CreateSchedule(r.Context(), location, req.DisplayName, req.Cron)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, scheduleJSON(s))
}

func (h *Handler) getSchedule(w http.ResponseWriter, r *http.Request, name string) {
	s, err := h.svc.GetSchedule(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, scheduleJSON(s))
}

func (h *Handler) listSchedules(w http.ResponseWriter, r *http.Request, location string) {
	ss, err := h.svc.ListSchedules(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(ss))
	for i := range ss {
		out = append(out, scheduleJSON(&ss[i]))
	}

	writeJSON(w, map[string]any{"schedules": out})
}

func (h *Handler) deleteSchedule(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteSchedule(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

// --- Notebook runtime templates ---

func nbTemplateJSON(t *driver.NotebookRuntimeTemplate) map[string]any {
	return map[string]any{
		"name": t.Name, "displayName": t.DisplayName,
		"machineSpec": map[string]any{"machineType": t.MachineType}, "createTime": t.CreateTime,
	}
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveNotebookRuntimeTemplates(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listNotebookRuntimeTemplates(w, r, p.location)
		case http.MethodPost:
			h.createNotebookRuntimeTemplate(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getNotebookRuntimeTemplate(w, r, p.name)
	case http.MethodDelete:
		h.deleteNotebookRuntimeTemplate(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createNotebookRuntimeTemplate(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName string `json:"displayName"`
		MachineSpec struct {
			MachineType string `json:"machineType"`
		} `json:"machineSpec"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, t, err := h.svc.CreateNotebookRuntimeTemplate(r.Context(), location, req.DisplayName, req.MachineSpec.MachineType)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, nbTemplateJSON(t), "NotebookRuntimeTemplate")
}

func (h *Handler) getNotebookRuntimeTemplate(w http.ResponseWriter, r *http.Request, name string) {
	t, err := h.svc.GetNotebookRuntimeTemplate(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, nbTemplateJSON(t))
}

func (h *Handler) listNotebookRuntimeTemplates(w http.ResponseWriter, r *http.Request, location string) {
	ts, err := h.svc.ListNotebookRuntimeTemplates(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(ts))
	for i := range ts {
		out = append(out, nbTemplateJSON(&ts[i]))
	}

	writeJSON(w, map[string]any{"notebookRuntimeTemplates": out})
}

func (h *Handler) deleteNotebookRuntimeTemplate(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteNotebookRuntimeTemplate(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

// --- Notebook runtimes ---

func nbRuntimeJSON(n *driver.NotebookRuntime) map[string]any {
	return map[string]any{
		"name": n.Name, "displayName": n.DisplayName,
		"runtimeState": n.RuntimeState, "createTime": n.CreateTime,
	}
}

func (h *Handler) serveNotebookRuntimes(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch {
		case r.Method == http.MethodGet:
			h.listNotebookRuntimes(w, r, p.location)
		case r.Method == http.MethodPost && p.action == "assign":
			h.assignNotebookRuntime(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action != "" {
		h.notebookRuntimeAction(w, r, p.name, p.action)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getNotebookRuntime(w, r, p.name)
	case http.MethodDelete:
		h.deleteNotebookRuntime(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) notebookRuntimeAction(w http.ResponseWriter, r *http.Request, name, action string) {
	var (
		op  *driver.Operation
		err error
	)

	switch action {
	case "start":
		op, err = h.svc.StartNotebookRuntime(r.Context(), name)
	case "stop":
		op, err = h.svc.StopNotebookRuntime(r.Context(), name)
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown notebookRuntime action: "+action)

		return
	}

	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func (h *Handler) assignNotebookRuntime(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		NotebookRuntime struct {
			DisplayName string `json:"displayName"`
		} `json:"notebookRuntime"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, nr, err := h.svc.AssignNotebookRuntime(r.Context(), location, req.NotebookRuntime.DisplayName)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, nbRuntimeJSON(nr), "NotebookRuntime")
}

func (h *Handler) getNotebookRuntime(w http.ResponseWriter, r *http.Request, name string) {
	n, err := h.svc.GetNotebookRuntime(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, nbRuntimeJSON(n))
}

func (h *Handler) listNotebookRuntimes(w http.ResponseWriter, r *http.Request, location string) {
	ns, err := h.svc.ListNotebookRuntimes(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(ns))
	for i := range ns {
		out = append(out, nbRuntimeJSON(&ns[i]))
	}

	writeJSON(w, map[string]any{"notebookRuntimes": out})
}

func (h *Handler) deleteNotebookRuntime(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteNotebookRuntime(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}
