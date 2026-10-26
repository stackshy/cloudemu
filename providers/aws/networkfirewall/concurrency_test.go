package networkfirewall

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const racers = 50

// runRacers calls fn from racers goroutines and counts successes and
// "already exists" losers.
func runRacers(fn func() error) (wins, losers int32) {
	var (
		wg   sync.WaitGroup
		w, l atomic.Int32
	)

	for i := 0; i < racers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			switch err := fn(); {
			case err == nil:
				w.Add(1)
			case cerrors.IsAlreadyExists(err):
				l.Add(1)
			}
		}()
	}

	wg.Wait()

	return w.Load(), l.Load()
}

func TestCreate_50SameName_ExactlyOneWins(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-1")

	creates := map[string]func() error{
		"firewall": func() error {
			_, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{Name: "fw", PolicyARN: pol.ARN, VPCID: "vpc-1a"})
			return err
		},
		"policy": func() error {
			_, err := m.CreateFirewallPolicy(bg, refPolicy("same", nil, nil))
			return err
		},
		"rule group": func() error {
			_, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{Name: "rg", Type: "STATEFUL", Capacity: 1})
			return err
		},
		"tls": func() error {
			_, err := m.CreateTLSInspectionConfiguration(bg, nfdriver.CreateTLSInspectionConfig{Name: "tls", Configuration: tlsConfigJSON("c")})
			return err
		},
	}

	for name, create := range creates {
		t.Run(name, func(t *testing.T) {
			wins, losers := runRacers(create)
			assert.EqualValues(t, 1, wins)
			assert.EqualValues(t, racers-1, losers)
		})
	}
}

func TestRuleGroup_DeleteVsPolicyReference_Race(t *testing.T) {
	for round := 0; round < 25; round++ {
		m := newMock()
		rg := seedRuleGroup(t, m, "rg", "STATEFUL", 5)
		pol := seedPolicy(t, m, "pol")

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = m.UpdateFirewallPolicy(bg, "pol", "", nfdriver.UpdateFirewallPolicyConfig{
				UpdateToken: pol.UpdateToken, StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions,
				StatefulRuleGroupReferences: []nfdriver.RuleGroupReference{{ResourceARN: rg.ARN}},
			})
		}()

		go func() {
			defer wg.Done()

			_, _ = m.DeleteRuleGroup(bg, "rg", "", "STATEFUL")
		}()

		wg.Wait()

		// Whatever order won, a policy never points at a deleted rule group.
		got, err := m.DescribeFirewallPolicy(bg, "pol", "")
		require.NoError(t, err)

		for _, ref := range got.StatefulRuleGroupReferences {
			_, err := m.DescribeRuleGroup(bg, "", ref.ResourceARN, "")
			assert.NoError(t, err, "round %d: dangling reference", round)
		}
	}
}

func TestCreateVsDelete_NoOrphans(t *testing.T) {
	for round := 0; round < 25; round++ {
		m := newMock()
		_, fw := seedStack(t, m)

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = m.CreateVpcEndpointAssociation(bg, nfdriver.CreateVPCEndpointAssociationConfig{
				FirewallARN: fw.ARN, VPCID: "vpc-2b", SubnetID: "subnet-9",
			})
		}()

		go func() {
			defer wg.Done()

			_, _ = m.DeleteFirewall(bg, "fw-1", "")
		}()

		wg.Wait()

		// Either the association exists and the firewall survived (delete refused),
		// or the firewall is gone and no association points at it.
		_, fwErr := m.DescribeFirewall(bg, "fw-1", "")
		assocs, _, err := m.ListVpcEndpointAssociations(bg, "", nfdriver.ListOptions{})
		require.NoError(t, err)

		if fwErr != nil {
			assert.Empty(t, assocs, "round %d: association orphaned by firewall delete", round)
		}
	}
}

