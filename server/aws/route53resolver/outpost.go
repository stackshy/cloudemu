package route53resolver

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

// --- wire shape ---

type wireOutpostResolver struct {
	ID                    string `json:"Id,omitempty"`
	Arn                   string `json:"Arn,omitempty"`
	Name                  string `json:"Name,omitempty"`
	CreatorRequestID      string `json:"CreatorRequestId,omitempty"`
	OutpostArn            string `json:"OutpostArn,omitempty"`
	PreferredInstanceType string `json:"PreferredInstanceType,omitempty"`
	InstanceCount         int32  `json:"InstanceCount"`
	Status                string `json:"Status,omitempty"`
	StatusMessage         string `json:"StatusMessage,omitempty"`
	CreationTime          string `json:"CreationTime,omitempty"`
	ModificationTime      string `json:"ModificationTime,omitempty"`
}

func outpostToWire(o *driver.OutpostResolver) wireOutpostResolver {
	return wireOutpostResolver{
		ID: o.ID, Arn: o.ARN, Name: o.Name, CreatorRequestID: o.CreatorRequestID,
		OutpostArn: o.OutpostARN, PreferredInstanceType: o.PreferredInstanceType,
		InstanceCount: o.InstanceCount, Status: o.Status, StatusMessage: o.StatusMessage,
		CreationTime: o.CreatedAt, ModificationTime: o.ModifiedAt,
	}
}

// --- handlers ---

func (h *Handler) createOutpostResolver(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CreatorRequestID      string    `json:"CreatorRequestId"`
		Name                  string    `json:"Name"`
		OutpostArn            string    `json:"OutpostArn"`
		PreferredInstanceType string    `json:"PreferredInstanceType"`
		InstanceCount         int32     `json:"InstanceCount"`
		Tags                  []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	o, err := h.r53r.CreateOutpostResolver(r.Context(), &driver.CreateOutpostResolverInput{
		CreatorRequestID: req.CreatorRequestID, Name: req.Name, OutpostARN: req.OutpostArn,
		PreferredInstanceType: req.PreferredInstanceType, InstanceCount: req.InstanceCount,
		Tags: toDriverTags(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"OutpostResolver": outpostToWire(o)})
}

func (h *Handler) getOutpostResolver(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"Id"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	o, err := h.r53r.GetOutpostResolver(r.Context(), req.ID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"OutpostResolver": outpostToWire(o)})
}

func (h *Handler) updateOutpostResolver(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID                    string  `json:"Id"`
		Name                  *string `json:"Name"`
		PreferredInstanceType *string `json:"PreferredInstanceType"`
		InstanceCount         *int32  `json:"InstanceCount"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	in := &driver.UpdateOutpostResolverInput{ID: req.ID, Name: req.Name}
	in.PreferredInstanceType = req.PreferredInstanceType
	in.InstanceCount = req.InstanceCount

	o, err := h.r53r.UpdateOutpostResolver(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"OutpostResolver": outpostToWire(o)})
}

func (h *Handler) deleteOutpostResolver(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"Id"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	o, err := h.r53r.DeleteOutpostResolver(r.Context(), req.ID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"OutpostResolver": outpostToWire(o)})
}

func (h *Handler) listOutpostResolvers(w http.ResponseWriter, r *http.Request) {
	os, err := h.r53r.ListOutpostResolvers(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireOutpostResolver, 0, len(os))
	for i := range os {
		out = append(out, outpostToWire(&os[i]))
	}

	wire.WriteJSON(w, map[string]any{"OutpostResolvers": out})
}
