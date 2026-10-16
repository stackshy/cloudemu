package networkfirewall

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const millisPerSecond = 1000

type tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type subnetMapping struct {
	SubnetID      string `json:"SubnetId"`
	IPAddressType string `json:"IPAddressType,omitempty"`
}

type zoneMapping struct {
	AvailabilityZone string `json:"AvailabilityZone"`
}

// epochSeconds renders a timestamp the way AWS JSON 1.0 does (seconds since the
// epoch, fractional), which is what the SDK deserializer expects.
func epochSeconds(t time.Time) float64 {
	return float64(t.UnixMilli()) / millisPerSecond
}

// ---- Firewall ----

type firewallJSON struct {
	FirewallName                     string          `json:"FirewallName"`
	FirewallArn                      string          `json:"FirewallArn"`
	FirewallID                       string          `json:"FirewallId,omitempty"`
	FirewallPolicyArn                string          `json:"FirewallPolicyArn,omitempty"`
	VpcID                            string          `json:"VpcId,omitempty"`
	SubnetMappings                   []subnetMapping `json:"SubnetMappings,omitempty"`
	AvailabilityZoneMappings         []zoneMapping   `json:"AvailabilityZoneMappings,omitempty"`
	TransitGatewayID                 string          `json:"TransitGatewayId,omitempty"`
	Description                      string          `json:"Description,omitempty"`
	DeleteProtection                 bool            `json:"DeleteProtection"`
	SubnetChangeProtection           bool            `json:"SubnetChangeProtection"`
	FirewallPolicyChangeProtection   bool            `json:"FirewallPolicyChangeProtection"`
	AvailabilityZoneChangeProtection bool            `json:"AvailabilityZoneChangeProtection"`
	EnabledAnalysisTypes             []string        `json:"EnabledAnalysisTypes,omitempty"`
	NumberOfAssociations             int             `json:"NumberOfAssociations"`
	Tags                             []tag           `json:"Tags,omitempty"`
}

type attachmentJSON struct {
	SubnetID   string `json:"SubnetId,omitempty"`
	EndpointID string `json:"EndpointId,omitempty"`
	Status     string `json:"Status,omitempty"`
}

type syncStateJSON struct {
	Attachment attachmentJSON `json:"Attachment"`
}

type firewallStatusJSON struct {
	Status                        string                   `json:"Status"`
	ConfigurationSyncStateSummary string                   `json:"ConfigurationSyncStateSummary,omitempty"`
	SyncStates                    map[string]syncStateJSON `json:"SyncStates,omitempty"`
}

// toFirewallStatusJSON builds the FirewallStatus block. A firewall in the
// emulator is always fully provisioned, so it reports READY / IN_SYNC with one
// SyncState per attached subnet keyed by a synthetic Availability Zone.
func toFirewallStatusJSON(f *nfdriver.Firewall) firewallStatusJSON {
	region := arnRegion(f.ARN)

	states := make(map[string]syncStateJSON, len(f.SubnetIDs))
	for i, subnet := range f.SubnetIDs {
		states[nfdriver.AvailabilityZoneFor(region, i)] = syncStateJSON{Attachment: attachmentJSON{
			SubnetID:   subnet,
			EndpointID: "vpce-" + f.ID,
			Status:     "READY",
		}}
	}

	for _, az := range f.AvailabilityZones {
		states[az] = syncStateJSON{Attachment: attachmentJSON{EndpointID: "vpce-" + f.ID, Status: "READY"}}
	}

	if len(states) == 0 {
		states = nil
	}

	return firewallStatusJSON{
		Status:                        f.Status,
		ConfigurationSyncStateSummary: "IN_SYNC",
		SyncStates:                    states,
	}
}

// arnRegion extracts the region field (index 3) from an AWS ARN, defaulting to
// "us-east-1" when the ARN is malformed or region-less.
func arnRegion(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) > 3 && parts[3] != "" {
		return parts[3]
	}

	return "us-east-1"
}

type createFirewallRequest struct {
	FirewallName                     string          `json:"FirewallName"`
	FirewallPolicyArn                string          `json:"FirewallPolicyArn"`
	VpcID                            string          `json:"VpcId"`
	SubnetMappings                   []subnetMapping `json:"SubnetMappings"`
	AvailabilityZoneMappings         []zoneMapping   `json:"AvailabilityZoneMappings"`
	TransitGatewayID                 string          `json:"TransitGatewayId"`
	Description                      string          `json:"Description"`
	DeleteProtection                 bool            `json:"DeleteProtection"`
	SubnetChangeProtection           bool            `json:"SubnetChangeProtection"`
	FirewallPolicyChangeProtection   bool            `json:"FirewallPolicyChangeProtection"`
	AvailabilityZoneChangeProtection bool            `json:"AvailabilityZoneChangeProtection"`
	EnabledAnalysisTypes             []string        `json:"EnabledAnalysisTypes"`
	Tags                             []tag           `json:"Tags"`
}

type nameArnRequest struct {
	FirewallName string `json:"FirewallName"`
	FirewallArn  string `json:"FirewallArn"`
}

