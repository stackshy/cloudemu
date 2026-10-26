package networkfirewall

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
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

	assoc, err := m.AssociateSubnets(bg, "fw-1", []nfdriver.SubnetMapping{{SubnetID: "subnet-1"}, {SubnetID: "subnet-2"}}, "")
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

func TestRuleGroup_StatefulAndDomainShareNamespace(t *testing.T) {
	m := newMock()

	stateful := seedRuleGroup(t, m, "dup", "STATEFUL", 10)

	_, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{Name: "dup", Type: "STATEFUL_DOMAIN", Capacity: 10})
	require.Error(t, err)
	assert.True(t, cerrors.IsAlreadyExists(err))

	// The ARN of the first group still resolves to it, and it is the only one.
	got, err := m.DescribeRuleGroup(bg, "", stateful.ARN, "")
	require.NoError(t, err)
	assert.Equal(t, "STATEFUL", got.Type)

	list, _, err := m.ListRuleGroups(bg, nfdriver.RuleGroupFilter{}, nfdriver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestRuleGroup_NameWithoutTypeIsRejected(t *testing.T) {
	m := newMock()
	seedRuleGroup(t, m, "y", "STATELESS", 10)
	seedRuleGroup(t, m, "y", "STATEFUL", 10)

	_, err := m.DescribeRuleGroup(bg, "y", "", "")
	require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)

	_, err = m.DescribeRuleGroupMetadata(bg, "y", "", "")
	require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)

	got, err := m.DescribeRuleGroup(bg, "y", "", "STATEFUL")
	require.NoError(t, err)
	assert.Equal(t, "STATEFUL", got.Type)
}

func TestTags_RetagAtLimitAllowed_NewKeyRefused(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-1")

	full := make(map[string]string, maxTags)
	for i := 0; i < maxTags; i++ {
		full[fmt.Sprintf("k%d", i)] = "v"
	}

	require.NoError(t, m.TagResource(bg, pol.ARN, full))

	// Same keys, new values: not new tags, so allowed.
	require.NoError(t, m.TagResource(bg, pol.ARN, map[string]string{"k0": "changed", "k1": "changed"}))

	err := m.TagResource(bg, pol.ARN, map[string]string{"extra": "v"})
	require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)
}

func TestSubnets_EmptyAndUnknownIDsRejected(t *testing.T) {
	m := newMock()
	_, fw := seedStack(t, m)

	_, err := m.AssociateSubnets(bg, "fw-1", []nfdriver.SubnetMapping{{SubnetID: ""}}, "")
	require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)

	_, err = m.DisassociateSubnets(bg, "fw-1", []string{"subnet-unknown"}, "")
	require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)

	got, err := m.DescribeFirewall(bg, "fw-1", "")
	require.NoError(t, err)
	assert.Equal(t, fw.UpdateToken, got.UpdateToken, "a rejected call must not rotate the token")
	assert.Equal(t, []string{"subnet-1"}, got.SubnetIDs)

	_, err = m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: "fw-empty", PolicyARN: fw.PolicyARN, VPCID: "vpc-1a2b", SubnetIDs: []string{""},
	})
	require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)
}

