package networkfirewall

import (
	"encoding/json"

	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const (
	ruleOrderStrict = "STRICT_ORDER"

	minPriority = 1
	maxPriority = 65535
)

// Standard stateless default actions and the documented stateful default
// actions (AWS FirewallPolicy reference).
//
//nolint:gochecknoglobals // immutable enum value sets.
var (
	statelessStandardActions = []string{"aws:pass", "aws:drop", "aws:forward_to_sfe"}
	statefulDefaultActions   = []string{
		"aws:drop_strict", "aws:drop_established", "aws:alert_strict", "aws:alert_established",
		"aws:drop_established_app_layer", "aws:alert_established_app_layer",
		"aws:drop_established_app_layer_to_server", "aws:alert_established_app_layer_to_server",
	}
)

// policySpec is the validated shape shared by create and update.
type policySpec struct {
	description              string
	statelessDefault         []string
	statelessFragmentDefault []string
	statefulDefault          []string
	statefulRefs             []nfdriver.RuleGroupReference
	statelessRefs            []nfdriver.RuleGroupReference
	tlsARN                   string
	extra                    nfdriver.PolicyExtra
}

// validatePolicy applies the AWS rules for a firewall policy body. current is
// the stored policy on update (nil on create).
func (m *Mock) validatePolicy(s *policySpec, current *nfdriver.FirewallPolicy) error {
	if err := validateDescription(s.description); err != nil {
		return err
	}

	strict, err := validatePolicyActions(s)
	if err != nil {
		return err
	}

	return m.validatePolicyReferences(s, strict, current)
}

// validatePolicyReferences checks the rule group references and the TLS
// inspection configuration the policy names.
func (m *Mock) validatePolicyReferences(s *policySpec, strict bool, current *nfdriver.FirewallPolicy) error {
	if err := m.validateRefs("StatelessRuleGroupReferences", s.statelessRefs, true, []string{ruleTypeStateless}); err != nil {
		return err
	}

	if err := m.validateRefs("StatefulRuleGroupReferences", s.statefulRefs, strict,
		[]string{ruleTypeStateful, ruleTypeStatefulDomain}); err != nil {
		return err
	}

	return m.validatePolicyTLS(s, current)
}

// validatePolicyActions checks the stateless and stateful default actions and
// reports whether the policy uses strict stateful rule ordering.
func validatePolicyActions(s *policySpec) (bool, error) {
	custom, err := customActionNames(s.extra.StatelessCustomActions)
	if err != nil {
		return false, err
	}

	strict, err := isStrictOrder(s.extra.StatefulEngineOptions)
	if err != nil {
		return false, err
	}

	return strict, checkDefaultActions(s, custom, strict)
}

func checkDefaultActions(s *policySpec, custom map[string]bool, strict bool) error {
	if err := validateStatelessActions("StatelessDefaultActions", s.statelessDefault, custom); err != nil {
		return err
	}

	if err := validateStatelessActions("StatelessFragmentDefaultActions", s.statelessFragmentDefault, custom); err != nil {
		return err
	}

	return validateStatefulDefaults(s.statefulDefault, strict)
}

// validateStatelessActions requires exactly one standard action; any other entry
// must name a custom action defined in the same policy.
func validateStatelessActions(field string, actions []string, custom map[string]bool) error {
	if len(actions) == 0 {
		return invalid("%s is required", field)
	}

	standard := 0

	for _, a := range actions {
		switch {
		case oneOf(a, statelessStandardActions...):
			standard++
		case custom[a]:
		default:
			return invalid("%s entry %q is neither a standard action nor a defined custom action", field, a)
		}
	}

	if standard != 1 {
		return invalid("%s must contain exactly one of %v", field, statelessStandardActions)
	}

	return nil
}

func validateStatefulDefaults(actions []string, strict bool) error {
	if len(actions) == 0 {
		return nil
	}

	if !strict {
		return invalid("StatefulDefaultActions is only valid with StatefulEngineOptions.RuleOrder %s", ruleOrderStrict)
	}

	for _, a := range actions {
		if !oneOf(a, statefulDefaultActions...) {
			return invalid("StatefulDefaultActions entry %q is not valid", a)
		}
	}

	return nil
}

// validateRefs checks rule group references: the group must exist with an
// allowed type, a priority is required where order matters, and priorities are
// unique within the list.
func (m *Mock) validateRefs(field string, refs []nfdriver.RuleGroupReference, needPriority bool, types []string) error {
	seen := make(map[int]bool, len(refs))

	for _, r := range refs {
		if err := validateARN(field+" ResourceArn", r.ResourceARN); err != nil {
			return err
		}

		rg, ok := m.ruleGroupByARN(r.ResourceARN)
		if !ok {
			return invalid("%s: rule group %q not found", field, r.ResourceARN)
		}

		if !oneOf(rg.Type, types...) {
			return invalid("%s: rule group %q has type %s", field, r.ResourceARN, rg.Type)
		}

		if !needPriority {
			continue
		}

		if r.Priority < minPriority || r.Priority > maxPriority {
			return invalid("%s: Priority must be between %d and %d", field, minPriority, maxPriority)
		}

		if seen[r.Priority] {
			return invalid("%s: duplicate Priority %d", field, r.Priority)
		}

		seen[r.Priority] = true
	}

	return nil
}

// validatePolicyTLS checks the TLS inspection configuration reference: it must
// exist, can only be replaced (never added or removed after create), and TLS
// session holding needs one.
func (m *Mock) validatePolicyTLS(s *policySpec, current *nfdriver.FirewallPolicy) error {
	if s.tlsARN != "" {
		if err := validateARN("TLSInspectionConfigurationArn", s.tlsARN); err != nil {
			return err
		}

		if _, ok := m.tlsByARN(s.tlsARN); !ok {
			return invalid("TLS inspection configuration %q not found", s.tlsARN)
		}
	}

	if current != nil && (current.TLSInspectionConfigurationARN == "") != (s.tlsARN == "") {
		return invalid("a TLS inspection configuration can only be replaced, not added or removed, after the policy is created")
	}

	if s.extra.EnableTLSSessionHolding != nil && *s.extra.EnableTLSSessionHolding && s.tlsARN == "" {
		return invalid("EnableTLSSessionHolding requires a TLS inspection configuration")
	}

	return nil
}

func customActionNames(raw json.RawMessage) (map[string]bool, error) {
	names := map[string]bool{}

	if len(raw) == 0 {
		return names, nil
	}

	var actions []struct {
		ActionName string `json:"ActionName"`
	}

	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, invalid("StatelessCustomActions is not valid: %v", err)
	}

	for _, a := range actions {
		names[a.ActionName] = true
	}

	return names, nil
}

func isStrictOrder(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 {
		return false, nil
	}

	var opts struct {
		RuleOrder string `json:"RuleOrder"`
	}

	if err := json.Unmarshal(raw, &opts); err != nil {
		return false, invalid("StatefulEngineOptions is not valid: %v", err)
	}

	if opts.RuleOrder != "" && !oneOf(opts.RuleOrder, "DEFAULT_ACTION_ORDER", ruleOrderStrict) {
		return false, invalid("StatefulEngineOptions.RuleOrder %q is not valid", opts.RuleOrder)
	}

	return opts.RuleOrder == ruleOrderStrict, nil
}
