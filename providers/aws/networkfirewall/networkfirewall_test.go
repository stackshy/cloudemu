package networkfirewall

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

func newMock() *Mock { return New(config.NewOptions()) }

// newClockMock returns a mock on a FakeClock so time-dependent output is exact.
func newClockMock(t *testing.T) (*Mock, *config.FakeClock) {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(clock))), clock
}

var (
	bg = context.Background()

	standardActions = []string{"aws:forward_to_sfe"}
)

func seedPolicy(t *testing.T, m *Mock, name string) *nfdriver.FirewallPolicy {
	t.Helper()

	p, err := m.CreateFirewallPolicy(bg, nfdriver.CreateFirewallPolicyConfig{
		Name: name, StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions,
	})
	require.NoError(t, err)

	return p
}

func seedFirewall(t *testing.T, m *Mock, name, policyARN string) *nfdriver.Firewall {
	t.Helper()

	fw, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: name, PolicyARN: policyARN, VPCID: "vpc-1a2b", SubnetIDs: []string{"subnet-1"},
	})
	require.NoError(t, err)

	return fw
}

func seedRuleGroup(t *testing.T, m *Mock, name, ruleType string, capacity int) *nfdriver.RuleGroup {
	t.Helper()

	rg, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{Name: name, Type: ruleType, Capacity: capacity})
	require.NoError(t, err)

	return rg
}

// seedStack creates a policy and a firewall using it.
func seedStack(t *testing.T, m *Mock) (*nfdriver.FirewallPolicy, *nfdriver.Firewall) {
	t.Helper()

	p := seedPolicy(t, m, "pol-1")

	return p, seedFirewall(t, m, "fw-1", p.ARN)
}

// requireException asserts err carries the given Network Firewall exception.
func requireException(t *testing.T, err error, want string) {
	t.Helper()

	got, ok := nfdriver.ExceptionOf(err)
	require.True(t, ok, "expected typed exception %s, got %v", want, err)
	assert.Equal(t, want, got)
}

func logDest(logType, destType string, dest map[string]string) nfdriver.LogDestinationConfig {
	return nfdriver.LogDestinationConfig{LogType: logType, LogDestinationType: destType, LogDestination: dest}
}

func s3Dest(logType, bucket string) nfdriver.LogDestinationConfig {
	return logDest(logType, "S3", map[string]string{"bucketName": bucket})
}

func rulesString(lines ...string) json.RawMessage {
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}

	b, _ := json.Marshal(map[string]any{"RulesSource": map[string]any{"RulesString": body}})

	return b
}

func TestFirewallLifecycle(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-1")

	_, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{})
	assert.True(t, cerrors.IsInvalidArgument(err), "firewall without name: %v", err)

	fw, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: "fw-1", PolicyARN: pol.ARN, VPCID: "vpc-1a", SubnetIDs: []string{"subnet-1"}, DeleteProtection: true,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, fw.ARN)
	assert.Equal(t, "READY", fw.Status)

	_, err = m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{Name: "fw-1", PolicyARN: pol.ARN, VPCID: "vpc-1a"})
	assert.True(t, cerrors.IsAlreadyExists(err), "duplicate firewall: %v", err)

	_, err = m.DeleteFirewall(bg, "fw-1", "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	got, err := m.DescribeFirewall(bg, "", fw.ARN)
	require.NoError(t, err)
	assert.Equal(t, "fw-1", got.Name)

	list, next, err := m.ListFirewalls(bg, nil, nfdriver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Empty(t, next)
}

func TestFirewallPolicyAndRuleGroup(t *testing.T) {
	m := newMock()

	pol := seedPolicy(t, m, "pol-1")
	assert.NotEmpty(t, pol.ID)

	_, err := m.DescribeFirewallPolicy(bg, "pol-1", "")
	require.NoError(t, err)

	_, err = m.DeleteFirewallPolicy(bg, "pol-1", "")
	require.NoError(t, err)

	_, err = m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{Name: "rg", Type: "BOGUS", Capacity: 1})
	assert.True(t, cerrors.IsInvalidArgument(err), "bad rule group type: %v", err)

	rg := seedRuleGroup(t, m, "rg-1", "STATEFUL", 100)

	got, err := m.DescribeRuleGroup(bg, "rg-1", "", "STATEFUL")
	require.NoError(t, err)
	assert.Equal(t, 100, got.Capacity)

	_, err = m.DeleteRuleGroup(bg, rg.Name, "", "STATEFUL")
	require.NoError(t, err)

	_, err = m.DescribeRuleGroup(bg, "rg-1", "", "STATEFUL")
	assert.True(t, cerrors.IsNotFound(err), "describe deleted rule group: %v", err)
}