func TestSubnets_ZonePerSubnetIsStableAndUnique(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-1")

	fw, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: "fw-az", PolicyARN: pol.ARN, VPCID: "vpc-1a2b", SubnetIDs: []string{"subnet-a", "subnet-b"},
		SubnetIPTypes: map[string]string{"subnet-a": "IPV4"},
	})
	require.NoError(t, err)

	za, zb := fw.SubnetDetails["subnet-a"], fw.SubnetDetails["subnet-b"]
	assert.NotEmpty(t, za.AvailabilityZone)
	assert.NotEqual(t, za.AvailabilityZone, zb.AvailabilityZone, "one subnet per zone")
	assert.NotEqual(t, za.EndpointID, zb.EndpointID, "each zone has its own endpoint")
	assert.Equal(t, "IPV4", za.IPAddressType)

	// Removing the first subnet must not move the second one.
	after, err := m.DisassociateSubnets(bg, "fw-az", []string{"subnet-a"}, "")
	require.NoError(t, err)
	assert.Equal(t, zb, after.SubnetDetails["subnet-b"])
	assert.NotContains(t, after.SubnetDetails, "subnet-a")

	// The zone a flow operation accepts is the zone the subnet reports.
	_, err = m.StartFlowCapture(bg, nfdriver.FlowOperationRequest{FirewallARN: fw.ARN, AvailabilityZone: zb.AvailabilityZone})
	require.NoError(t, err)

	meta, err := m.DescribeFirewallMetadata(bg, fw.ARN)
	require.NoError(t, err)
	assert.Equal(t, []string{zb.AvailabilityZone}, meta.AvailabilityZones)

	// A VPC endpoint association for the same subnet lands in the same zone.
	assoc, err := m.CreateVpcEndpointAssociation(bg, nfdriver.CreateVPCEndpointAssociationConfig{
		FirewallARN: fw.ARN, VPCID: "vpc-1a2b", SubnetID: "subnet-b",
	})
	require.NoError(t, err)
	assert.Equal(t, zb.AvailabilityZone, assoc.AvailabilityZone)
}

func TestSubnets_AssociateKeepsIPAddressType(t *testing.T) {
	m := newMock()
	seedStack(t, m)

	fw, err := m.AssociateSubnets(bg, "fw-1", []nfdriver.SubnetMapping{{SubnetID: "subnet-2", IPAddressType: "DUALSTACK"}}, "")
	require.NoError(t, err)
	assert.Equal(t, "DUALSTACK", fw.SubnetDetails["subnet-2"].IPAddressType)

	_, err = m.AssociateSubnets(bg, "fw-1", []nfdriver.SubnetMapping{{SubnetID: "subnet-3", IPAddressType: "BOGUS"}}, "")
	require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)
}

func TestSnapshot_FirewallWithoutSubnetDetailsGetsZones(t *testing.T) {
	m := newMock()
	_, fw := seedStack(t, m)

	m.mu.Lock()
	stored, _ := m.firewalls.Get(fw.Name)
	stored.SubnetDetails = nil
	m.backfillRestoredState()
	m.mu.Unlock()

	got, err := m.DescribeFirewall(bg, "fw-1", "")
	require.NoError(t, err)
	assert.NotEmpty(t, got.SubnetDetails["subnet-1"].AvailabilityZone)
}

// seedZone is the Availability Zone the emulator assigns "subnet-1", the subnet
// every seeded firewall is attached to.
var seedZone = zoneForKey("us-east-1", "subnet-1")

// fakeSubnets answers DescribeSubnets from a fixed subnet-to-zone map.
type fakeSubnets map[string]string

func (f fakeSubnets) DescribeSubnets(_ context.Context, ids []string) ([]netdriver.SubnetInfo, error) {
	var out []netdriver.SubnetInfo

	for _, id := range ids {
		if az, ok := f[id]; ok {
			out = append(out, netdriver.SubnetInfo{ID: id, AvailabilityZone: az})
		}
	}

	return out, nil
}

