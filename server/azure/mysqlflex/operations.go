package mysqlflex

import (
	"net/http"

	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// ---- Server lifecycle ----

func (h *Handler) createServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armServer
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.InstanceConfig{
		ID:               rp.ResourceName,
		Engine:           "MySQL",
		AvailabilityZone: body.Location,
		Tags:             body.Tags,
	}

	if body.SKU != nil {
		cfg.InstanceClass = body.SKU.Name
	}

	if body.Properties != nil {
		cfg.MasterUsername = body.Properties.AdministratorLogin
		cfg.MasterUserPassword = body.Properties.AdministratorLoginPassword
		cfg.EngineVersion = body.Properties.Version

		if body.Properties.Storage != nil {
			cfg.AllocatedStorage = body.Properties.Storage.StorageSizeGB
			cfg.StorageType = body.Properties.Storage.StorageSKU
		}
	}

	inst, err := h.db.CreateInstance(r.Context(), cfg)
	if err != nil {
		// Idempotent PUT: if the server already exists, fall back to a get.
		existing, getErr := h.db.DescribeInstances(r.Context(), []string{rp.ResourceName})
		if getErr != nil || len(existing) != 1 {
			azurearm.WriteCErr(w, err)
			return
		}

		inst = &existing[0]
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(inst, rp.Subscription, rp.ResourceGroup))
}

func (h *Handler) updateServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armServer
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	input := rdsdriver.ModifyInstanceInput{
		Tags: body.Tags,
	}

	if body.SKU != nil {
		input.InstanceClass = body.SKU.Name
	}

	if body.Properties != nil {
		input.EngineVersion = body.Properties.Version

		if body.Properties.Storage != nil && body.Properties.Storage.StorageSizeGB > 0 {
			input.AllocatedStorage = body.Properties.Storage.StorageSizeGB
		}
	}

	inst, err := h.db.ModifyInstance(r.Context(), rp.ResourceName, input)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(inst, rp.Subscription, rp.ResourceGroup))
}

func (h *Handler) getServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	insts, err := h.db.DescribeInstances(r.Context(), []string{rp.ResourceName})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(&insts[0], rp.Subscription, rp.ResourceGroup))
}

func (h *Handler) deleteServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.DeleteInstance(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listServers(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	insts, err := h.db.DescribeInstances(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armServer, 0, len(insts))
	for i := range insts {
		out = append(out, toARMServer(&insts[i], rp.Subscription, rp.ResourceGroup))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armServer]{Value: out})
}

// ---- Action sub-resources ----

func (h *Handler) startServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.StartInstance(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.respondWithServer(w, r, rp)
}

func (h *Handler) stopServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.StopInstance(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.respondWithServer(w, r, rp)
}

func (h *Handler) restartServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.RebootInstance(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.respondWithServer(w, r, rp)
}

// respondWithServer fetches the current server state and writes it as the
// action response so the SDK's LRO poller observes a typed body.
func (h *Handler) respondWithServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	insts, err := h.db.DescribeInstances(r.Context(), []string{rp.ResourceName})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(&insts[0], rp.Subscription, rp.ResourceGroup))
}
