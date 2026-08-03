package networkfirewall

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type associatePolicyRequest struct {
	FirewallName      string `json:"FirewallName"`
	FirewallArn       string `json:"FirewallArn"`
	FirewallPolicyArn string `json:"FirewallPolicyArn"`
}

func (h *Handler) associateFirewallPolicy(w http.ResponseWriter, r *http.Request) {
	var req associatePolicyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	fw, err := h.db.AssociateFirewallPolicy(r.Context(), firewallName(req.FirewallName, req.FirewallArn), req.FirewallPolicyArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"FirewallArn": fw.ARN, "FirewallName": fw.Name,
		"FirewallPolicyArn": fw.PolicyARN, "UpdateToken": updateToken,
	})
}

type subnetsRequest struct {
	FirewallName   string          `json:"FirewallName"`
	FirewallArn    string          `json:"FirewallArn"`
	SubnetMappings []subnetMapping `json:"SubnetMappings"`
	SubnetIDs      []string        `json:"SubnetIds"`
}

func (h *Handler) associateSubnets(w http.ResponseWriter, r *http.Request) {
	var req subnetsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	fw, err := h.db.AssociateSubnets(r.Context(), firewallName(req.FirewallName, req.FirewallArn), subnetIDs(req.SubnetMappings))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeSubnetsResult(w, fw)
}

func (h *Handler) disassociateSubnets(w http.ResponseWriter, r *http.Request) {
	var req subnetsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	fw, err := h.db.DisassociateSubnets(r.Context(), firewallName(req.FirewallName, req.FirewallArn), req.SubnetIDs)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeSubnetsResult(w, fw)
}

func writeSubnetsResult(w http.ResponseWriter, fw *nfdriver.Firewall) {
	mappings := make([]subnetMapping, 0, len(fw.SubnetIDs))
	for _, s := range fw.SubnetIDs {
		mappings = append(mappings, subnetMapping{SubnetID: s})
	}

	wire.WriteJSON(w, map[string]any{
		"FirewallArn": fw.ARN, "FirewallName": fw.Name,
		"SubnetMappings": mappings, "UpdateToken": updateToken,
	})
}

type deleteProtectionRequest struct {
	FirewallName     string `json:"FirewallName"`
	FirewallArn      string `json:"FirewallArn"`
	DeleteProtection bool   `json:"DeleteProtection"`
}

func (h *Handler) updateFirewallDeleteProtection(w http.ResponseWriter, r *http.Request) {
	var req deleteProtectionRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	fw, err := h.db.UpdateFirewallDeleteProtection(r.Context(), firewallName(req.FirewallName, req.FirewallArn), req.DeleteProtection)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"FirewallArn": fw.ARN, "FirewallName": fw.Name,
		"DeleteProtection": fw.DeleteProtection, "UpdateToken": updateToken,
	})
}

type logDestinationConfig struct {
	LogType            string            `json:"LogType"`
	LogDestinationType string            `json:"LogDestinationType,omitempty"`
	LogDestination     map[string]string `json:"LogDestination,omitempty"`
}

type loggingConfiguration struct {
	LogDestinationConfigs []logDestinationConfig `json:"LogDestinationConfigs"`
}

type loggingRequest struct {
	FirewallName         string               `json:"FirewallName"`
	FirewallArn          string               `json:"FirewallArn"`
	LoggingConfiguration loggingConfiguration `json:"LoggingConfiguration"`
}

func (h *Handler) updateLoggingConfiguration(w http.ResponseWriter, r *http.Request) {
	var req loggingRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	logTypes := make([]string, 0, len(req.LoggingConfiguration.LogDestinationConfigs))
	for _, c := range req.LoggingConfiguration.LogDestinationConfigs {
		logTypes = append(logTypes, c.LogType)
	}

	name := firewallName(req.FirewallName, req.FirewallArn)
	if err := h.db.UpdateLoggingConfiguration(r.Context(), name, logTypes); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"FirewallArn": req.FirewallArn, "FirewallName": name,
		"LoggingConfiguration": req.LoggingConfiguration,
	})
}

func (h *Handler) describeLoggingConfiguration(w http.ResponseWriter, r *http.Request) {
	var req nameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name := firewallName(req.FirewallName, req.FirewallArn)

	logTypes, err := h.db.DescribeLoggingConfiguration(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	configs := make([]logDestinationConfig, 0, len(logTypes))
	for _, lt := range logTypes {
		configs = append(configs, logDestinationConfig{LogType: lt})
	}

	wire.WriteJSON(w, map[string]any{
		"FirewallArn":          req.FirewallArn,
		"LoggingConfiguration": loggingConfiguration{LogDestinationConfigs: configs},
	})
}

type tagResourceRequest struct {
	ResourceArn string `json:"ResourceArn"`
	Tags        []tag  `json:"Tags"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var req tagResourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.db.TagResource(r.Context(), req.ResourceArn, tagsToMap(req.Tags)); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

type untagResourceRequest struct {
	ResourceArn string   `json:"ResourceArn"`
	TagKeys     []string `json:"TagKeys"`
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var req untagResourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.db.UntagResource(r.Context(), req.ResourceArn, req.TagKeys); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// firewallName resolves the firewall's store key (its name) from either the
// name or the ARN provided by the caller. Firewalls are keyed by name.
func firewallName(name, arn string) string {
	if name != "" {
		return name
	}

	// ARN tail after "firewall/" is the name.
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == '/' {
			return arn[i+1:]
		}
	}

	return arn
}
