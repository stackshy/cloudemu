package networkfirewall

import (
	"context"
	"encoding/json"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const maxResourcePolicyLen = 395000

// allowedPolicyActions are the actions a resource-sharing policy may grant, per
// shareable resource kind (AWS PutResourcePolicy reference).
//
//nolint:gochecknoglobals // immutable lookup table.
var allowedPolicyActions = map[string][]string{
	"stateful-rulegroup":  ruleGroupPolicyActions,
	"stateless-rulegroup": ruleGroupPolicyActions,
	"firewall-policy":     {"network-firewall:AssociateFirewallPolicy", "network-firewall:ListFirewallPolicies"},
	"firewall": {
		"network-firewall:CreateVpcEndpointAssociation", "network-firewall:DescribeFirewallMetadata", "network-firewall:ListFirewalls",
	},
}

//nolint:gochecknoglobals // immutable lookup table.
var ruleGroupPolicyActions = []string{
	"network-firewall:CreateFirewallPolicy", "network-firewall:UpdateFirewallPolicy", "network-firewall:ListRuleGroups",
}

// PutResourcePolicy creates or replaces the resource-sharing policy of a rule
// group, firewall policy or firewall.
func (m *Mock) PutResourcePolicy(_ context.Context, resourceARN, policy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	kind, err := m.shareableKind(resourceARN)
	if err != nil {
		return err
	}

	if policy == "" || len(policy) > maxResourcePolicyLen {
		return invalid("Policy must be 1 to %d characters", maxResourcePolicyLen)
	}

	if err := validatePolicyDocument(policy, allowedPolicyActions[kind]); err != nil {
		return err
	}

	m.resourcePolicies[resourceARN] = policy

	return nil
}

// DescribeResourcePolicy returns the resource-sharing policy of a resource.
func (m *Mock) DescribeResourcePolicy(_ context.Context, resourceARN string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, err := m.shareableKind(resourceARN); err != nil {
		return "", err
	}

	p, ok := m.resourcePolicies[resourceARN]
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "no resource policy is attached to %q", resourceARN)
	}

	return p, nil
}

// DeleteResourcePolicy removes the resource-sharing policy of a resource.
func (m *Mock) DeleteResourcePolicy(_ context.Context, resourceARN string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.shareableKind(resourceARN); err != nil {
		return err
	}

	if _, ok := m.resourcePolicies[resourceARN]; !ok {
		return cerrors.Newf(cerrors.NotFound, "no resource policy is attached to %q", resourceARN)
	}

	delete(m.resourcePolicies, resourceARN)

	return nil
}

// shareableKind validates the ARN and returns its resource kind when it names an
// existing rule group, firewall policy or firewall.
func (m *Mock) shareableKind(arn string) (string, error) {
	if err := validateARN("ResourceArn", arn); err != nil {
		return "", err
	}

	kind := arnKind(arn)
	if _, ok := allowedPolicyActions[kind]; !ok {
		return "", invalid("ResourceArn %q is not a rule group, firewall policy or firewall", arn)
	}

	exists := false

	switch kind {
	case "firewall":
		_, exists = m.firewallByARN(arn)
	case "firewall-policy":
		exists = m.policyExists(arn)
	default:
		_, exists = m.ruleGroupByARN(arn)
	}

	if !exists {
		return "", cerrors.Newf(cerrors.NotFound, "resource %q not found", arn)
	}

	return kind, nil
}

func (m *Mock) firewallByARN(arn string) (*nfdriver.Firewall, bool) {
	return m.lookupFirewall("", arn)
}

// validatePolicyDocument parses an IAM policy document and checks that every
// statement grants only the allowed actions.
func validatePolicyDocument(doc string, allowed []string) error {
	var p struct {
		Statement json.RawMessage `json:"Statement"`
	}

	if err := json.Unmarshal([]byte(doc), &p); err != nil {
		return nfdriver.InvalidResourcePolicy("the policy is not valid JSON: %v", err)
	}

	stmts, err := policyStatements(p.Statement)
	if err != nil {
		return err
	}

	if len(stmts) == 0 {
		return nfdriver.InvalidResourcePolicy("the policy has no statements")
	}

	for _, s := range stmts {
		actions, err := stringOrList(s.Action)
		if err != nil || len(actions) == 0 {
			return nfdriver.InvalidResourcePolicy("every statement needs an Action")
		}

		for _, a := range actions {
			if !oneOf(a, allowed...) {
				return nfdriver.InvalidResourcePolicy("action %q cannot be granted on this resource (allowed: %v)", a, allowed)
			}
		}
	}

	return nil
}

type policyStatement struct {
	Action json.RawMessage `json:"Action"`
}

func policyStatements(raw json.RawMessage) ([]policyStatement, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var many []policyStatement
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}

	var one policyStatement
	if err := json.Unmarshal(raw, &one); err != nil {
		return nil, nfdriver.InvalidResourcePolicy("the policy Statement is not valid")
	}

	return []policyStatement{one}, nil
}

func stringOrList(raw json.RawMessage) ([]string, error) {
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}

	var one string
	if err := json.Unmarshal(raw, &one); err != nil {
		return nil, err //nolint:wrapcheck // caller only tests for failure
	}

	return []string{one}, nil
}
