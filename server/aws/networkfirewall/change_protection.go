package networkfirewall

import (
	"net/http"

	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

// firewallSettingCall is a FirewallSettings operation: it returns the updated
// firewall and the response member that echoes the changed setting (a nil value
// is left out, as AWS omits an absent member).
type firewallSettingCall[R any] func(s nfdriver.FirewallSettings, name string, req *R) (*nfdriver.Firewall, string, any, error)

// serveFirewallSetting adapts a FirewallSettings operation to serveFirewallOp.
func serveFirewallSetting[R any, P firewallRequest[R]](h *Handler, w http.ResponseWriter, r *http.Request, call firewallSettingCall[R]) {
	serveFirewallOp[R, P](h, w, r, func(s nfdriver.FirewallSettings, name string, req *R) (*nfdriver.Firewall, map[string]any, error) {
		fw, member, value, err := call(s, name, req)
		if err != nil {
			return nil, nil, err
		}

		extra := map[string]any{}
		if value != nil {
			extra[member] = value
		}

		return fw, extra, nil
	})
}

// memberDescription is the response member UpdateFirewallDescription echoes.
const memberDescription = "Description"

type descriptionRequest struct {
	firewallUpdateRequest
	Description string `json:"Description"`
}

func (h *Handler) updateFirewallDescription(w http.ResponseWriter, r *http.Request) {
	serveFirewallSetting[descriptionRequest](h, w, r,
		func(s nfdriver.FirewallSettings, name string, req *descriptionRequest) (*nfdriver.Firewall, string, any, error) {
			fw, err := s.UpdateFirewallDescription(r.Context(), name, req.Description, req.UpdateToken)
			if err != nil {
				return nil, "", nil, err
			}

			if fw.Description == "" {
				return fw, memberDescription, nil, nil
			}

			return fw, memberDescription, fw.Description, nil
		})
}

type subnetProtectionRequest struct {
	firewallUpdateRequest
	SubnetChangeProtection bool `json:"SubnetChangeProtection"`
}

func (h *Handler) updateSubnetChangeProtection(w http.ResponseWriter, r *http.Request) {
	serveFirewallSetting[subnetProtectionRequest](h, w, r,
		func(s nfdriver.FirewallSettings, name string, req *subnetProtectionRequest) (*nfdriver.Firewall, string, any, error) {
			fw, err := s.UpdateSubnetChangeProtection(r.Context(), name, req.SubnetChangeProtection, req.UpdateToken)
			if err != nil {
				return nil, "", nil, err
			}

			return fw, "SubnetChangeProtection", fw.SubnetChangeProtection, nil
		})
}

type policyProtectionRequest struct {
	firewallUpdateRequest
	FirewallPolicyChangeProtection bool `json:"FirewallPolicyChangeProtection"`
}

func (h *Handler) updateFirewallPolicyChangeProtection(w http.ResponseWriter, r *http.Request) {
	serveFirewallSetting[policyProtectionRequest](h, w, r,
		func(s nfdriver.FirewallSettings, name string, req *policyProtectionRequest) (*nfdriver.Firewall, string, any, error) {
			fw, err := s.UpdateFirewallPolicyChangeProtection(r.Context(), name, req.FirewallPolicyChangeProtection, req.UpdateToken)
			if err != nil {
				return nil, "", nil, err
			}

			return fw, "FirewallPolicyChangeProtection", fw.FirewallPolicyChangeProtection, nil
		})
}

type zoneProtectionRequest struct {
	firewallUpdateRequest
	AvailabilityZoneChangeProtection bool `json:"AvailabilityZoneChangeProtection"`
}

func (h *Handler) updateAvailabilityZoneChangeProtection(w http.ResponseWriter, r *http.Request) {
	serveFirewallSetting[zoneProtectionRequest](h, w, r,
		func(s nfdriver.FirewallSettings, name string, req *zoneProtectionRequest) (*nfdriver.Firewall, string, any, error) {
			fw, err := s.UpdateAvailabilityZoneChangeProtection(r.Context(), name, req.AvailabilityZoneChangeProtection, req.UpdateToken)
			if err != nil {
				return nil, "", nil, err
			}

			return fw, "AvailabilityZoneChangeProtection", fw.AvailabilityZoneChangeProtection, nil
		})
}

type analysisSettingsRequest struct {
	firewallUpdateRequest
	EnabledAnalysisTypes []string `json:"EnabledAnalysisTypes"`
}

func (h *Handler) updateFirewallAnalysisSettings(w http.ResponseWriter, r *http.Request) {
	serveFirewallSetting[analysisSettingsRequest](h, w, r,
		func(s nfdriver.FirewallSettings, name string, req *analysisSettingsRequest) (*nfdriver.Firewall, string, any, error) {
			fw, err := s.UpdateFirewallAnalysisSettings(r.Context(), name, req.EnabledAnalysisTypes, req.UpdateToken)
			if err != nil {
				return nil, "", nil, err
			}

			return fw, "EnabledAnalysisTypes", fw.EnabledAnalysisTypes, nil
		})
}
