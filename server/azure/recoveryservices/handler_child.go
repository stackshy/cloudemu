package recoveryservices

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/providers/azure/recoveryservices"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// servePolicy routes the nested backupPolicies collection. A missing
// SubResourceName lists the collection; otherwise the named policy is served.
func (h *Handler) servePolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		h.listPolicies(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createPolicy(w, r, rp)
	case http.MethodGet:
		h.getPolicy(w, r, rp)
	case http.MethodDelete:
		h.deletePolicy(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createPolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req policyRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	p, created, err := h.store.CreateOrUpdatePolicy(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, req.Properties)
	if err != nil {
		writeChildErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toPolicyResponse(&p))
}

func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	p, err := h.store.GetPolicy(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toPolicyResponse(&p))
}

func (h *Handler) deletePolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeletePolicy(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listPolicies(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	items, err := h.store.ListPolicies(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := policyListResponse{Value: make([]policyResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toPolicyResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// serveVaultConfig routes the singleton backupconfig surface (GET, PUT, PATCH).
// The trailing SubResourceName (vaultconfig) is a fixed singleton name and is not
// used to key storage.
func (h *Handler) serveVaultConfig(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	serveSingletonConfig(w, r, rp, h.store.GetVaultConfig, h.store.UpdateVaultConfig, writeVaultConfig)
}

// serveStorageConfig routes the singleton backupstorageconfig surface (GET,
// PATCH — matching real ARM, which exposes only get/update here).
func (h *Handler) serveStorageConfig(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	serveSingletonConfig(w, r, rp, h.store.GetStorageConfig, h.store.UpdateStorageConfig, writeStorageConfig)
}

// serveSingletonConfig serves the shared GET / PUT-PATCH surface of a per-vault
// config singleton, dispatching to the type-specific get/update store calls and
// the wire writer.
func serveSingletonConfig[T any](
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	get func(context.Context, string, string, string) (T, error),
	update func(context.Context, string, string, string, json.RawMessage) (T, error),
	write func(http.ResponseWriter, *T, int),
) {
	switch r.Method {
	case http.MethodGet:
		c, err := get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		write(w, &c, http.StatusOK)
	case http.MethodPut, http.MethodPatch:
		var req configRequest
		if !azurearm.DecodeJSON(w, r, &req) {
			return
		}

		c, err := update(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Properties)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		write(w, &c, http.StatusOK)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// writeVaultConfig writes the backup vault config ARM response.
func writeVaultConfig(w http.ResponseWriter, c *recoveryservices.Config, status int) {
	azurearm.WriteJSON(w, status, toConfigResponse(c.ARMID(), vaultConfigName, configArmType, c.Etag, c.Properties))
}

// writeStorageConfig writes the backup storage config ARM response.
func writeStorageConfig(w http.ResponseWriter, c *recoveryservices.Config, status int) {
	azurearm.WriteJSON(w, status, toConfigResponse(c.ARMID(), vaultStorageConfigName, storageConfigArmType, c.Etag, c.Properties))
}
