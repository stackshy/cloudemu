package networkfirewall

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

func refPolicy(name string, stateless, stateful []nfdriver.RuleGroupReference) nfdriver.CreateFirewallPolicyConfig {
	return nfdriver.CreateFirewallPolicyConfig{
		Name: name, StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions,
		StatelessRuleGroupReferences: stateless, StatefulRuleGroupReferences: stateful,
	}
}

func TestDeleteGuards_InvalidOperation(t *testing.T) {
	m := newMock()
	pol, fw := seedStack(t, m)

	_, err := m.UpdateFirewallDeleteProtection(bg, "fw-1", true, "")
	require.NoError(t, err)

	_, err = m.DeleteFirewall(bg, "fw-1", "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_, err = m.DeleteFirewallPolicy(bg, "pol-1", "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	rg := seedRuleGroup(t, m, "rg-1", "STATEFUL", 5)
	_, err = m.CreateFirewallPolicy(bg, refPolicy("p2", nil, []nfdriver.RuleGroupReference{{ResourceARN: rg.ARN}}))
	require.NoError(t, err)

	_, err = m.DeleteRuleGroup(bg, "rg-1", "", "STATEFUL")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_ = pol
	_ = fw
}

func TestDeleteRuleGroup_InUseByPolicy_Stateful_Stateless(t *testing.T) {
	m := newMock()
	sl := seedRuleGroup(t, m, "rg-sl", "STATELESS", 5)
	sf := seedRuleGroup(t, m, "rg-sf", "STATEFUL", 5)

	p, err := m.CreateFirewallPolicy(bg, refPolicy("p",
		[]nfdriver.RuleGroupReference{{ResourceARN: sl.ARN, Priority: 10}},
		[]nfdriver.RuleGroupReference{{ResourceARN: sf.ARN}}))
	require.NoError(t, err)

	_, err = m.DeleteRuleGroup(bg, "rg-sl", "", "STATELESS")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_, err = m.DeleteRuleGroup(bg, "", sf.ARN, "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	// Dropping the references frees the rule groups.
	_, err = m.UpdateFirewallPolicy(bg, "p", "", nfdriver.UpdateFirewallPolicyConfig{
		UpdateToken: p.UpdateToken, StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions,
	})
	require.NoError(t, err)

	_, err = m.DeleteRuleGroup(bg, "rg-sl", "", "STATELESS")
	require.NoError(t, err)

	_, err = m.DeleteRuleGroup(bg, "rg-sf", "", "STATEFUL")
	require.NoError(t, err)
}

func TestComputed_RuleGroupFields(t *testing.T) {
	m, clock := newClockMock(t)

	stateless := []byte(`{"RulesSource":{"StatelessRulesAndCustomActions":{"StatelessRules":[` +
		`{"Priority":1,"RuleDefinition":{"Actions":["aws:pass"],"MatchAttributes":{"Protocols":[6,17],` +
		`"Sources":[{"AddressDefinition":"10.0.0.0/24"},{"AddressDefinition":"10.0.1.0/24"},{"AddressDefinition":"10.0.2.0/24"}]}}},` +
		`{"Priority":2,"RuleDefinition":{"Actions":["aws:drop"],"MatchAttributes":{}}}]}}}`)

	sl, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{Name: "sl", Type: "STATELESS", Capacity: 10, Rules: stateless})
	require.NoError(t, err)
	assert.Equal(t, 7, sl.ConsumedCapacity, "2 protocols x 3 sources = 6, plus 1 for the unconstrained rule")
	assert.Equal(t, "ACTIVE", sl.Status)
	assert.Equal(t, clock.Now(), sl.LastModified)

	sf, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{
		Name: "sf", Type: "STATEFUL", Capacity: 10, Rules: rulesString("r1", "# note", "", "r2"),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, sf.ConsumedCapacity, "one unit per rule, comments and blanks excluded")

	structured, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{
		Name: "sf2", Type: "STATEFUL", Capacity: 10, Rules: []byte(`{"RulesSource":{"StatefulRules":[{"Action":"PASS"},{"Action":"DROP"},{"Action":"ALERT"}]}}`),
	})
	require.NoError(t, err)
	assert.Equal(t, 3, structured.ConsumedCapacity)

	_, err = m.CreateFirewallPolicy(bg, refPolicy("p", []nfdriver.RuleGroupReference{{ResourceARN: sl.ARN, Priority: 1}}, nil))
	require.NoError(t, err)

	got, _ := m.DescribeRuleGroup(bg, "sl", "", "STATELESS")
	assert.Equal(t, 1, got.NumberOfAssociations)

	clock.Advance(time.Hour)

	later, _ := m.DescribeRuleGroup(bg, "sl", "", "STATELESS")
	assert.Equal(t, got.LastModified, later.LastModified, "reads never move the timestamp")

	updated, err := m.UpdateRuleGroup(bg, "sl", "", "STATELESS", nfdriver.UpdateRuleGroupConfig{UpdateToken: got.UpdateToken, Description: "x"})
	require.NoError(t, err)
	assert.True(t, updated.LastModified.After(got.LastModified), "a mutation does")
}