func (h *Handler) createFirewall(w http.ResponseWriter, r *http.Request) {
	var req createFirewallRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	fw, err := h.db.CreateFirewall(r.Context(), nfdriver.CreateFirewallConfig{
		Name:                             req.FirewallName,
		PolicyARN:                        req.FirewallPolicyArn,
		VPCID:                            req.VpcID,
		SubnetIDs:                        subnetIDs(req.SubnetMappings),
		AvailabilityZones:                zoneNames(req.AvailabilityZoneMappings),
		TransitGatewayID:                 req.TransitGatewayID,
		Description:                      req.Description,
		DeleteProtection:                 req.DeleteProtection,
		SubnetChangeProtection:           req.SubnetChangeProtection,
		FirewallPolicyChangeProtection:   req.FirewallPolicyChangeProtection,
		AvailabilityZoneChangeProtection: req.AvailabilityZoneChangeProtection,
		EnabledAnalysisTypes:             req.EnabledAnalysisTypes,
		Tags:                             tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"Firewall":       toFirewallJSON(fw),
		"FirewallStatus": toFirewallStatusJSON(fw),
	})
}

func (h *Handler) describeFirewall(w http.ResponseWriter, r *http.Request) {
	var req nameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	fw, err := h.db.DescribeFirewall(r.Context(), req.FirewallName, req.FirewallArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":    fw.UpdateToken,
		"Firewall":       toFirewallJSON(fw),
		"FirewallStatus": toFirewallStatusJSON(fw),
	})
}

func (h *Handler) deleteFirewall(w http.ResponseWriter, r *http.Request) {
	var req nameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	fw, err := h.db.DeleteFirewall(r.Context(), req.FirewallName, req.FirewallArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"Firewall":       toFirewallJSON(fw),
		"FirewallStatus": firewallStatusJSON{Status: fw.Status},
	})
}

type listFirewallsRequest struct {
	MaxResults int      `json:"MaxResults"`
	NextToken  string   `json:"NextToken"`
	VpcIDs     []string `json:"VpcIds"`
}

func (h *Handler) listFirewalls(w http.ResponseWriter, r *http.Request) {
	var req listFirewallsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	items, next, err := h.db.ListFirewalls(r.Context(), req.VpcIDs, nfdriver.ListOptions{MaxResults: req.MaxResults, NextToken: req.NextToken})
	if err != nil {
		writeErr(w, err)
		return
	}

	metas := make([]map[string]string, 0, len(items))
	for i := range items {
		metas = append(metas, map[string]string{"FirewallName": items[i].Name, "FirewallArn": items[i].ARN})
	}

	wire.WriteJSON(w, withNextToken(map[string]any{"Firewalls": metas}, next))
}

// withNextToken adds NextToken to a list response when there is another page.
func withNextToken(resp map[string]any, next string) map[string]any {
	if next != "" {
		resp["NextToken"] = next
	}

	return resp
}

func toFirewallJSON(f *nfdriver.Firewall) firewallJSON {
	mappings := make([]subnetMapping, 0, len(f.SubnetIDs))
	for _, s := range f.SubnetIDs {
		mappings = append(mappings, subnetMapping{SubnetID: s})
	}

	zones := make([]zoneMapping, 0, len(f.AvailabilityZones))
	for _, z := range f.AvailabilityZones {
		zones = append(zones, zoneMapping{AvailabilityZone: z})
	}

	return firewallJSON{
		FirewallName: f.Name, FirewallArn: f.ARN, FirewallID: f.ID, FirewallPolicyArn: f.PolicyARN,
		VpcID: f.VPCID, SubnetMappings: mappings, AvailabilityZoneMappings: zones,
		TransitGatewayID: f.TransitGatewayID, Description: f.Description,
		DeleteProtection: f.DeleteProtection, SubnetChangeProtection: f.SubnetChangeProtection,
		FirewallPolicyChangeProtection:   f.FirewallPolicyChangeProtection,
		AvailabilityZoneChangeProtection: f.AvailabilityZoneChangeProtection,
		EnabledAnalysisTypes:             f.EnabledAnalysisTypes, NumberOfAssociations: f.NumberOfAssociations,
		Tags: mapToTags(f.Tags),
	}
}

// ---- helpers ----

func subnetIDs(mappings []subnetMapping) []string {
	if len(mappings) == 0 {
		return nil
	}

	out := make([]string, 0, len(mappings))
	for _, m := range mappings {
		out = append(out, m.SubnetID)
	}

	return out
}

func zoneNames(mappings []zoneMapping) []string {
	if len(mappings) == 0 {
		return nil
	}

	out := make([]string, 0, len(mappings))
	for _, m := range mappings {
		out = append(out, m.AvailabilityZone)
	}

	return out
}

func tagsToMap(tags []tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

func mapToTags(m map[string]string) []tag {
	if len(m) == 0 {
		return nil
	}

	out := make([]tag, 0, len(m))
	for k, v := range m {
		out = append(out, tag{Key: k, Value: v})
	}

	return out
}

// ruleGroupPayload resolves the rules content for Create/UpdateRuleGroup: the
// modern "RuleGroup" object takes precedence; the deprecated top-level
// "Rules" Suricata string (used by aws_networkfirewall_rule_group's
// rules_source_list-free legacy form) is normalized into the equivalent
// {"RulesSource":{"RulesString":...}} shape so DescribeRuleGroup always
// returns rules via RuleGroup.RulesSource regardless of which form was used
// to write them.
func ruleGroupPayload(ruleGroup json.RawMessage, legacyRules string) json.RawMessage {
	if len(ruleGroup) > 0 {
		return ruleGroup
	}

	if legacyRules == "" {
		return nil
	}

	b, err := json.Marshal(map[string]any{
		"RulesSource": map[string]any{"RulesString": legacyRules},
	})
	if err != nil {
		return nil
	}

	return b
}
