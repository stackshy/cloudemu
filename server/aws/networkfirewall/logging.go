package networkfirewall

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type logDestinationConfig struct {
	LogType            string            `json:"LogType"`
	LogDestinationType string            `json:"LogDestinationType,omitempty"`
	LogDestination     map[string]string `json:"LogDestination,omitempty"`
}

type loggingConfiguration struct {
	LogDestinationConfigs []logDestinationConfig `json:"LogDestinationConfigs"`
}

type loggingRequest struct {
	FirewallName              string                `json:"FirewallName"`
	FirewallArn               string                `json:"FirewallArn"`
	LoggingConfiguration      *loggingConfiguration `json:"LoggingConfiguration"`
	EnableMonitoringDashboard *bool                 `json:"EnableMonitoringDashboard"`
}

func (h *Handler) updateLoggingConfiguration(w http.ResponseWriter, r *http.Request) {
	var req loggingRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name, ok := h.resolveFirewall(w, r, req.FirewallName, req.FirewallArn)
	if !ok {
		return
	}

	cfg := &nfdriver.LoggingConfiguration{EnableMonitoringDashboard: req.EnableMonitoringDashboard}
	if req.LoggingConfiguration != nil {
		cfg.Destinations = toLogConfigs(req.LoggingConfiguration.LogDestinationConfigs)
	}

	if err := h.db.UpdateLoggingConfiguration(r.Context(), name, cfg); err != nil {
		writeErr(w, err)
		return
	}

	h.writeLoggingConfiguration(w, r, name, true)
}

func (h *Handler) describeLoggingConfiguration(w http.ResponseWriter, r *http.Request) {
	var req nameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name, ok := h.resolveFirewall(w, r, req.FirewallName, req.FirewallArn)
	if !ok {
		return
	}

	h.writeLoggingConfiguration(w, r, name, false)
}

// writeLoggingConfiguration reads the stored configuration back and renders it,
// so both the update and describe responses reflect what is stored (the update
// response also names the firewall).
func (h *Handler) writeLoggingConfiguration(w http.ResponseWriter, r *http.Request, name string, withName bool) {
	cfg, err := h.db.DescribeLoggingConfiguration(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	fw, err := h.db.DescribeFirewall(r.Context(), name, "")
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := map[string]any{
		"FirewallArn":          fw.ARN,
		"LoggingConfiguration": loggingConfiguration{LogDestinationConfigs: fromLogConfigs(cfg.Destinations)},
	}
	if cfg.EnableMonitoringDashboard != nil {
		resp["EnableMonitoringDashboard"] = *cfg.EnableMonitoringDashboard
	}

	if withName {
		resp["FirewallName"] = name
	}

	wire.WriteJSON(w, resp)
}

func toLogConfigs(cs []logDestinationConfig) []nfdriver.LogDestinationConfig {
	if len(cs) == 0 {
		return nil
	}

	out := make([]nfdriver.LogDestinationConfig, 0, len(cs))
	for _, c := range cs {
		out = append(out, nfdriver.LogDestinationConfig{
			LogType: c.LogType, LogDestinationType: c.LogDestinationType, LogDestination: c.LogDestination,
		})
	}

	return out
}

func fromLogConfigs(cs []nfdriver.LogDestinationConfig) []logDestinationConfig {
	out := make([]logDestinationConfig, 0, len(cs))
	for _, c := range cs {
		out = append(out, logDestinationConfig{
			LogType: c.LogType, LogDestinationType: c.LogDestinationType, LogDestination: c.LogDestination,
		})
	}

	return out
}