func TestSubnets_ZoneIsTheSubnetsRealZone(t *testing.T) {
	m := newMock()
	m.SetSubnetResolver(fakeSubnets{"subnet-a": "us-east-1a", "subnet-b": "us-east-1b", "subnet-c": "us-east-1c"})

	pol := seedPolicy(t, m, "pol-1")

	fw, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: "fw-real", PolicyARN: pol.ARN, VPCID: "vpc-1a2b", SubnetIDs: []string{"subnet-a", "subnet-b"},
	})
	require.NoError(t, err)

	assert.Equal(t, "us-east-1a", fw.SubnetDetails["subnet-a"].AvailabilityZone)
	assert.Equal(t, "us-east-1b", fw.SubnetDetails["subnet-b"].AvailabilityZone)
	assert.NotEqual(t, fw.SubnetDetails["subnet-a"].EndpointID, fw.SubnetDetails["subnet-b"].EndpointID)

	// A one-subnet firewall in us-east-1c takes flow operations for us-east-1c.
	one, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: "fw-one", PolicyARN: pol.ARN, VPCID: "vpc-1a2b", SubnetIDs: []string{"subnet-c"},
	})
	require.NoError(t, err)

	_, err = m.StartFlowCapture(bg, nfdriver.FlowOperationRequest{FirewallARN: one.ARN, AvailabilityZone: "us-east-1c"})
	require.NoError(t, err)

	meta, err := m.DescribeFirewallMetadata(bg, one.ARN)
	require.NoError(t, err)
	assert.Equal(t, []string{"us-east-1c"}, meta.AvailabilityZones)

	// A VPC endpoint association for an unattached known subnet reports its real zone.
	assoc, err := m.CreateVpcEndpointAssociation(bg, nfdriver.CreateVPCEndpointAssociationConfig{
		FirewallARN: one.ARN, VPCID: "vpc-1a2b", SubnetID: "subnet-a",
	})
	require.NoError(t, err)
	assert.Equal(t, "us-east-1a", assoc.AvailabilityZone)
}

func TestSubnets_SecondSubnetInUsedZoneRefused(t *testing.T) {
	m := newMock()
	m.SetSubnetResolver(fakeSubnets{"subnet-a": "us-east-1a", "subnet-a2": "us-east-1a", "subnet-b": "us-east-1b"})

	pol := seedPolicy(t, m, "pol-1")

	_, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: "fw-dup", PolicyARN: pol.ARN, VPCID: "vpc-1a2b", SubnetIDs: []string{"subnet-a", "subnet-a2"},
	})
	require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)

	fw, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: "fw-ok", PolicyARN: pol.ARN, VPCID: "vpc-1a2b", SubnetIDs: []string{"subnet-a"},
	})
	require.NoError(t, err)

	_, err = m.AssociateSubnets(bg, "fw-ok", []nfdriver.SubnetMapping{{SubnetID: "subnet-a2"}}, "")
	require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)

	got, err := m.AssociateSubnets(bg, "fw-ok", []nfdriver.SubnetMapping{{SubnetID: "subnet-b"}}, "")
	require.NoError(t, err)
	assert.Equal(t, "us-east-1b", got.SubnetDetails["subnet-b"].AvailabilityZone)

	// The refused create left nothing behind, and the rejected associate kept the token.
	_, err = m.DescribeFirewall(bg, "fw-dup", "")
	assert.True(t, cerrors.IsNotFound(err))
	assert.NotEqual(t, fw.UpdateToken, got.UpdateToken)
}

func TestSubnets_UnknownSubnetFallsBackToIDZone(t *testing.T) {
	m := newMock()
	m.SetSubnetResolver(fakeSubnets{"subnet-a": "us-east-1a"})

	pol := seedPolicy(t, m, "pol-1")

	fw, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: "fw-mix", PolicyARN: pol.ARN, VPCID: "vpc-1a2b", SubnetIDs: []string{"subnet-a", "subnet-unknown"},
	})
	require.NoError(t, err)

	assert.Equal(t, "us-east-1a", fw.SubnetDetails["subnet-a"].AvailabilityZone)
	assert.NotEmpty(t, fw.SubnetDetails["subnet-unknown"].AvailabilityZone)
	assert.NotEqual(t, "us-east-1a", fw.SubnetDetails["subnet-unknown"].AvailabilityZone)
}

func TestZoneEndpoints_TransitGatewayZonesShareTheSubnetShape(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-1")

	fw, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: "fw-tgw", PolicyARN: pol.ARN, TransitGatewayID: "tgw-0abc", AvailabilityZones: []string{"us-east-1a", "us-east-1b"},
	})
	require.NoError(t, err)

	assert.Equal(t, "vpce-"+fw.ID+"-a", fw.ZoneEndpoints["us-east-1a"])
	assert.Equal(t, "vpce-"+fw.ID+"-b", fw.ZoneEndpoints["us-east-1b"])
}
