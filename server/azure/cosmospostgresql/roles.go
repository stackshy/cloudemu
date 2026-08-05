package cosmospostgresql

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

func (h *Handler) serveRoles(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	serveCRUD(w, r, rp, crudHandlers{
		put:  h.createRole,
		get:  h.getRole,
		del:  h.deleteRole,
		list: h.listRoles,
	})
}

func (h *Handler) createRole(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body roleResource
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := cpgdriver.CreateRoleConfig{
		ResourceGroup: rp.ResourceGroup,
		ClusterName:   rp.ResourceName,
		Name:          rp.SubResourceName,
	}
	if p := body.Properties; p != nil {
		cfg.Password = p.Password
	}

	role, err := h.db.CreateRole(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMRole(role, h.childID(rp, subRoles, role.Name)))
}

func (h *Handler) getRole(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	role, err := h.db.GetRole(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMRole(role, h.childID(rp, subRoles, role.Name)))
}

func (h *Handler) deleteRole(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.DeleteRole(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listRoles(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	roles, err := h.db.ListRoles(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, armListOf(roles, func(role *cpgdriver.Role) roleResource {
		return toARMRole(role, h.childID(rp, subRoles, role.Name))
	}))
}
