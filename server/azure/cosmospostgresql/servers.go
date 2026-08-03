package cosmospostgresql

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

func (h *Handler) serveServers(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	switch {
	case rp.SubResourceName == "":
		h.listServers(w, r, rp)
	case rp.SubResourceAction == subConfigurations:
		h.listServerConfigurations(w, r, rp)
	default:
		h.getServer(w, r, rp)
	}
}

func (h *Handler) listServers(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	servers, err := h.db.ListServers(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, armListOf(servers, func(s *cpgdriver.Server) serverResource {
		return toARMServer(s, h.childID(rp, subServers, s.Name))
	}))
}

func (h *Handler) getServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	s, err := h.db.GetServer(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMServer(s, h.childID(rp, subServers, s.Name)))
}

func (h *Handler) listServerConfigurations(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	cfgs, err := h.db.ListServerConfigurations(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, armListOf(cfgs, func(sc *cpgdriver.ServerConfiguration) serverConfigurationResource {
		id := h.childID(rp, subServers, rp.SubResourceName) + "/" + subConfigurations + "/" + sc.Name

		return toARMServerConfiguration(sc, id, subConfigurations)
	}))
}