func TestComputed_PolicyFields(t *testing.T) {
	m := newMock()
	sl := seedRuleGroup(t, m, "rg-sl", "STATELESS", 20)
	sf := seedRuleGroup(t, m, "rg-sf", "STATEFUL", 30)

	domain, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{Name: "rg-d", Type: "STATEFUL_DOMAIN", Capacity: 40})
	require.NoError(t, err)

	p, err := m.CreateFirewallPolicy(bg, refPolicy("p",
		[]nfdriver.RuleGroupReference{{ResourceARN: sl.ARN, Priority: 1}},
		[]nfdriver.RuleGroupReference{{ResourceARN: sf.ARN}, {ResourceARN: domain.ARN}}))
	require.NoError(t, err)

	assert.Equal(t, "ACTIVE", p.Status)
	assert.Equal(t, 20, p.ConsumedStatelessRuleCapacity)
	assert.Equal(t, 30, p.ConsumedStatefulRuleCapacity)
	assert.Equal(t, 40, p.ConsumedStatefulDomainCapacity)
	assert.Equal(t, 0, p.NumberOfAssociations)

	seedFirewall(t, m, "fw-a", p.ARN)
	seedFirewall(t, m, "fw-b", p.ARN)

	got, _ := m.DescribeFirewallPolicy(bg, "p", "")
	assert.Equal(t, 2, got.NumberOfAssociations)
}

func TestComputed_FirewallFields(t *testing.T) {
	m := newMock()
	_, fw := seedStack(t, m)

	assert.Equal(t, "READY", fw.Status)
	assert.Equal(t, 0, fw.NumberOfAssociations)

	vpcAssociation(t, m, fw.ARN, "subnet-a")
	vpcAssociation(t, m, fw.ARN, "subnet-b")

	got, _ := m.DescribeFirewall(bg, "fw-1", "")
	assert.Equal(t, 2, got.NumberOfAssociations)
}

func TestListFirewalls_VpcIdsFilter(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-1")

	for name, vpc := range map[string]string{"fw-a": "vpc-aa", "fw-b": "vpc-bb", "fw-c": "vpc-aa"} {
		_, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{Name: name, PolicyARN: pol.ARN, VPCID: vpc})
		require.NoError(t, err)
	}

	names := func(vpcs ...string) []string {
		items, _, err := m.ListFirewalls(bg, vpcs, nfdriver.ListOptions{})
		require.NoError(t, err)

		out := make([]string, 0, len(items))
		for _, f := range items {
			out = append(out, f.Name)
		}

		return out
	}

	assert.Equal(t, []string{"fw-a", "fw-b", "fw-c"}, names())
	assert.Equal(t, []string{"fw-a", "fw-c"}, names("vpc-aa"))
	assert.Equal(t, []string{"fw-a", "fw-b", "fw-c"}, names("vpc-aa", "vpc-bb"))
	assert.Empty(t, names("vpc-ff"))

	_, _, err := m.ListFirewalls(bg, []string{"not-a-vpc"}, nfdriver.ListOptions{})
	assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)
}

func TestListRuleGroups_TypeScopeFilters(t *testing.T) {
	m := newMock()
	seedRuleGroup(t, m, "a-sl", "STATELESS", 1)
	seedRuleGroup(t, m, "b-sf", "STATEFUL", 1)

	count := func(f nfdriver.RuleGroupFilter) int {
		items, _, err := m.ListRuleGroups(bg, f, nfdriver.ListOptions{})
		require.NoError(t, err)

		return len(items)
	}

	assert.Equal(t, 2, count(nfdriver.RuleGroupFilter{}))
	assert.Equal(t, 1, count(nfdriver.RuleGroupFilter{Type: "STATELESS"}))
	assert.Equal(t, 2, count(nfdriver.RuleGroupFilter{Scope: "ACCOUNT"}))
	assert.Equal(t, 0, count(nfdriver.RuleGroupFilter{Scope: "MANAGED"}), "the emulator has no AWS-managed rule groups")
	assert.Equal(t, 0, count(nfdriver.RuleGroupFilter{ManagedType: "AWS_MANAGED_DOMAIN_LISTS"}))

	for _, bad := range []nfdriver.RuleGroupFilter{{Type: "X"}, {Scope: "X"}, {ManagedType: "X"}} {
		_, _, err := m.ListRuleGroups(bg, bad, nfdriver.ListOptions{})
		assert.True(t, cerrors.IsInvalidArgument(err), "%+v: %v", bad, err)
	}
}