func TestDeepCopy_MutateReturned_NoAlias(t *testing.T) {
	m := newMock()
	pol, fw := seedStack(t, m)

	_, err := m.CreateFirewallPolicy(bg, nfdriver.CreateFirewallPolicyConfig{
		Name: "deep", StatelessDefaultActions: []string{"aws:pass"}, StatelessFragmentDefaultActions: standardActions,
		Tags: map[string]string{"k": "v"}, Extra: nfdriver.PolicyExtra{StatelessCustomActions: []byte(`[]`)},
	})
	require.NoError(t, err)

	rg, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{
		Name: "rg", Type: "STATEFUL", Capacity: 5, Rules: rulesString("r1"), Tags: map[string]string{"k": "v"},
		SummaryRuleOptions: []string{"SID"},
	})
	require.NoError(t, err)

	require.NoError(t, m.UpdateLoggingConfiguration(bg, "fw-1", &nfdriver.LoggingConfiguration{
		Destinations: []nfdriver.LogDestinationConfig{s3Dest("FLOW", "b")},
	}))

	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()

			// Mutators scribble on everything they were handed.
			got, _ := m.DescribeFirewall(bg, "fw-1", "")
			got.SubnetIDs[0] = "scribbled"
			got.Tags = map[string]string{"x": "y"}

			p, _ := m.DescribeFirewallPolicy(bg, "deep", "")
			p.StatelessDefaultActions[0] = "scribbled"
			p.Tags["k"] = "scribbled"
			p.Extra.StatelessCustomActions[0] = 'X'

			r, _ := m.DescribeRuleGroup(bg, "rg", "", "STATEFUL")
			r.Rules[0] = 'X'
			r.Tags["k"] = "scribbled"
			r.SummaryRuleOptions[0] = "scribbled"

			l, _ := m.DescribeLoggingConfiguration(bg, "fw-1")
			l.Destinations[0].LogDestination["bucketName"] = "scribbled"
		}()

		go func() {
			defer wg.Done()

			_, _ = m.DescribeFirewall(bg, "fw-1", "")
			_, _, _ = m.ListFirewallPolicies(bg, nfdriver.ListOptions{})
			_, _, _ = m.ListRuleGroups(bg, nfdriver.RuleGroupFilter{}, nfdriver.ListOptions{})
			_, _ = m.DescribeLoggingConfiguration(bg, "fw-1")
		}()
	}

	wg.Wait()

	gotFw, _ := m.DescribeFirewall(bg, "fw-1", "")
	assert.Equal(t, []string{"subnet-1"}, gotFw.SubnetIDs)
	assert.Empty(t, gotFw.Tags)

	gotPol, _ := m.DescribeFirewallPolicy(bg, "deep", "")
	assert.Equal(t, []string{"aws:pass"}, gotPol.StatelessDefaultActions)
	assert.Equal(t, map[string]string{"k": "v"}, gotPol.Tags)
	assert.Equal(t, "[]", string(gotPol.Extra.StatelessCustomActions))

	gotRg, _ := m.DescribeRuleGroup(bg, "rg", "", "STATEFUL")
	assert.Equal(t, rg.Rules, gotRg.Rules)
	assert.Equal(t, map[string]string{"k": "v"}, gotRg.Tags)
	assert.Equal(t, []string{"SID"}, gotRg.SummaryRuleOptions)

	gotLog, _ := m.DescribeLoggingConfiguration(bg, "fw-1")
	assert.Equal(t, "b", gotLog.Destinations[0].LogDestination["bucketName"])

	_, _ = pol, fw
}

func TestCreateFirewall_CallerSliceNotRetained(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-1")

	subnets := []string{"subnet-1"}
	tags := map[string]string{"a": "b"}

	_, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{Name: "fw", PolicyARN: pol.ARN, VPCID: "vpc-1a", SubnetIDs: subnets, Tags: tags})
	require.NoError(t, err)

	subnets[0] = fmt.Sprint("mutated")
	tags["a"] = "mutated"

	got, _ := m.DescribeFirewall(bg, "fw", "")
	assert.Equal(t, []string{"subnet-1"}, got.SubnetIDs)
	assert.Equal(t, "b", got.Tags["a"])
}
