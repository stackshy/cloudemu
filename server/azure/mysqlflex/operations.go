package mysqlflex

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// ---- Server lifecycle ----

// rejectInvalidHAMode writes a 400 and returns true when the body carries a
// highAvailability.mode that is not a recognized enum value — real Azure
// rejects a bogus mode rather than storing it.
func rejectInvalidHAMode(w http.ResponseWriter, body *armServer) bool {
	if body.Properties == nil || body.Properties.HighAvailability == nil {
		return false
	}

	mode := body.Properties.HighAvailability.Mode
	if rdsdriver.ValidHAMode(mode) {
		return false
	}

	azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameterValue",
		"invalid highAvailability.mode: "+mode)

	return true
}

func (h *Handler) createServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armServer
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if rejectInvalidHAMode(w, &body) {
		return
	}

	cfg := rdsdriver.InstanceConfig{
		ID:       rp.ResourceName,
		Engine:   "MySQL",
		Location: body.Location,
		Tags:     body.Tags,
	}

	if body.SKU != nil {
		cfg.InstanceClass = body.SKU.Name
	}

	if body.Properties != nil {
		cfg.MasterUsername = body.Properties.AdministratorLogin
		cfg.MasterUserPassword = body.Properties.AdministratorLoginPassword
		cfg.EngineVersion = body.Properties.Version
		cfg.AvailabilityZone = body.Properties.AvailabilityZone

		if body.Properties.Storage != nil {
			cfg.AllocatedStorage = body.Properties.Storage.StorageSizeGB
			cfg.StorageType = body.Properties.Storage.StorageSKU
		}

		if ha := body.Properties.HighAvailability; ha != nil {
			cfg.HighAvailabilityMode = ha.Mode
			cfg.StandbyAvailabilityZone = ha.StandbyAvailabilityZone
		}
	}

	inst, err := h.db.CreateInstance(r.Context(), cfg)
	if err != nil {
		if !cerrors.IsAlreadyExists(err) {
			azurearm.WriteCErr(w, err)
			return
		}

		// Idempotent PUT: a create against an existing server applies the body's
		// storage/sku/version/HA rather than returning the stale record.
		inst, err = h.db.ModifyInstance(r.Context(), rp.ResourceName, modifyInputFromBody(&body))
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(inst, rp.Subscription, rp.ResourceGroup))
}

// modifyInputFromBody maps a MySQL Flex server body to the portable modify
// input. Shared by PATCH (updateServer) and the re-PUT upsert path.
func modifyInputFromBody(body *armServer) rdsdriver.ModifyInstanceInput {
	input := rdsdriver.ModifyInstanceInput{Tags: body.Tags}

	if body.SKU != nil {
		input.InstanceClass = body.SKU.Name
	}

	if body.Properties != nil {
		input.EngineVersion = body.Properties.Version

		if body.Properties.Storage != nil && body.Properties.Storage.StorageSizeGB > 0 {
			input.AllocatedStorage = body.Properties.Storage.StorageSizeGB
		}

		if ha := body.Properties.HighAvailability; ha != nil {
			input.HighAvailabilityMode = ha.Mode
			input.StandbyAvailabilityZone = ha.StandbyAvailabilityZone
		}
	}

	return input
}

func (h *Handler) updateServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armServer
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if rejectInvalidHAMode(w, &body) {
		return
	}

	inst, err := h.db.ModifyInstance(r.Context(), rp.ResourceName, modifyInputFromBody(&body))
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

	if len(insts) == 0 {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "server "+rp.ResourceName+" not found")
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
// action response so the SDK's LRO poller observes a typed body. It is the same
// fetch-and-write as getServer.
func (h *Handler) respondWithServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	h.getServer(w, r, rp)
}
