package mysqlflex

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// requestScope builds the subscription/resource-group filter a server must
// live in to be visible under rp's path — a MySQL Flexible Server created
// under one resource group must not resolve, list, or delete under another.
func requestScope(rp *azurearm.ResourcePath) scope.Scope {
	return scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}
}

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
		Scope:    requestScope(rp),
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

// getServer handles GET on a single server — Servers.Get. The driver keys
// servers by name alone, so the handler enforces the request's resource-group
// scope: a server created in one subscription/resource group must not resolve
// under a different one in the URL (real ARM answers 404, since the id would
// contradict the request path).
func (h *Handler) getServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	insts, err := h.db.DescribeInstances(r.Context(), []string{rp.ResourceName})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if len(insts) == 0 || !insts[0].Scope.Matches(requestScope(rp)) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "server "+rp.ResourceName+" not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(&insts[0], rp.Subscription, rp.ResourceGroup))
}

// deleteServer handles DELETE — Servers.Delete. When the backend implements
// ScopedDelete (MySQL Flex always does), the scope check and the delete happen
// atomically so a cross-tenant DELETE can never remove another resource
// group's server; otherwise DeleteInstance runs unscoped.
func (h *Handler) deleteServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var err error

	if sd, ok := h.db.(rdsdriver.ScopedDelete); ok {
		err = sd.DeleteInstanceInScope(r.Context(), rp.ResourceName, requestScope(rp))
	} else {
		err = h.db.DeleteInstance(r.Context(), rp.ResourceName)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// listServers handles GET on the collection — Servers.ListByResourceGroup /
// ListBySubscription. The filter carries the path's subscription and, for
// RG-level lists, its resource group; subscription-level lists leave the
// resource group empty so the filter spans the subscription's groups.
func (h *Handler) listServers(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	insts, err := h.db.DescribeInstances(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	filter := requestScope(rp)

	out := make([]armServer, 0, len(insts))

	for i := range insts {
		if !insts[i].Scope.Matches(filter) {
			continue
		}

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

// restartWithFailoverEnabled is the ServerRestartParameter.restartWithFailover
// EnableStatusEnum value that routes a restart through failover to the
// standby instead of a plain in-place restart.
const restartWithFailoverEnabled = "Enabled"

// restartServer handles POST .../restart — Servers.BeginRestart. The request
// body is a ServerRestartParameter: restartWithFailover=="Enabled" routes the
// restart through FailoverInstance (so it inherits the standby precondition)
// instead of a plain in-place reboot; maxFailoverSeconds bounds the SDK
// poller's wait and is accepted without further effect on the synchronous mock.
func (h *Handler) restartServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armServerRestartParameter
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if body.RestartWithFailover == restartWithFailoverEnabled {
		fo, ok := h.failoverCap()
		if !ok {
			writeUnsupported(w, "restartWithFailover")
			return
		}

		if err := fo.FailoverInstance(r.Context(), rp.ResourceName); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		h.respondWithServer(w, r, rp)

		return
	}

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
