package postgresflex

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// requestScope builds the subscription/resource-group filter a server must
// live in to be visible under rp's path — a Postgres Flexible Server created
// under one resource group must not resolve, list, or delete under another.
func requestScope(rp *azurearm.ResourcePath) scope.Scope {
	return scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}
}

// lookupInScope resolves the server named in rp and verifies it lives in the
// request's subscription/resource group before any by-name mutation. It writes
// the wire error and returns false when the server is missing (WriteCErr) or
// belongs to a different scope (404 ResourceNotFound), so a caller in one
// resource group can never update, start, stop, restart, or reach the
// sub-resources of another group's server. A server's Scope is fixed at create
// time, so this check-then-act needs no extra lock.
func (h *Handler) lookupInScope(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
) (*rdsdriver.Instance, bool) {
	insts, err := h.db.DescribeInstances(r.Context(), []string{rp.ResourceName})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return nil, false
	}

	if len(insts) == 0 || !insts[0].Scope.Matches(requestScope(rp)) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "server "+rp.ResourceName+" not found")
		return nil, false
	}

	return &insts[0], true
}

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

// instanceFromBody decodes a Postgres Flex create/update body and converts it
// to the portable driver shape.
func instanceFromBody(body *armServer, rp *azurearm.ResourcePath) rdsdriver.InstanceConfig {
	cfg := rdsdriver.InstanceConfig{
		Engine:   "Postgres",
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

		if body.Properties.Storage != nil && body.Properties.Storage.StorageSizeGB > 0 {
			cfg.AllocatedStorage = body.Properties.Storage.StorageSizeGB
		}

		if ha := body.Properties.HighAvailability; ha != nil {
			cfg.HighAvailabilityMode = ha.Mode
			cfg.StandbyAvailabilityZone = ha.StandbyAvailabilityZone
		}
	}

	return cfg
}

func (h *Handler) createOrUpdateServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armServer
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if rejectInvalidHAMode(w, &body) {
		return
	}

	// Restore path: createMode=PointInTimeRestore + sourceServerResourceId.
	if body.Properties != nil && body.Properties.CreateMode == "PointInTimeRestore" {
		h.restoreServer(w, r, rp, &body)
		return
	}

	cfg := instanceFromBody(&body, rp)
	cfg.ID = rp.ResourceName

	// ARM PUT of a new resource returns 201 Created; an in-place update of an
	// existing one returns 200.
	status := http.StatusCreated

	inst, err := h.db.CreateInstance(r.Context(), cfg)
	if err != nil {
		if !cerrors.IsAlreadyExists(err) {
			azurearm.WriteCErr(w, err)
			return
		}

		var ok bool
		if inst, ok = h.upsertOnNameCollision(w, r, rp, &body); !ok {
			return
		}

		status = http.StatusOK
	}

	azurearm.WriteJSON(w, status, toARMServer(inst, rp.Subscription, rp.ResourceGroup))
}

// upsertOnNameCollision resolves a PUT whose server name is already taken.
// Flexible Server names are globally unique (they back a public FQDN), so a
// same-name PUT from a different resource group is a naming conflict — mutating
// the real owner's server would be a cross-tenant write. Only an in-scope
// collision is a legitimate idempotent PUT that applies the body's
// storage/sku/version/HA. It writes the wire error and returns false when the
// caller must stop.
func (h *Handler) upsertOnNameCollision(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, body *armServer,
) (*rdsdriver.Instance, bool) {
	existing, err := h.db.DescribeInstances(r.Context(), []string{rp.ResourceName})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return nil, false
	}

	if len(existing) == 0 || !existing[0].Scope.Matches(requestScope(rp)) {
		azurearm.WriteError(w, http.StatusConflict, "Conflict",
			"server name "+rp.ResourceName+" is already in use")
		return nil, false
	}

	inst, err := h.db.ModifyInstance(r.Context(), rp.ResourceName, modifyInputFromBody(body))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return nil, false
	}

	return inst, true
}

// modifyInputFromBody maps a Postgres Flex server body to the portable modify
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

func (h *Handler) restoreServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, body *armServer) {
	input := rdsdriver.RestoreInstanceInput{
		NewInstanceID: rp.ResourceName,
		SnapshotID:    body.Properties.SourceServerResourceID,
		Tags:          body.Tags,
	}

	if body.SKU != nil {
		input.InstanceClass = body.SKU.Name
	}

	inst, err := h.db.RestoreInstanceFromSnapshot(r.Context(), input)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(inst, rp.Subscription, rp.ResourceGroup))
}

func (h *Handler) updateServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armServer
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if rejectInvalidHAMode(w, &body) {
		return
	}

	if _, ok := h.lookupInScope(w, r, rp); !ok {
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
	inst, ok := h.lookupInScope(w, r, rp)
	if !ok {
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(inst, rp.Subscription, rp.ResourceGroup))
}

// deleteServer handles DELETE — Servers.Delete. When the backend implements
// ScopedDelete (Postgres Flex always does), the scope check and the delete
// happen atomically so a cross-tenant DELETE can never remove another
// resource group's server; otherwise DeleteInstance runs unscoped.
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

		// Render the id from the server's own group, not the request path's
		// (empty on a subscription-scoped list) — so the id carries its true
		// resourceGroups/{rg} segment.
		rg := insts[i].Scope.ResourceGroup
		if rg == "" {
			rg = rp.ResourceGroup
		}

		out = append(out, toARMServer(&insts[i], rp.Subscription, rg))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armServer]{Value: out})
}

func (h *Handler) startServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, ok := h.lookupInScope(w, r, rp); !ok {
		return
	}

	if err := h.db.StartInstance(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) stopServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, ok := h.lookupInScope(w, r, rp); !ok {
		return
	}

	if err := h.db.StopInstance(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) restartServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, ok := h.lookupInScope(w, r, rp); !ok {
		return
	}

	if err := h.db.RebootInstance(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}
