package bigtable

import (
	"net/http"

	bt "google.golang.org/api/bigtableadmin/v2"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

func (h *Handler) serveInstances(w http.ResponseWriter, r *http.Request, rt *route) {
	if !rt.isResource {
		switch r.Method {
		case http.MethodPost:
			h.createInstance(w, r, rt)
		case http.MethodGet:
			h.listInstances(w, r, rt)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if rt.verb != "" {
		if !h.serveIamVerb(w, r, rt.name, rt.verb) {
			gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported verb: "+rt.verb)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getInstance(w, r, rt)
	case http.MethodPut:
		h.updateInstance(w, r, rt)
	case http.MethodPatch:
		h.partialUpdateInstance(w, r, rt)
	case http.MethodDelete:
		h.deleteInstance(w, r, rt)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createInstance(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.CreateInstanceRequest
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	name := rt.parent + "/instances/" + in.InstanceId

	cfg := btdriver.CreateInstanceConfig{Name: name}
	if in.Instance != nil {
		cfg.DisplayName = in.Instance.DisplayName
		cfg.Type = in.Instance.Type
		cfg.Labels = in.Instance.Labels
	}

	for id := range in.Clusters {
		cc := in.Clusters[id]
		cfg.Clusters = append(cfg.Clusters, clusterConfig(name+"/clusters/"+id, &cc))
	}

	inst, op, err := h.db.CreateInstance(r.Context(), cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOp(op, toWireInstance(inst)))
}

func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request, rt *route) {
	inst, err := h.db.GetInstance(r.Context(), rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireInstance(inst))
}

func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request, rt *route) {
	insts, err := h.db.ListInstances(r.Context(), lastSegment(rt.parent))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, next, ok := paginate(w, r, insts)
	if !ok {
		return
	}

	out := &bt.ListInstancesResponse{NextPageToken: next}
	for i := range page {
		out.Instances = append(out.Instances, toWireInstance(&page[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) updateInstance(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.Instance
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	inst, err := h.db.UpdateInstance(r.Context(), rt.name, btdriver.UpdateInstanceConfig{
		DisplayName: in.DisplayName, Type: in.Type, Labels: in.Labels,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireInstance(inst))
}

func (h *Handler) partialUpdateInstance(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.Instance
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	cfg := btdriver.UpdateInstanceConfig{DisplayName: in.DisplayName, Type: in.Type, Labels: in.Labels}
	if mask := parseFieldMask(r.URL.Query().Get("updateMask")); mask != nil {
		cfg.UpdateMask = mask.paths
	}

	inst, op, err := h.db.PartialUpdateInstance(r.Context(), rt.name, cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOp(op, toWireInstance(inst)))
}

func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteInstance(r.Context(), rt.name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}