func TestFirewallDepth(t *testing.T) {
	m := newMock()
	pol, fw := seedStack(t, m)
	other := seedPolicy(t, m, "pol-2")

	_, err := m.AssociateFirewallPolicy(bg, "fw-1", "arn:aws:network-firewall:us-east-1:000000000000:firewall-policy/nope", "")
	assert.True(t, cerrors.IsInvalidArgument(err), "associate missing policy: %v", err)

	_, err = m.AssociateFirewallPolicy(bg, "fw-1", other.ARN, "")
	require.NoError(t, err)

	// pol-1 is free again; pol-2 is now in use.
	_, err = m.DeleteFirewallPolicy(bg, "pol-2", "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_, err = m.DeleteFirewallPolicy(bg, "pol-1", "")
	require.NoError(t, err)

	_ = pol

	assoc, err := m.AssociateSubnets(bg, "fw-1", []string{"subnet-1", "subnet-2"}, "")
	require.NoError(t, err)
	assert.Len(t, assoc.SubnetIDs, 2)

	dis, err := m.DisassociateSubnets(bg, "fw-1", []string{"subnet-1"}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"subnet-2"}, dis.SubnetIDs)

	prot, err := m.UpdateFirewallDeleteProtection(bg, "fw-1", true, "")
	require.NoError(t, err)
	assert.True(t, prot.DeleteProtection)

	require.NoError(t, m.UpdateLoggingConfiguration(bg, "fw-1", &nfdriver.LoggingConfiguration{
		Destinations: []nfdriver.LogDestinationConfig{s3Dest("FLOW", "b")},
	}))
	require.NoError(t, m.UpdateLoggingConfiguration(bg, "fw-1", &nfdriver.LoggingConfiguration{
		Destinations: []nfdriver.LogDestinationConfig{s3Dest("FLOW", "b"), s3Dest("ALERT", "b")},
	}))

	logs, err := m.DescribeLoggingConfiguration(bg, "fw-1")
	require.NoError(t, err)
	assert.Len(t, logs.Destinations, 2)

	require.NoError(t, m.TagResource(bg, fw.ARN, map[string]string{"env": "prod"}))

	got, _ := m.DescribeFirewall(bg, "fw-1", "")
	assert.Equal(t, "prod", got.Tags["env"])

	require.NoError(t, m.UntagResource(bg, fw.ARN, []string{"env"}))

	got, _ = m.DescribeFirewall(bg, "fw-1", "")
	assert.NotContains(t, got.Tags, "env")

	_, err = m.AssociateFirewallPolicy(bg, "missing", pol.ARN, "")
	assert.True(t, cerrors.IsNotFound(err), "associate missing firewall: %v", err)

	err = m.TagResource(bg, "arn:aws:network-firewall:us-east-1:000000000000:firewall/nope", map[string]string{"a": "b"})
	assert.True(t, cerrors.IsNotFound(err), "tag unknown arn: %v", err)
}

// TestFirewallConcurrentAccess exercises the mutex under the race detector:
// concurrent logging writes + describes + tag mutations must not race or trip
// "concurrent map writes". Run with -race for it to be meaningful.
func TestFirewallConcurrentAccess(t *testing.T) {
	m := newMock()
	_, fw := seedStack(t, m)

	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = m.UpdateLoggingConfiguration(bg, "fw-1", &nfdriver.LoggingConfiguration{
				Destinations: []nfdriver.LogDestinationConfig{s3Dest("FLOW", "b")},
			})
			_, _ = m.DescribeLoggingConfiguration(bg, "fw-1")
			_, _ = m.DescribeFirewall(bg, "fw-1", "")
			_ = m.TagResource(bg, fw.ARN, map[string]string{"k": "v"})
			_, _, _ = m.ListFirewalls(bg, nil, nfdriver.ListOptions{})
		}()
	}

	wg.Wait()
}