func TestARNFormats(t *testing.T) {
	m := newMock()
	pol, fw := seedStack(t, m)
	sl := seedRuleGroup(t, m, "rg-sl", "STATELESS", 1)
	sf := seedRuleGroup(t, m, "rg-sf", "STATEFUL", 1)

	prefix := "arn:aws:network-firewall:us-east-1:123456789012:"
	assert.Equal(t, prefix+"firewall/fw-1", fw.ARN)
	assert.Equal(t, prefix+"firewall-policy/pol-1", pol.ARN)
	assert.Equal(t, prefix+"stateless-rulegroup/rg-sl", sl.ARN)
	assert.Equal(t, prefix+"stateful-rulegroup/rg-sf", sf.ARN)

	assoc := vpcAssociation(t, m, fw.ARN, "subnet-a")
	assert.Contains(t, assoc.ARN, prefix+"vpc-endpoint-association/")

	for arn, kind := range map[string]string{fw.ARN: "firewall", pol.ARN: "firewall-policy", sl.ARN: "stateless-rulegroup", "x": ""} {
		assert.Equal(t, kind, arnKind(arn))
	}
}

func TestMalformedARN_BeforeLookup(t *testing.T) {
	m := newMock()
	seedStack(t, m)

	for name, call := range map[string]func() error{
		"describe firewall":   func() error { _, err := m.DescribeFirewall(bg, "", "bogus"); return err },
		"delete firewall":     func() error { _, err := m.DeleteFirewall(bg, "", "bogus"); return err },
		"describe policy":     func() error { _, err := m.DescribeFirewallPolicy(bg, "", "bogus"); return err },
		"delete policy":       func() error { _, err := m.DeleteFirewallPolicy(bg, "", "bogus"); return err },
		"describe rule group": func() error { _, err := m.DescribeRuleGroup(bg, "", "bogus", ""); return err },
		"delete rule group":   func() error { _, err := m.DeleteRuleGroup(bg, "", "bogus", ""); return err },
		"describe tls":        func() error { _, err := m.DescribeTLSInspectionConfiguration(bg, "", "bogus"); return err },
		"describe assoc":      func() error { _, err := m.DescribeVpcEndpointAssociation(bg, "bogus"); return err },
		"tag":                 func() error { return m.TagResource(bg, "bogus", map[string]string{"a": "b"}) },
		"untag":               func() error { return m.UntagResource(bg, "bogus", nil) },
		"list tags":           func() error { _, err := m.ListTagsForResource(bg, "bogus"); return err },
		"no identifier":       func() error { _, err := m.DescribeFirewall(bg, "", ""); return err },
		"metadata":            func() error { _, err := m.DescribeFirewallMetadata(bg, "bogus"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			assert.True(t, cerrors.IsInvalidArgument(call()), "a malformed ARN is a request error, not NotFound")
		})
	}

	_, err := m.DescribeFirewall(bg, "", "arn:aws:network-firewall:us-east-1:000000000000:firewall/ghost")
	assert.True(t, cerrors.IsNotFound(err), "a well-formed unknown ARN is NotFound: %v", err)
}

func TestNameAndARNDisagree(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-1")
	seedFirewall(t, m, "fw-a", pol.ARN)
	other := seedFirewall(t, m, "fw-b", pol.ARN)

	// When both are supplied the name selects the resource, as before this change.
	got, err := m.DescribeFirewall(bg, "fw-a", other.ARN)
	require.NoError(t, err)
	assert.Equal(t, "fw-a", got.Name)
}

