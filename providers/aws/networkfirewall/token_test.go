package networkfirewall

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const uuidPattern = `^[0-9a-f]{8}-([0-9a-f]{4}-){3}[0-9a-f]{12}$`

func TestToken_MintedAtCreate_UUID_StableAcrossReads(t *testing.T) {
	m := newMock()
	pol, fw := seedStack(t, m)
	rg := seedRuleGroup(t, m, "rg-1", "STATEFUL", 10)

	assert.Regexp(t, uuidPattern, fw.UpdateToken)
	assert.Regexp(t, uuidPattern, pol.UpdateToken)
	assert.Regexp(t, uuidPattern, rg.UpdateToken)

	for i := 0; i < 3; i++ {
		got, err := m.DescribeFirewall(bg, "fw-1", "")
		require.NoError(t, err)
		assert.Equal(t, fw.UpdateToken, got.UpdateToken)
	}

	assert.NotEqual(t, pol.UpdateToken, rg.UpdateToken)
}

func TestToken_RotatesOnEveryMutation(t *testing.T) {
	m := newMock()
	pol, fw := seedStack(t, m)

	other := seedPolicy(t, m, "pol-2")
	before := fw.UpdateToken

	updated, err := m.UpdateFirewallDeleteProtection(bg, "fw-1", true, "")
	require.NoError(t, err)
	assert.NotEqual(t, before, updated.UpdateToken)

	again, err := m.UpdateFirewallDeleteProtection(bg, "fw-1", false, updated.UpdateToken)
	require.NoError(t, err)
	assert.NotEqual(t, updated.UpdateToken, again.UpdateToken)

	// Other resources are untouched by a firewall mutation.
	gotPol, _ := m.DescribeFirewallPolicy(bg, "pol-1", "")
	assert.Equal(t, pol.UpdateToken, gotPol.UpdateToken)

	gotOther, _ := m.DescribeFirewallPolicy(bg, "pol-2", "")
	assert.Equal(t, other.UpdateToken, gotOther.UpdateToken)
}

func TestToken_StaleRejected_InvalidTokenException(t *testing.T) {
	m := newMock()
	pol, fw := seedStack(t, m)

	_, err := m.UpdateFirewallDeleteProtection(bg, "fw-1", true, "")
	require.NoError(t, err)

	// The token read at create time is now stale.
	_, err = m.UpdateFirewallDeleteProtection(bg, "fw-1", false, fw.UpdateToken)
	requireException(t, err, nfdriver.ExInvalidToken)

	got, _ := m.DescribeFirewall(bg, "fw-1", "")
	assert.True(t, got.DeleteProtection, "a rejected update changes nothing")

	_, err = m.UpdateFirewallPolicy(bg, "pol-1", "", nfdriver.UpdateFirewallPolicyConfig{
		UpdateToken: "00000000-0000-0000-0000-000000000000", StatelessDefaultActions: standardActions,
		StatelessFragmentDefaultActions: standardActions,
	})
	requireException(t, err, nfdriver.ExInvalidToken)

	gotPol, _ := m.DescribeFirewallPolicy(bg, "pol-1", "")
	assert.Equal(t, pol.UpdateToken, gotPol.UpdateToken)
}

