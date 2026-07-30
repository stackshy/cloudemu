package alloydb

import (
	"net/http"

	alloydb "google.golang.org/api/alloydb/v1"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func (h *Handler) serveInstances(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	if p.subID == "" {
		switch r.Method {
		case http.MethodPost:
			h.createInstance(w, r, p)
		case http.MethodGet:
			h.listInstances(w, r, p)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		}

		return
	}

	if p.subAction == actionFailover || p.subAction == actionRestart {
		h.instanceAction(w, r, p)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getInstance(w, r, p)
	case http.MethodPatch:
		h.patchInstance(w, r, p)
	case http.MethodDelete:
		h.deleteInstance(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func (h *Handler) createInstance(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	adb, ok := h.alloyCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB capability not wired")
		return
	}

	var body alloydb.Instance
	if !decodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.AlloyDBInstanceConfig{
		ClusterID:        p.clusterID,
		ID:               r.URL.Query().Get("instanceId"),
		InstanceType:     body.InstanceType,
		AvailabilityType: body.AvailabilityType,
	}

	if body.MachineConfig != nil {
		cfg.CPUCount = int(body.MachineConfig.CpuCount)
	}

	if body.ReadPoolConfig != nil {
		cfg.NodeCount = int(body.ReadPoolConfig.NodeCount)
	}

	inst, err := adb.CreateAlloyDBInstance(r.Context(), cfg)
	if err != nil {
		writeCErr(w, err)
		return
	}

	info, _ := adb.AlloyDBInstanceInfo(r.Context(), p.clusterID, inst.ID)
	writeJSON(w, http.StatusOK, h.doneOperation(p, "create-instance", h.toWireInstance(inst, info)))
}

func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	adb, ok := h.alloyCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB capability not wired")
		return
	}

	all, err := h.db.DescribeInstances(r.Context(), nil)
	if err != nil {
		writeCErr(w, err)
		return
	}

	out := &alloydb.ListInstancesResponse{Instances: []*alloydb.Instance{}}

	for i := range all {
		if all[i].ClusterID != p.clusterID {
			continue
		}

		info, _ := adb.AlloyDBInstanceInfo(r.Context(), p.clusterID, all[i].ID)
		out.Instances = append(out.Instances, h.toWireInstance(&all[i], info))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	adb, ok := h.alloyCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB capability not wired")
		return
	}

	insts, err := h.db.DescribeInstances(r.Context(), []string{p.clusterID + "/" + p.subID})
	if err != nil {
		writeCErr(w, err)
		return
	}

	if len(insts) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "instance "+p.subID+" not found")
		return
	}

	info, _ := adb.AlloyDBInstanceInfo(r.Context(), p.clusterID, p.subID)
	writeJSON(w, http.StatusOK, h.toWireInstance(&insts[0], info))
}

func (h *Handler) patchInstance(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	adb, _ := h.alloyCap()

	var body alloydb.Instance
	if !decodeJSON(w, r, &body) {
		return
	}

	input := rdsdriver.ModifyInstanceInput{Tags: body.Labels}

	inst, err := h.db.ModifyInstance(r.Context(), p.clusterID+"/"+p.subID, input)
	if err != nil {
		writeCErr(w, err)
		return
	}

	info, _ := adb.AlloyDBInstanceInfo(r.Context(), p.clusterID, p.subID)
	writeJSON(w, http.StatusOK, h.doneOperation(p, "update-instance", h.toWireInstance(inst, info)))
}

func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	if err := h.db.DeleteInstance(r.Context(), p.clusterID+"/"+p.subID); err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, h.doneOperation(p, "delete-instance", nil))
}

func (h *Handler) instanceAction(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	adb, ok := h.alloyCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB capability not wired")
		return
	}

	var (
		inst *rdsdriver.Instance
		err  error
	)

	if p.subAction == actionFailover {
		inst, err = adb.FailoverInstance(r.Context(), p.clusterID, p.subID)
	} else {
		inst, err = adb.RestartInstance(r.Context(), p.clusterID, p.subID)
	}

	if err != nil {
		writeCErr(w, err)
		return
	}

	info, _ := adb.AlloyDBInstanceInfo(r.Context(), p.clusterID, p.subID)
	writeJSON(w, http.StatusOK, h.doneOperation(p, p.subAction+"-instance", h.toWireInstance(inst, info)))
}