func TestPagination_AllLists_PageSize1_Walk(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-b")
	seedPolicy(t, m, "pol-c")

	for i := 0; i < 3; i++ {
		seedFirewall(t, m, fmt.Sprintf("fw-%d", 3-i), pol.ARN)
		seedRuleGroup(t, m, fmt.Sprintf("rg-%d", 3-i), "STATEFUL", 1)

		_, err := m.CreateTLSInspectionConfiguration(bg, nfdriver.CreateTLSInspectionConfig{
			Name: fmt.Sprintf("tls-%d", 3-i), Configuration: tlsConfigJSON("c"),
		})
		require.NoError(t, err)
	}

	// A policy created last sorts first, proving order is by name, not insertion.
	seedPolicy(t, m, "pol-a")

	fw, _ := m.DescribeFirewall(bg, "fw-1", "")
	for i := 0; i < 3; i++ {
		vpcAssociation(t, m, fw.ARN, fmt.Sprintf("subnet-%d", i))
	}

	_, err := m.UpdateFirewallAnalysisSettings(bg, "fw-1", []string{"TLS_SNI"}, "")
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		_, err := m.StartAnalysisReport(bg, "fw-1", "", "TLS_SNI")
		require.NoError(t, err)
	}

	scope := nfdriver.FlowOperationScope{FirewallARN: fw.ARN, AvailabilityZone: seedZone}
	for i := 0; i < 3; i++ {
		_, err := m.StartFlowFlush(bg, nfdriver.FlowOperationRequest{FirewallARN: fw.ARN, AvailabilityZone: seedZone})
		require.NoError(t, err)
	}

	lists := map[string]struct {
		want int
		page func(opts nfdriver.ListOptions) (int, string, error)
	}{
		"firewalls": {3, func(o nfdriver.ListOptions) (int, string, error) {
			v, n, err := m.ListFirewalls(bg, nil, o)
			return len(v), n, err
		}},
		"policies": {3, func(o nfdriver.ListOptions) (int, string, error) {
			v, n, err := m.ListFirewallPolicies(bg, o)
			return len(v), n, err
		}},
		"rule groups": {3, func(o nfdriver.ListOptions) (int, string, error) {
			v, n, err := m.ListRuleGroups(bg, nfdriver.RuleGroupFilter{}, o)
			return len(v), n, err
		}},
		"tls": {3, func(o nfdriver.ListOptions) (int, string, error) {
			v, n, err := m.ListTLSInspectionConfigurations(bg, o)
			return len(v), n, err
		}},
		"associations": {3, func(o nfdriver.ListOptions) (int, string, error) {
			v, n, err := m.ListVpcEndpointAssociations(bg, fw.ARN, o)
			return len(v), n, err
		}},
		"analysis reports": {3, func(o nfdriver.ListOptions) (int, string, error) {
			v, n, err := m.ListAnalysisReports(bg, "fw-1", "", o)
			return len(v), n, err
		}},
		"flow operations": {3, func(o nfdriver.ListOptions) (int, string, error) {
			v, n, err := m.ListFlowOperations(bg, scope, "", o)
			return len(v), n, err
		}},
	}

	for name, l := range lists {
		t.Run(name, func(t *testing.T) {
			seen, token := 0, ""

			for pages := 0; pages < 10; pages++ {
				n, next, err := l.page(nfdriver.ListOptions{MaxResults: 1, NextToken: token})
				require.NoError(t, err)
				assert.Equal(t, 1, n)

				seen += n

				if next == "" {
					break
				}

				token = next
			}

			assert.Equal(t, l.want, seen, "every item exactly once")
		})
	}

	// Firewalls come back sorted by name regardless of creation order.
	items, _, err := m.ListFirewalls(bg, nil, nfdriver.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, "fw-1", items[0].Name)

	pols, _, err := m.ListFirewallPolicies(bg, nfdriver.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, "pol-a", pols[0].Name)
}

func TestPagination_InvalidTokenAndRange(t *testing.T) {
	m := newMock()
	seedPolicy(t, m, "pol-1")

	for _, opts := range []nfdriver.ListOptions{{MaxResults: -1}, {MaxResults: 101}, {NextToken: "!!!not-a-token"}} {
		_, _, err := m.ListFirewallPolicies(bg, opts)
		assert.True(t, cerrors.IsInvalidArgument(err), "%+v: %v", opts, err)
	}

	items, next, err := m.ListFirewallPolicies(bg, nfdriver.ListOptions{MaxResults: 100})
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Empty(t, next, "the last page has no token")
}

func TestPagination_HugeMaxResults_Bounded(t *testing.T) {
	m := newMock()
	seedPolicy(t, m, "pol-1")

	_, _, err := m.ListFirewallPolicies(bg, nfdriver.ListOptions{MaxResults: 1 << 30})
	assert.True(t, cerrors.IsInvalidArgument(err), "MaxResults above 100 is rejected, never used to size an allocation: %v", err)
}
