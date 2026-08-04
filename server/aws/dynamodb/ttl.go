package dynamodb

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

func (h *Handler) routeTTL(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "UpdateTimeToLive":
		h.updateTimeToLive(w, r)
	case "DescribeTimeToLive":
		h.describeTimeToLive(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) updateTimeToLive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName               string `json:"TableName"`
		TimeToLiveSpecification struct {
			Enabled       bool   `json:"Enabled"`
			AttributeName string `json:"AttributeName"`
		} `json:"TimeToLiveSpecification"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	spec := req.TimeToLiveSpecification

	if err := h.db.UpdateTTL(r.Context(), req.TableName, dbdriver.TTLConfig{
		Enabled: spec.Enabled, AttributeName: spec.AttributeName,
	}); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"TimeToLiveSpecification": map[string]any{
			"Enabled": spec.Enabled, "AttributeName": spec.AttributeName,
		},
	})
}

func (h *Handler) describeTimeToLive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string `json:"TableName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cfg, err := h.db.DescribeTTL(r.Context(), req.TableName)
	if err != nil {
		writeErr(w, err)
		return
	}

	status := "DISABLED"
	if cfg.Enabled {
		status = "ENABLED"
	}

	wire.WriteJSON(w, map[string]any{
		"TimeToLiveDescription": map[string]any{
			"TimeToLiveStatus": status,
			"AttributeName":    cfg.AttributeName,
		},
	})
}
