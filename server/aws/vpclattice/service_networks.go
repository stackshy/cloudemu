package vpclattice

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

// --- wire shapes (camelCase member names per the SDK) ---

type wireSharingConfig struct {
	Enabled bool `json:"enabled"`
}

type wireServiceNetwork struct {
	Arn                                      string             `json:"arn,omitempty"`
	AuthType                                 string             `json:"authType,omitempty"`
	ID                                       string             `json:"id,omitempty"`
	Name                                     string             `json:"name,omitempty"`
	SharingConfig                            *wireSharingConfig `json:"sharingConfig,omitempty"`
	CreatedAt                                string             `json:"createdAt,omitempty"`
	LastUpdatedAt                            string             `json:"lastUpdatedAt,omitempty"`
	NumberOfAssociatedServices               int64              `json:"numberOfAssociatedServices"`
	NumberOfAssociatedVPCs                   int64              `json:"numberOfAssociatedVPCs"`
	NumberOfAssociatedResourceConfigurations int64              `json:"numberOfAssociatedResourceConfigurations"`
}

func serviceNetworkToWire(s *driver.ServiceNetwork) wireServiceNetwork {
	return wireServiceNetwork{
		Arn:                                      s.ARN,
		AuthType:                                 s.AuthType,
		ID:                                       s.ID,
		Name:                                     s.Name,
		SharingConfig:                            &wireSharingConfig{Enabled: s.SharingConfigEnabled},
		CreatedAt:                                s.CreatedAt,
		LastUpdatedAt:                            s.LastUpdatedAt,
		NumberOfAssociatedServices:               s.NumberOfAssociatedServices,
		NumberOfAssociatedVPCs:                   s.NumberOfAssociatedVPCs,
		NumberOfAssociatedResourceConfigurations: s.NumberOfAssociatedResourceConfigurations,
	}
}

// serveServiceNetworks routes /servicenetworks[/{id}].
func (h *Handler) serveServiceNetworks(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		routeCollection(w, r, h.createServiceNetwork, h.listServiceNetworks)

		return
	}

	routeByID(w, r, rest[0], h.getServiceNetwork, h.updateServiceNetwork, h.deleteServiceNetwork)
}

func (h *Handler) createServiceNetwork(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string             `json:"name"`
		AuthType      string             `json:"authType"`
		SharingConfig *wireSharingConfig `json:"sharingConfig"`
		Tags          map[string]string  `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	in := &driver.CreateServiceNetworkInput{Name: req.Name, AuthType: req.AuthType, Tags: req.Tags}
	if req.SharingConfig != nil {
		in.SharingConfigEnabled = req.SharingConfig.Enabled
	}

	sn, err := h.lattice.CreateServiceNetwork(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, serviceNetworkToWire(sn))
}

func (h *Handler) getServiceNetwork(w http.ResponseWriter, r *http.Request, id string) {
	sn, err := h.lattice.GetServiceNetwork(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, serviceNetworkToWire(sn))
}

func (h *Handler) updateServiceNetwork(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		AuthType string `json:"authType"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	sn, err := h.lattice.UpdateServiceNetwork(r.Context(), id, req.AuthType)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, serviceNetworkToWire(sn))
}

func (h *Handler) deleteServiceNetwork(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.lattice.DeleteServiceNetwork(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listServiceNetworks(w http.ResponseWriter, r *http.Request) {
	sns, err := h.lattice.ListServiceNetworks(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireServiceNetwork, 0, len(sns))
	for i := range sns {
		items = append(items, serviceNetworkToWire(&sns[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}