func TestToken_RequiredOnPolicyAndRuleGroupUpdate(t *testing.T) {
	m := newMock()
	seedPolicy(t, m, "pol-1")
	seedRuleGroup(t, m, "rg-1", "STATEFUL", 10)

	_, err := m.UpdateFirewallPolicy(bg, "pol-1", "", nfdriver.UpdateFirewallPolicyConfig{
		StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions,
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "policy update without token: %v", err)

	_, err = m.UpdateRuleGroup(bg, "rg-1", "", "STATEFUL", nfdriver.UpdateRuleGroupConfig{Description: "d"})
	assert.True(t, cerrors.IsInvalidArgument(err), "rule group update without token: %v", err)
}

func TestToken_OptionalOnFirewallUpdates(t *testing.T) {
	m := newMock()
	_, fw := seedStack(t, m)

	_, err := m.UpdateFirewallDeleteProtection(bg, "fw-1", true, "")
	require.NoError(t, err, "omitted token = unconditional")

	cur, _ := m.DescribeFirewall(bg, "fw-1", "")

	_, err = m.UpdateFirewallDeleteProtection(bg, "fw-1", false, cur.UpdateToken)
	require.NoError(t, err, "matching token succeeds")

	_ = fw
}

func TestToken_TagOpsDoNotRotate(t *testing.T) {
	m := newMock()
	_, fw := seedStack(t, m)

	require.NoError(t, m.TagResource(bg, fw.ARN, map[string]string{"a": "b"}))

	got, _ := m.DescribeFirewall(bg, "fw-1", "")
	assert.Equal(t, fw.UpdateToken, got.UpdateToken)
}

func TestToken_Race_50Writers_ExactlyOneWins(t *testing.T) {
	m := newMock()
	_, fw := seedStack(t, m)

	var (
		wg      sync.WaitGroup
		wins    atomic.Int32
		invalid atomic.Int32
	)

	for i := 0; i < 50; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := m.UpdateFirewallDeleteProtection(bg, "fw-1", true, fw.UpdateToken)

			switch ex, _ := nfdriver.ExceptionOf(err); {
			case err == nil:
				wins.Add(1)
			case ex == nfdriver.ExInvalidToken:
				invalid.Add(1)
			}
		}()
	}

	wg.Wait()

	assert.EqualValues(t, 1, wins.Load())
	assert.EqualValues(t, 49, invalid.Load())
}

func TestChangeProtection_DefaultsFalse_EchoedOnDescribe(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-1")

	plain := seedFirewall(t, m, "fw-plain", pol.ARN)
	assert.False(t, plain.SubnetChangeProtection || plain.FirewallPolicyChangeProtection || plain.AvailabilityZoneChangeProtection)

	_, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: "fw-prot", PolicyARN: pol.ARN, VPCID: "vpc-1a", SubnetChangeProtection: true,
		FirewallPolicyChangeProtection: true, AvailabilityZoneChangeProtection: true,
	})
	require.NoError(t, err)

	got, _ := m.DescribeFirewall(bg, "fw-prot", "")
	assert.True(t, got.SubnetChangeProtection && got.FirewallPolicyChangeProtection && got.AvailabilityZoneChangeProtection)
}

func TestChangeProtection_Update_Toggles(t *testing.T) {
	m := newMock()
	seedStack(t, m)

	fw, err := m.UpdateSubnetChangeProtection(bg, "fw-1", true, "")
	require.NoError(t, err)
	assert.True(t, fw.SubnetChangeProtection)
	assert.False(t, fw.FirewallPolicyChangeProtection)
	assert.False(t, fw.AvailabilityZoneChangeProtection)

	fw, err = m.UpdateFirewallPolicyChangeProtection(bg, "fw-1", true, fw.UpdateToken)
	require.NoError(t, err)
	assert.True(t, fw.SubnetChangeProtection && fw.FirewallPolicyChangeProtection)

	fw, err = m.UpdateAvailabilityZoneChangeProtection(bg, "fw-1", true, "")
	require.NoError(t, err)
	assert.True(t, fw.AvailabilityZoneChangeProtection)

	fw, err = m.UpdateSubnetChangeProtection(bg, "fw-1", false, "")
	require.NoError(t, err)
	assert.False(t, fw.SubnetChangeProtection)
}

