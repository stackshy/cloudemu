package cosmospostgresql

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

// serveConfigurations handles the cluster-wide configurations collection and
// single-resource GET.
func (h *Handler) serveConfigurations(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	serveReadOnly(w, r, rp, h.listConfigurations, h.getConfiguration)
}

func (h *Handler) getConfiguration(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	c, err := h.db.GetConfiguration(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMConfiguration(c, h.childID(rp, subConfigurations, c.Name)))
}

func (h *Handler) listConfigurations(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	cfgs, err := h.db.ListConfigurations(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, armListOf(cfgs, func(c *cpgdriver.Configuration) configurationResource {
		return toARMConfiguration(c, h.childID(rp, subConfigurations, c.Name))
	}))
}

// serveServerConfig handles the coordinator/node configuration GET + PUT
// (update). coordinator selects the coordinator role group; otherwise node.
func (h *Handler) serveServerConfig(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, coordinator bool) {
	if rp.SubResourceName == "" {
		writeMethodNotAllowed(w)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getServerConfig(w, r, rp, coordinator)
	case http.MethodPut:
		h.updateServerConfig(w, r, rp, coordinator)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) getServerConfig(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, coordinator bool) {
	var (
		sc  *cpgdriver.ServerConfiguration
		err error
		sub = subNodeCfgs
	)

	if coordinator {
		sub = subCoordinatorCfgs
		sc, err = h.db.GetCoordinatorConfiguration(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	} else {
		sc, err = h.db.GetNodeConfiguration(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServerConfiguration(sc, h.childID(rp, sub, sc.Name), sub))
}

func (h *Handler) updateServerConfig(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, coordinator bool) {
	var body serverConfigurationResource
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	value := ""
	if body.Properties != nil {
		value = body.Properties.Value
	}

	var (
		sc  *cpgdriver.ServerConfiguration
		err error
		sub = subNodeCfgs
	)

	if coordinator {
		sub = subCoordinatorCfgs
		sc, err = h.db.UpdateCoordinatorConfiguration(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, value)
	} else {
		sc, err = h.db.UpdateNodeConfiguration(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, value)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServerConfiguration(sc, h.childID(rp, sub, sc.Name), sub))
}
