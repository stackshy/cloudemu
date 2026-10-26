package networkfirewall

import (
	"net/http"

	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type zonesRequest struct {
	firewallUpdateRequest
	AvailabilityZoneMappings []zoneMapping `json:"AvailabilityZoneMappings"`
}

func zoneMappingsOf(fw *nfdriver.Firewall) []zoneMapping {
	out := make([]zoneMapping, 0, len(fw.AvailabilityZones))
	for _, z := range fw.AvailabilityZones {
		out = append(out, zoneMapping{AvailabilityZone: z})
	}

	return out
}

// zoneChange is the Associate/DisassociateAvailabilityZones driver call.
type zoneChange func(z nfdriver.AvailabilityZones, name string, zones []string, token string) (*nfdriver.Firewall, error)

func (h *Handler) changeZones(w http.ResponseWriter, r *http.Request, change zoneChange) {
	serveFirewallOp[zonesRequest](h, w, r,
		func(z nfdriver.AvailabilityZones, name string, req *zonesRequest) (*nfdriver.Firewall, map[string]any, error) {
			fw, err := change(z, name, zoneNames(req.AvailabilityZoneMappings), req.UpdateToken)
			if err != nil {
				return nil, nil, err
			}

			return fw, map[string]any{"AvailabilityZoneMappings": zoneMappingsOf(fw)}, nil
		})
}

func (h *Handler) associateAvailabilityZones(w http.ResponseWriter, r *http.Request) {
	h.changeZones(w, r, func(z nfdriver.AvailabilityZones, name string, zones []string, token string) (*nfdriver.Firewall, error) {
		return z.AssociateAvailabilityZones(r.Context(), name, zones, token)
	})
}

func (h *Handler) disassociateAvailabilityZones(w http.ResponseWriter, r *http.Request) {
	h.changeZones(w, r, func(z nfdriver.AvailabilityZones, name string, zones []string, token string) (*nfdriver.Firewall, error) {
		return z.DisassociateAvailabilityZones(r.Context(), name, zones, token)
	})
}