func TestChangeProtection_BlocksSubnetAssociateDisassociate(t *testing.T) {
	m := newMock()
	seedStack(t, m)

	_, err := m.UpdateSubnetChangeProtection(bg, "fw-1", true, "")
	require.NoError(t, err)

	_, err = m.AssociateSubnets(bg, "fw-1", []nfdriver.SubnetMapping{{SubnetID: "subnet-2"}}, "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_, err = m.DisassociateSubnets(bg, "fw-1", []string{"subnet-1"}, "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_, err = m.UpdateSubnetChangeProtection(bg, "fw-1", false, "")
	require.NoError(t, err)

	_, err = m.AssociateSubnets(bg, "fw-1", []nfdriver.SubnetMapping{{SubnetID: "subnet-2"}}, "")
	require.NoError(t, err)
}

func TestChangeProtection_BlocksPolicyAssociate(t *testing.T) {
	m := newMock()
	seedStack(t, m)
	other := seedPolicy(t, m, "pol-2")

	_, err := m.UpdateFirewallPolicyChangeProtection(bg, "fw-1", true, "")
	require.NoError(t, err)

	_, err = m.AssociateFirewallPolicy(bg, "fw-1", other.ARN, "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_, err = m.UpdateFirewallPolicyChangeProtection(bg, "fw-1", false, "")
	require.NoError(t, err)

	_, err = m.AssociateFirewallPolicy(bg, "fw-1", other.ARN, "")
	require.NoError(t, err)
}

func tgwFirewall(t *testing.T, m *Mock, name string, protect bool) *nfdriver.Firewall {
	t.Helper()

	pol := seedPolicy(t, m, name+"-pol")

	fw, err := m.CreateFirewall(bg, nfdriver.CreateFirewallConfig{
		Name: name, PolicyARN: pol.ARN, TransitGatewayID: "tgw-0abc", AvailabilityZones: []string{"us-east-1a"},
		AvailabilityZoneChangeProtection: protect,
	})
	require.NoError(t, err)

	return fw
}

func TestChangeProtection_BlocksAZAssociateDisassociate(t *testing.T) {
	m := newMock()
	tgwFirewall(t, m, "fw-tgw", true)

	_, err := m.AssociateAvailabilityZones(bg, "fw-tgw", []string{"us-east-1b"}, "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_, err = m.DisassociateAvailabilityZones(bg, "fw-tgw", []string{"us-east-1a"}, "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_, err = m.UpdateAvailabilityZoneChangeProtection(bg, "fw-tgw", false, "")
	require.NoError(t, err)

	_, err = m.AssociateAvailabilityZones(bg, "fw-tgw", []string{"us-east-1b"}, "")
	require.NoError(t, err)
}

func TestAvailabilityZones_AssociateDisassociate_TGWFirewall(t *testing.T) {
	m := newMock()
	fw := tgwFirewall(t, m, "fw-tgw", false)

	got, err := m.AssociateAvailabilityZones(bg, "fw-tgw", []string{"us-east-1b", "us-east-1a"}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"us-east-1a", "us-east-1b"}, got.AvailabilityZones, "deduplicated")
	assert.NotEqual(t, fw.UpdateToken, got.UpdateToken)

	got, err = m.DisassociateAvailabilityZones(bg, "fw-tgw", []string{"us-east-1a"}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"us-east-1b"}, got.AvailabilityZones)

	_, err = m.DisassociateAvailabilityZones(bg, "fw-tgw", []string{"us-east-1z"}, "")
	assert.True(t, cerrors.IsInvalidArgument(err), "unknown zone: %v", err)
}

func TestAvailabilityZones_OnVPCFirewall_Unsupported(t *testing.T) {
	m := newMock()
	tgw := tgwFirewall(t, m, "fw-tgw", false)
	_, vpc := seedStack(t, m)

	_, err := m.AssociateAvailabilityZones(bg, vpc.Name, []string{"us-east-1b"}, "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_, err = m.AssociateSubnets(bg, tgw.Name, []nfdriver.SubnetMapping{{SubnetID: "subnet-1"}}, "")
	requireException(t, err, nfdriver.ExInvalidOperation)
}

func TestChangeProtection_NotFoundFirewall(t *testing.T) {
	m := newMock()

	_, err := m.UpdateSubnetChangeProtection(bg, "ghost", true, "")
	assert.True(t, cerrors.IsNotFound(err), "%v", err)
}

func TestUpdateFirewallDescription_SetAndClear(t *testing.T) {
	m := newMock()
	_, fw := seedStack(t, m)

	got, err := m.UpdateFirewallDescription(bg, "fw-1", "edge firewall", "")
	require.NoError(t, err)
	assert.Equal(t, "edge firewall", got.Description)
	assert.NotEqual(t, fw.UpdateToken, got.UpdateToken)

	got, err = m.UpdateFirewallDescription(bg, "fw-1", "", got.UpdateToken)
	require.NoError(t, err)
	assert.Empty(t, got.Description, "an omitted description clears it")
}

func TestUpdateFirewallDescription_TooLong(t *testing.T) {
	m := newMock()
	seedStack(t, m)

	long := make([]byte, 513)
	for i := range long {
		long[i] = 'x'
	}

	_, err := m.UpdateFirewallDescription(bg, "fw-1", string(long), "")
	assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)
}
