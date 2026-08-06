package route53resolver

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

// --- wire shapes ---

type wireResolverConfig struct {
	ID                 string `json:"Id,omitempty"`
	OwnerID            string `json:"OwnerId,omitempty"`
	ResourceID         string `json:"ResourceId,omitempty"`
	AutodefinedReverse string `json:"AutodefinedReverse,omitempty"`
}

type wireDnssecConfig struct {
	ID               string `json:"Id,omitempty"`
	OwnerID          string `json:"OwnerId,omitempty"`
	ResourceID       string `json:"ResourceId,omitempty"`
	ValidationStatus string `json:"ValidationStatus,omitempty"`
}

// --- mapping ---

func resolverConfigToWire(c *driver.ResolverConfig) wireResolverConfig {
	return wireResolverConfig{
		ID:                 c.ID,
		OwnerID:            c.OwnerID,
		ResourceID:         c.ResourceID,
		AutodefinedReverse: c.AutodefinedReverse,
	}
}

func dnssecConfigToWire(c *driver.ResolverDnssecConfig) wireDnssecConfig {
	return wireDnssecConfig{
		ID:               c.ID,
		OwnerID:          c.OwnerID,
		ResourceID:       c.ResourceID,
		ValidationStatus: c.ValidationStatus,
	}
}

// --- handlers ---

func (h *Handler) getResolverConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceID string `json:"ResourceId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.r53r.GetResolverConfig(r.Context(), req.ResourceID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverConfig": resolverConfigToWire(c)})
}

func (h *Handler) updateResolverConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AutodefinedReverseFlag string `json:"AutodefinedReverseFlag"`
		ResourceID             string `json:"ResourceId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.r53r.UpdateResolverConfig(r.Context(), req.ResourceID, req.AutodefinedReverseFlag)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverConfig": resolverConfigToWire(c)})
}

func (h *Handler) listResolverConfigs(w http.ResponseWriter, r *http.Request) {
	cs, err := h.r53r.ListResolverConfigs(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireResolverConfig, 0, len(cs))
	for i := range cs {
		out = append(out, resolverConfigToWire(&cs[i]))
	}

	wire.WriteJSON(w, map[string]any{"ResolverConfigs": out})
}

func (h *Handler) getResolverDnssecConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceID string `json:"ResourceId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.r53r.GetResolverDnssecConfig(r.Context(), req.ResourceID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverDNSSECConfig": dnssecConfigToWire(c)})
}

func (h *Handler) updateResolverDnssecConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceID string `json:"ResourceId"`
		Validation string `json:"Validation"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.r53r.UpdateResolverDnssecConfig(r.Context(), req.ResourceID, req.Validation)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverDNSSECConfig": dnssecConfigToWire(c)})
}

func (h *Handler) listResolverDnssecConfigs(w http.ResponseWriter, r *http.Request) {
	cs, err := h.r53r.ListResolverDnssecConfigs(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireDnssecConfig, 0, len(cs))
	for i := range cs {
		out = append(out, dnssecConfigToWire(&cs[i]))
	}

	wire.WriteJSON(w, map[string]any{"ResolverDnssecConfigs": out})
}
