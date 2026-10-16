package networkfirewall

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

// TestSnapshotRoundTripNetworkFirewall proves a snapshot/restore round-trip
// preserves every store, the logging and resource-policy maps and the update
// tokens under their original identities.
func TestSnapshotRoundTripNetworkFirewall(t *testing.T) {
	src := newMock()
	pol, fw := seedStack(t, src)

	_, err := src.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{Name: "rg-1", Type: "STATEFUL", Capacity: 100})
	require.NoError(t, err)

	_, err = src.CreateTLSInspectionConfiguration(bg, nfdriver.CreateTLSInspectionConfig{
		Name: "tls-1", Configuration: tlsConfigJSON("arn:aws:acm:us-east-1:000000000000:certificate/c1"),
	})
	require.NoError(t, err)

	assoc, err := src.CreateVpcEndpointAssociation(bg, nfdriver.CreateVPCEndpointAssociationConfig{
		FirewallARN: fw.ARN, VPCID: "vpc-2b", SubnetID: "subnet-9",
	})
	require.NoError(t, err)

	require.NoError(t, src.UpdateLoggingConfiguration(bg, "fw-1", &nfdriver.LoggingConfiguration{
		Destinations: []nfdriver.LogDestinationConfig{s3Dest("FLOW", "b")},
	}))
	require.NoError(t, src.PutResourcePolicy(bg, pol.ARN, policyDoc("network-firewall:AssociateFirewallPolicy")))

	flow, err := src.StartFlowFlush(bg, nfdriver.FlowOperationRequest{FirewallARN: fw.ARN, AvailabilityZone: "us-east-1a"})
	require.NoError(t, err)

	_, err = src.UpdateFirewallAnalysisSettings(bg, "fw-1", []string{"TLS_SNI"}, "")
	require.NoError(t, err)

	rep, err := src.StartAnalysisReport(bg, "fw-1", "", "TLS_SNI")
	require.NoError(t, err)

	raw, err := src.Snapshot(bg, true)
	require.NoError(t, err)

	dst := newMock()
	require.NoError(t, dst.Restore(bg, raw))

	gotFw, err := dst.DescribeFirewall(bg, "fw-1", "")
	require.NoError(t, err)
	assert.Equal(t, pol.ARN, gotFw.PolicyARN)
	assert.Equal(t, 1, gotFw.NumberOfAssociations)

	srcFw, _ := src.DescribeFirewall(bg, "fw-1", "")
	assert.Equal(t, srcFw.UpdateToken, gotFw.UpdateToken, "tokens survive a restore")

	gotRg, err := dst.DescribeRuleGroup(bg, "rg-1", "", "STATEFUL")
	require.NoError(t, err)
	assert.Equal(t, 100, gotRg.Capacity)

	_, err = dst.DescribeTLSInspectionConfiguration(bg, "tls-1", "")
	require.NoError(t, err)

	_, err = dst.DescribeVpcEndpointAssociation(bg, assoc.ARN)
	require.NoError(t, err)

	logs, err := dst.DescribeLoggingConfiguration(bg, "fw-1")
	require.NoError(t, err)
	require.Len(t, logs.Destinations, 1)
	assert.Equal(t, "FLOW", logs.Destinations[0].LogType)

	policy, err := dst.DescribeResourcePolicy(bg, pol.ARN)
	require.NoError(t, err)
	assert.Contains(t, policy, "AssociateFirewallPolicy")

	_, err = dst.DescribeFlowOperation(bg, nfdriver.FlowOperationScope{FirewallARN: fw.ARN, AvailabilityZone: "us-east-1a"}, flow.ID)
	require.NoError(t, err)

	_, err = dst.GetAnalysisReport(bg, "fw-1", "", rep.ID)
	require.NoError(t, err)
}

// TestRestoreLegacySnapshot loads state written before update tokens, status
// fields and the logging-configuration object existed.
func TestRestoreLegacySnapshot(t *testing.T) {
	legacy := map[string]any{
		"firewalls": json.RawMessage(`{"fw-old":{"Name":"fw-old","ARN":"arn:aws:network-firewall:us-east-1:000000000000:firewall/fw-old","VPCID":"vpc-1","Status":"READY"}}`),
		"policies":  json.RawMessage(`{"pol-old":{"Name":"pol-old","ARN":"arn:aws:network-firewall:us-east-1:000000000000:firewall-policy/pol-old"}}`),
		"logging":   json.RawMessage(`{"fw-old":[{"LogType":"FLOW","LogDestinationType":"S3","LogDestination":{"bucketName":"b"}}]}`),
	}
	raw, err := json.Marshal(legacy)
	require.NoError(t, err)

	m := newMock()
	require.NoError(t, m.Restore(bg, raw))

	fw, err := m.DescribeFirewall(bg, "fw-old", "")
	require.NoError(t, err)
	assert.Regexp(t, `^[0-9a-f]{8}-([0-9a-f]{4}-){3}[0-9a-f]{12}$`, fw.UpdateToken)
	assert.False(t, fw.SubnetChangeProtection)

	pol, err := m.DescribeFirewallPolicy(bg, "pol-old", "")
	require.NoError(t, err)
	assert.NotEmpty(t, pol.UpdateToken)
	assert.Equal(t, "ACTIVE", pol.Status)

	logs, err := m.DescribeLoggingConfiguration(bg, "fw-old")
	require.NoError(t, err)
	require.Len(t, logs.Destinations, 1)
	assert.Equal(t, "S3", logs.Destinations[0].LogDestinationType)
}
