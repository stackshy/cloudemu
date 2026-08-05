package route53resolver

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

// --- wire shapes ---

type wireQueryLogConfig struct {
	ID               string `json:"Id,omitempty"`
	Arn              string `json:"Arn,omitempty"`
	AssociationCount int32  `json:"AssociationCount,omitempty"`
	CreatorRequestID string `json:"CreatorRequestId,omitempty"`
	DestinationArn   string `json:"DestinationArn,omitempty"`
	Name             string `json:"Name,omitempty"`
	OwnerID          string `json:"OwnerId,omitempty"`
	ShareStatus      string `json:"ShareStatus,omitempty"`
	Status           string `json:"Status,omitempty"`
	CreationTime     string `json:"CreationTime,omitempty"`
}

type wireQLCAssociation struct {
	ID                       string `json:"Id,omitempty"`
	ResolverQueryLogConfigID string `json:"ResolverQueryLogConfigId,omitempty"`
	ResourceID               string `json:"ResourceId,omitempty"`
	Status                   string `json:"Status,omitempty"`
	Error                    string `json:"Error,omitempty"`
	ErrorMessage             string `json:"ErrorMessage,omitempty"`
	CreationTime             string `json:"CreationTime,omitempty"`
}

// --- mapping ---

func qlcToWire(c *driver.QueryLogConfig) wireQueryLogConfig {
	return wireQueryLogConfig{
		ID:               c.ID,
		Arn:              c.ARN,
		AssociationCount: c.AssociationCount,
		CreatorRequestID: c.CreatorRequestID,
		DestinationArn:   c.DestinationARN,
		Name:             c.Name,
		OwnerID:          c.OwnerID,
		ShareStatus:      c.ShareStatus,
		Status:           c.Status,
		CreationTime:     c.CreatedAt,
	}
}

func qlcAssocToWire(a *driver.QueryLogConfigAssociation) wireQLCAssociation {
	return wireQLCAssociation{
		ID:                       a.ID,
		ResolverQueryLogConfigID: a.ResolverQueryLogConfigID,
		ResourceID:               a.ResourceID,
		Status:                   a.Status,
		Error:                    a.Error,
		ErrorMessage:             a.ErrorMessage,
		CreationTime:             a.CreatedAt,
	}
}

// --- handlers ---

func (h *Handler) createQueryLogConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CreatorRequestID string    `json:"CreatorRequestId"`
		DestinationArn   string    `json:"DestinationArn"`
		Name             string    `json:"Name"`
		Tags             []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.r53r.CreateResolverQueryLogConfig(r.Context(), &driver.CreateQueryLogConfigInput{
		CreatorRequestID: req.CreatorRequestID,
		DestinationARN:   req.DestinationArn,
		Name:             req.Name,
		Tags:             toDriverTags(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverQueryLogConfig": qlcToWire(c)})
}

func (h *Handler) getQueryLogConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverQueryLogConfigID string `json:"ResolverQueryLogConfigId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.r53r.GetResolverQueryLogConfig(r.Context(), req.ResolverQueryLogConfigID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverQueryLogConfig": qlcToWire(c)})
}

func (h *Handler) deleteQueryLogConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverQueryLogConfigID string `json:"ResolverQueryLogConfigId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.r53r.DeleteResolverQueryLogConfig(r.Context(), req.ResolverQueryLogConfigID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverQueryLogConfig": qlcToWire(c)})
}

func (h *Handler) listQueryLogConfigs(w http.ResponseWriter, r *http.Request) {
	cs, err := h.r53r.ListResolverQueryLogConfigs(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireQueryLogConfig, 0, len(cs))
	for i := range cs {
		out = append(out, qlcToWire(&cs[i]))
	}

	wire.WriteJSON(w, map[string]any{"ResolverQueryLogConfigs": out})
}

func (h *Handler) associateQueryLogConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverQueryLogConfigID string `json:"ResolverQueryLogConfigId"`
		ResourceID               string `json:"ResourceId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := h.r53r.AssociateResolverQueryLogConfig(r.Context(), req.ResolverQueryLogConfigID, req.ResourceID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverQueryLogConfigAssociation": qlcAssocToWire(a)})
}

func (h *Handler) disassociateQueryLogConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverQueryLogConfigID string `json:"ResolverQueryLogConfigId"`
		ResourceID               string `json:"ResourceId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := h.r53r.DisassociateResolverQueryLogConfig(r.Context(), req.ResolverQueryLogConfigID, req.ResourceID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverQueryLogConfigAssociation": qlcAssocToWire(a)})
}

func (h *Handler) getQueryLogConfigAssociation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverQueryLogConfigAssociationID string `json:"ResolverQueryLogConfigAssociationId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := h.r53r.GetResolverQueryLogConfigAssociation(r.Context(), req.ResolverQueryLogConfigAssociationID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverQueryLogConfigAssociation": qlcAssocToWire(a)})
}

func (h *Handler) listQueryLogConfigAssociations(w http.ResponseWriter, r *http.Request) {
	as, err := h.r53r.ListResolverQueryLogConfigAssociations(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireQLCAssociation, 0, len(as))
	for i := range as {
		out = append(out, qlcAssocToWire(&as[i]))
	}

	wire.WriteJSON(w, map[string]any{"ResolverQueryLogConfigAssociations": out})
}

func (h *Handler) putQueryLogConfigPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Arn                          string `json:"Arn"`
		ResolverQueryLogConfigPolicy string `json:"ResolverQueryLogConfigPolicy"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.r53r.PutResolverQueryLogConfigPolicy(r.Context(), req.Arn, req.ResolverQueryLogConfigPolicy); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ReturnValue": true})
}

func (h *Handler) getQueryLogConfigPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Arn string `json:"Arn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policy, err := h.r53r.GetResolverQueryLogConfigPolicy(r.Context(), req.Arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverQueryLogConfigPolicy": policy})
}
