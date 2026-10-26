package networkfirewall

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

// Suricata rule options a summary reports.
var (
	ruleMsgRE      = regexp.MustCompile(`msg:\s*"([^"]*)"\s*;`)
	ruleSIDRE      = regexp.MustCompile(`sid:\s*(\d+)\s*;`)
	ruleMetadataRE = regexp.MustCompile(`metadata:\s*([^;]*);`)
)

// DescribeFirewallMetadata returns the firewall's metadata, as visible to an
// account a firewall is shared with.
func (m *Mock) DescribeFirewallMetadata(_ context.Context, firewallARN string) (*nfdriver.FirewallMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := validateARN("FirewallArn", firewallARN); err != nil {
		return nil, err
	}

	fw, ok := m.firewallByARN(firewallARN)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall %q not found", firewallARN)
	}

	return &nfdriver.FirewallMetadata{
		FirewallARN:       fw.ARN,
		PolicyARN:         fw.PolicyARN,
		Description:       fw.Description,
		Status:            fw.Status,
		AvailabilityZones: syncZones(fw),
	}, nil
}

// DescribeRuleGroupMetadata returns the rule group (the wire layer reports its
// metadata subset).
func (m *Mock) DescribeRuleGroupMetadata(_ context.Context, name, arn, ruleType string) (*nfdriver.RuleGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rg, err := m.findRuleGroup(name, arn, ruleType)
	if err != nil {
		return nil, err
	}

	return m.outRuleGroup(rg), nil
}

// DescribeRuleGroupSummary returns the rule group and the per-rule summaries
// parsed from its Suricata rules. Only the options listed in the rule group's
// SummaryConfiguration are populated. Only stateful rule groups carry rules text.
func (m *Mock) DescribeRuleGroupSummary(
	_ context.Context, name, arn, ruleType string,
) (*nfdriver.RuleGroup, []nfdriver.RuleSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rg, err := m.findRuleGroup(name, arn, ruleType)
	if err != nil {
		return nil, nil, err
	}

	if rg.Type == ruleTypeStateless {
		return nil, nil, invalid("rule group summaries are only available for stateful rule groups")
	}

	return m.outRuleGroup(rg), ruleSummaries(rg), nil
}

func ruleSummaries(rg *nfdriver.RuleGroup) []nfdriver.RuleSummary {
	var src ruleSource
	if err := json.Unmarshal(rg.Rules, &src); err != nil {
		return nil
	}

	want := make(map[string]bool, len(rg.SummaryRuleOptions))
	for _, o := range rg.SummaryRuleOptions {
		want[o] = true
	}

	lines := strings.Split(src.RulesSource.RulesString, "\n")
	out := make([]nfdriver.RuleSummary, 0, len(lines))

	for _, line := range lines {
		if !isRuleLine(line) {
			continue
		}

		var s nfdriver.RuleSummary
		if want["MSG"] {
			s.Msg = firstGroup(ruleMsgRE, line)
		}

		if want["SID"] {
			s.SID = firstGroup(ruleSIDRE, line)
		}

		if want["METADATA"] {
			s.Metadata = strings.TrimSpace(firstGroup(ruleMetadataRE, line))
		}

		out = append(out, s)
	}

	return out
}

func firstGroup(re *regexp.Regexp, s string) string {
	if g := re.FindStringSubmatch(s); len(g) > 1 {
		return g[1]
	}

	return ""
}
