package networkfirewall

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

func tlsConfigJSON(certARNs ...string) json.RawMessage {
	certs := make([]map[string]string, 0, len(certARNs))
	for _, a := range certARNs {
		certs = append(certs, map[string]string{"ResourceArn": a})
	}

	b, _ := json.Marshal(map[string]any{"ServerCertificateConfigurations": []map[string]any{{"ServerCertificates": certs}}})

	return b
}

func policyDoc(actions ...string) string {
	b, _ := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect": "Allow", "Principal": map[string]string{"AWS": "arn:aws:iam::111122223333:root"},
			"Action": actions, "Resource": "*",
		}},
	})

	return string(b)
}

func TestResourcePolicy_PutDescribeDelete_AllTypes(t *testing.T) {
	m := newMock()
	pol, fw := seedStack(t, m)
	rg := seedRuleGroup(t, m, "rg-1", "STATEFUL", 5)
	sl := seedRuleGroup(t, m, "rg-sl", "STATELESS", 5)

	cases := map[string]struct{ arn, action string }{
		"rule group":           {rg.ARN, "network-firewall:CreateFirewallPolicy"},
		"stateless rule group": {sl.ARN, "network-firewall:ListRuleGroups"},
		"firewall policy":      {pol.ARN, "network-firewall:AssociateFirewallPolicy"},
		"firewall":             {fw.ARN, "network-firewall:ListFirewalls"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			doc := policyDoc(c.action)
			require.NoError(t, m.PutResourcePolicy(bg, c.arn, doc))

			got, err := m.DescribeResourcePolicy(bg, c.arn)
			require.NoError(t, err)
			assert.JSONEq(t, doc, got)

			require.NoError(t, m.DeleteResourcePolicy(bg, c.arn))

			_, err = m.DescribeResourcePolicy(bg, c.arn)
			assert.True(t, cerrors.IsNotFound(err), "after delete: %v", err)
		})
	}
}

func TestResourcePolicy_Validation(t *testing.T) {
	m := newMock()
	pol, fw := seedStack(t, m)

	err := m.PutResourcePolicy(bg, pol.ARN, "{not json")
	requireException(t, err, nfdriver.ExInvalidResourcePolicy)

	err = m.PutResourcePolicy(bg, pol.ARN, policyDoc("network-firewall:ListFirewalls"))
	requireException(t, err, nfdriver.ExInvalidResourcePolicy)

	err = m.PutResourcePolicy(bg, pol.ARN, `{"Version":"2012-10-17","Statement":[]}`)
	requireException(t, err, nfdriver.ExInvalidResourcePolicy)

	err = m.PutResourcePolicy(bg, fw.ARN, `{"Statement":[{"Effect":"Allow"}]}`)
	requireException(t, err, nfdriver.ExInvalidResourcePolicy)

	err = m.PutResourcePolicy(bg, "arn:aws:network-firewall:us-east-1:000000000000:firewall/ghost", policyDoc("network-firewall:ListFirewalls"))
	assert.True(t, cerrors.IsNotFound(err), "%v", err)

	err = m.PutResourcePolicy(bg, "not-an-arn", policyDoc("network-firewall:ListFirewalls"))
	assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)

	err = m.PutResourcePolicy(bg, fw.ARN, "")
	assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)

	err = m.PutResourcePolicy(bg, fw.ARN, strings.Repeat("x", 395001))
	assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)

	tls, tlsErr := m.CreateTLSInspectionConfiguration(bg, nfdriver.CreateTLSInspectionConfig{Name: "t", Configuration: tlsConfigJSON("c")})
	require.NoError(t, tlsErr)

	err = m.PutResourcePolicy(bg, tls.ARN, policyDoc("network-firewall:ListFirewalls"))
	assert.True(t, cerrors.IsInvalidArgument(err), "a TLS configuration cannot be shared: %v", err)
}

func TestResourcePolicy_RemovedWithResource(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-1")

	require.NoError(t, m.PutResourcePolicy(bg, pol.ARN, policyDoc("network-firewall:AssociateFirewallPolicy")))
	_, err := m.DeleteFirewallPolicy(bg, "pol-1", "")
	require.NoError(t, err)

	assert.Empty(t, m.resourcePolicies, "no orphaned policy after the resource is deleted")
}

func TestTLS_CRUD_List_Tagging(t *testing.T) {
	m, clock := newClockMock(t)

	created, err := m.CreateTLSInspectionConfiguration(bg, nfdriver.CreateTLSInspectionConfig{
		Name: "tls-1", Description: "d", Configuration: tlsConfigJSON("arn:aws:acm:us-east-1:000000000000:certificate/c1"),
		Tags: map[string]string{"a": "b"},
	})
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", created.Status)
	assert.Equal(t, []string{"arn:aws:acm:us-east-1:000000000000:certificate/c1"}, created.CertificateARNs)
	assert.Regexp(t, uuidPattern, created.UpdateToken)

	clock.Advance(time.Minute)

	updated, err := m.UpdateTLSInspectionConfiguration(bg, "tls-1", "", nfdriver.UpdateTLSInspectionConfig{
		UpdateToken: created.UpdateToken, Description: "d2", Configuration: tlsConfigJSON("c2", "c3"),
	})
	require.NoError(t, err)
	assert.NotEqual(t, created.UpdateToken, updated.UpdateToken)
	assert.True(t, updated.LastModified.After(created.LastModified))
	assert.Len(t, updated.CertificateARNs, 2)

	byARN, err := m.DescribeTLSInspectionConfiguration(bg, "", created.ARN)
	require.NoError(t, err)
	assert.Equal(t, "d2", byARN.Description)

	require.NoError(t, m.TagResource(bg, created.ARN, map[string]string{"c": "d"}))

	tags, err := m.ListTagsForResource(bg, created.ARN)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a": "b", "c": "d"}, tags)

	items, _, err := m.ListTLSInspectionConfigurations(bg, nfdriver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, items, 1)

	deleted, err := m.DeleteTLSInspectionConfiguration(bg, "tls-1", "")
	require.NoError(t, err)
	assert.Equal(t, "DELETING", deleted.Status)

	_, err = m.DescribeTLSInspectionConfiguration(bg, "tls-1", "")
	assert.True(t, cerrors.IsNotFound(err))
}

func TestTLS_Validation(t *testing.T) {
	m := newMock()

	cases := map[string]nfdriver.CreateTLSInspectionConfig{
		"no configuration":    {Name: "t"},
		"empty configuration": {Name: "t", Configuration: []byte(`{"ServerCertificateConfigurations":[]}`)},
		"malformed":           {Name: "t", Configuration: []byte(`{`)},
		"bad name":            {Name: "t_1", Configuration: tlsConfigJSON("c")},
		"long description":    {Name: "t", Description: strings.Repeat("d", 513), Configuration: tlsConfigJSON("c")},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := m.CreateTLSInspectionConfiguration(bg, cfg)
			assert.True(t, cerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
		})
	}

	created, err := m.CreateTLSInspectionConfiguration(bg, nfdriver.CreateTLSInspectionConfig{Name: "t", Configuration: tlsConfigJSON("c")})
	require.NoError(t, err)

	_, err = m.CreateTLSInspectionConfiguration(bg, nfdriver.CreateTLSInspectionConfig{Name: "t", Configuration: tlsConfigJSON("c")})
	assert.True(t, cerrors.IsAlreadyExists(err))

	_, err = m.UpdateTLSInspectionConfiguration(bg, "t", "", nfdriver.UpdateTLSInspectionConfig{Configuration: tlsConfigJSON("c")})
	assert.True(t, cerrors.IsInvalidArgument(err), "update needs a token: %v", err)

	_, err = m.UpdateTLSInspectionConfiguration(bg, "t", "", nfdriver.UpdateTLSInspectionConfig{
		UpdateToken: "11111111-1111-1111-1111-111111111111", Configuration: tlsConfigJSON("c"),
	})
	requireException(t, err, nfdriver.ExInvalidToken)

	_ = created
}

func TestTLS_InUseByPolicy_BlockedDelete(t *testing.T) {
	m := newMock()

	tlsCfg, err := m.CreateTLSInspectionConfiguration(bg, nfdriver.CreateTLSInspectionConfig{Name: "t", Configuration: tlsConfigJSON("c")})
	require.NoError(t, err)

	pol, err := m.CreateFirewallPolicy(bg, nfdriver.CreateFirewallPolicyConfig{
		Name: "p", StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions,
		TLSInspectionConfigurationARN: tlsCfg.ARN,
	})
	require.NoError(t, err)

	got, _ := m.DescribeTLSInspectionConfiguration(bg, "t", "")
	assert.Equal(t, 1, got.NumberOfAssociations)

	_, err = m.DeleteTLSInspectionConfiguration(bg, "t", "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_, err = m.DeleteFirewallPolicy(bg, pol.Name, "")
	require.NoError(t, err)

	_, err = m.DeleteTLSInspectionConfiguration(bg, "t", "")
	require.NoError(t, err)
}

func TestTLS_NoPrivateMaterialInResponse(t *testing.T) {
	m := newMock()

	cfg := []byte(`{"ServerCertificateConfigurations":[{"CertificateAuthorityArn":"arn:aws:acm-pca:us-east-1:000000000000:certificate-authority/ca",` +
		`"ServerCertificates":[{"ResourceArn":"arn:aws:acm:us-east-1:000000000000:certificate/c1"}]}]}`)

	got, err := m.CreateTLSInspectionConfiguration(bg, nfdriver.CreateTLSInspectionConfig{Name: "t", Configuration: cfg})
	require.NoError(t, err)

	// Only ARNs are stored and echoed; there is no key or certificate body to leak.
	assert.NotContains(t, string(got.Configuration), "PRIVATE KEY")
	assert.Equal(t, []string{"arn:aws:acm:us-east-1:000000000000:certificate/c1"}, got.CertificateARNs)
}

func vpcAssociation(t *testing.T, m *Mock, firewallARN, subnet string) *nfdriver.VPCEndpointAssociation {
	t.Helper()

	a, err := m.CreateVpcEndpointAssociation(bg, nfdriver.CreateVPCEndpointAssociationConfig{
		FirewallARN: firewallARN, VPCID: "vpc-2b", SubnetID: subnet, Description: "extra",
	})
	require.NoError(t, err)

	return a
}

func TestVPCEndpointAssociation_CRUD_List_FilterByFirewall(t *testing.T) {
	m := newMock()
	pol, fw := seedStack(t, m)
	other := seedFirewall(t, m, "fw-2", pol.ARN)

	a1 := vpcAssociation(t, m, fw.ARN, "subnet-a")
	vpcAssociation(t, m, other.ARN, "subnet-b")

	assert.Equal(t, "READY", a1.Status)
	assert.NotEmpty(t, a1.AvailabilityZone)

	got, err := m.DescribeVpcEndpointAssociation(bg, a1.ARN)
	require.NoError(t, err)
	assert.Equal(t, a1.ID, got.ID)

	again, _ := m.DescribeVpcEndpointAssociation(bg, a1.ARN)
	assert.Equal(t, got.AvailabilityZone, again.AvailabilityZone, "stable across reads")

	all, _, err := m.ListVpcEndpointAssociations(bg, "", nfdriver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all, 2)

	mine, _, err := m.ListVpcEndpointAssociations(bg, fw.ARN, nfdriver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, a1.ARN, mine[0].ARN)

	fwNow, _ := m.DescribeFirewall(bg, "fw-1", "")
	assert.Equal(t, 1, fwNow.NumberOfAssociations)

	require.NoError(t, m.TagResource(bg, a1.ARN, map[string]string{"k": "v"}))

	deleted, err := m.DeleteVpcEndpointAssociation(bg, a1.ARN)
	require.NoError(t, err)
	assert.Equal(t, "DELETING", deleted.Status)

	_, err = m.DescribeVpcEndpointAssociation(bg, a1.ARN)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestVPCEndpointAssociation_Validation(t *testing.T) {
	m := newMock()
	_, fw := seedStack(t, m)

	cases := map[string]nfdriver.CreateVPCEndpointAssociationConfig{
		"bad vpc":          {FirewallARN: fw.ARN, VPCID: "nope", SubnetID: "s"},
		"no subnet":        {FirewallARN: fw.ARN, VPCID: "vpc-2b"},
		"bad ip type":      {FirewallARN: fw.ARN, VPCID: "vpc-2b", SubnetID: "s", IPAddressType: "IPV5"},
		"malformed arn":    {FirewallARN: "x", VPCID: "vpc-2b", SubnetID: "s"},
		"long description": {FirewallARN: fw.ARN, VPCID: "vpc-2b", SubnetID: "s", Description: strings.Repeat("d", 513)},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := m.CreateVpcEndpointAssociation(bg, cfg)
			assert.True(t, cerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
		})
	}

	_, err := m.CreateVpcEndpointAssociation(bg, nfdriver.CreateVPCEndpointAssociationConfig{
		FirewallARN: "arn:aws:network-firewall:us-east-1:000000000000:firewall/ghost", VPCID: "vpc-2b", SubnetID: "s",
	})
	assert.True(t, cerrors.IsNotFound(err), "%v", err)
}

func TestDeleteFirewall_WithAssociations_Blocked(t *testing.T) {
	m := newMock()
	_, fw := seedStack(t, m)

	a := vpcAssociation(t, m, fw.ARN, "subnet-a")

	_, err := m.DeleteFirewall(bg, "fw-1", "")
	requireException(t, err, nfdriver.ExInvalidOperation)

	_, err = m.DeleteVpcEndpointAssociation(bg, a.ARN)
	require.NoError(t, err)

	_, err = m.DeleteFirewall(bg, "fw-1", "")
	require.NoError(t, err)
}

func TestMetadata_Firewall_RuleGroup(t *testing.T) {
	m, _ := newClockMock(t)
	pol, fw := seedStack(t, m)
	rg := seedRuleGroup(t, m, "rg-1", "STATEFUL", 7)

	meta, err := m.DescribeFirewallMetadata(bg, fw.ARN)
	require.NoError(t, err)
	assert.Equal(t, fw.ARN, meta.FirewallARN)
	assert.Equal(t, pol.ARN, meta.PolicyARN)
	assert.Equal(t, "READY", meta.Status)
	assert.Equal(t, []string{seedZone}, meta.AvailabilityZones)

	_, err = m.DescribeFirewallMetadata(bg, "arn:aws:network-firewall:us-east-1:000000000000:firewall/ghost")
	assert.True(t, cerrors.IsNotFound(err))

	got, err := m.DescribeRuleGroupMetadata(bg, "", rg.ARN, "")
	require.NoError(t, err)
	assert.Equal(t, 7, got.Capacity)
}

func TestRuleGroupSummary_ParsesMsgSIDMetadata(t *testing.T) {
	m := newMock()

	_, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{
		Name: "rg", Type: "STATEFUL", Capacity: 10, SummaryRuleOptions: []string{"MSG", "SID", "METADATA"},
		Rules: rulesString(
			`# a comment`,
			`alert tcp any any -> any 80 (msg:"web hit"; metadata:author tester; sid:100;)`,
			`drop udp any any -> any 53 (msg:"dns"; sid:101;)`),
	})
	require.NoError(t, err)

	_, sums, err := m.DescribeRuleGroupSummary(bg, "rg", "", "STATEFUL")
	require.NoError(t, err)
	require.Len(t, sums, 2)
	assert.Equal(t, nfdriver.RuleSummary{Msg: "web hit", SID: "100", Metadata: "author tester"}, sums[0])
	assert.Equal(t, nfdriver.RuleSummary{Msg: "dns", SID: "101"}, sums[1])

	// Only the options named in SummaryConfiguration are populated.
	_, err = m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{
		Name: "rg2", Type: "STATEFUL", Capacity: 10, SummaryRuleOptions: []string{"SID"},
		Rules: rulesString(`alert tcp any any -> any 80 (msg:"web hit"; sid:7;)`),
	})
	require.NoError(t, err)

	_, sums, err = m.DescribeRuleGroupSummary(bg, "rg2", "", "STATEFUL")
	require.NoError(t, err)
	assert.Equal(t, []nfdriver.RuleSummary{{SID: "7"}}, sums)
}

func TestRuleGroupSummary_Stateless_InvalidRequest(t *testing.T) {
	m := newMock()
	seedRuleGroup(t, m, "sl", "STATELESS", 3)

	_, _, err := m.DescribeRuleGroupSummary(bg, "sl", "", "STATELESS")
	assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)
}

func TestFlowOperations_StartDescribeListResults(t *testing.T) {
	m, clock := newClockMock(t)
	_, fw := seedStack(t, m)
	scope := nfdriver.FlowOperationScope{FirewallARN: fw.ARN, AvailabilityZone: seedZone}

	capture, err := m.StartFlowCapture(bg, nfdriver.FlowOperationRequest{FirewallARN: fw.ARN, AvailabilityZone: seedZone})
	require.NoError(t, err)
	assert.Regexp(t, uuidPattern, capture.ID)
	assert.Equal(t, "COMPLETED", capture.Status)
	assert.Equal(t, "FLOW_CAPTURE", capture.Type)

	clock.Advance(time.Second)

	flush, err := m.StartFlowFlush(bg, nfdriver.FlowOperationRequest{FirewallARN: fw.ARN, AvailabilityZone: seedZone})
	require.NoError(t, err)
	assert.Equal(t, "FLOW_FLUSH", flush.Type)

	got, err := m.DescribeFlowOperation(bg, scope, capture.ID)
	require.NoError(t, err)
	assert.Equal(t, capture.RequestedAt, got.RequestedAt, "timestamp is stable")

	all, _, err := m.ListFlowOperations(bg, scope, "", nfdriver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, capture.ID, all[0].ID, "oldest first")

	onlyFlush, _, err := m.ListFlowOperations(bg, scope, "FLOW_FLUSH", nfdriver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, onlyFlush, 1)
	assert.Equal(t, flush.ID, onlyFlush[0].ID)

	op, next, err := m.ListFlowOperationResults(bg, scope, capture.ID, nfdriver.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, capture.ID, op.ID)
	assert.Empty(t, next, "no data plane, so no flows")
}

func TestFlowOperations_Validation(t *testing.T) {
	m := newMock()
	pol, fw := seedStack(t, m)
	assoc := vpcAssociation(t, m, fw.ARN, "subnet-a")
	other := seedFirewall(t, m, "fw-2", pol.ARN)

	_, err := m.StartFlowCapture(bg, nfdriver.FlowOperationRequest{FirewallARN: "arn:aws:network-firewall:us-east-1:000000000000:firewall/ghost", AvailabilityZone: seedZone})
	assert.True(t, cerrors.IsNotFound(err), "%v", err)

	_, err = m.StartFlowCapture(bg, nfdriver.FlowOperationRequest{FirewallARN: fw.ARN})
	assert.True(t, cerrors.IsInvalidArgument(err), "no endpoint selector: %v", err)

	_, err = m.StartFlowCapture(bg, nfdriver.FlowOperationRequest{FirewallARN: fw.ARN, AvailabilityZone: "eu-west-1a"})
	assert.True(t, cerrors.IsInvalidArgument(err), "zone without an endpoint: %v", err)

	_, err = m.StartFlowCapture(bg, nfdriver.FlowOperationRequest{FirewallARN: other.ARN, VPCEndpointAssociationARN: assoc.ARN})
	assert.True(t, cerrors.IsInvalidArgument(err), "association of another firewall: %v", err)

	neg := int32(-1)
	_, err = m.StartFlowFlush(bg, nfdriver.FlowOperationRequest{FirewallARN: fw.ARN, AvailabilityZone: seedZone, MinimumFlowAgeSeconds: &neg})
	assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)

	_, _, err = m.ListFlowOperations(bg, nfdriver.FlowOperationScope{FirewallARN: fw.ARN, AvailabilityZone: seedZone}, "BOGUS", nfdriver.ListOptions{})
	assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)

	_, err = m.DescribeFlowOperation(bg, nfdriver.FlowOperationScope{FirewallARN: fw.ARN, AvailabilityZone: seedZone}, "no-such-op")
	assert.True(t, cerrors.IsNotFound(err), "%v", err)

	ok, err := m.StartFlowCapture(bg, nfdriver.FlowOperationRequest{FirewallARN: fw.ARN, VPCEndpointAssociationARN: assoc.ARN})
	require.NoError(t, err)

	_, err = m.DescribeFlowOperation(bg, nfdriver.FlowOperationScope{FirewallARN: fw.ARN, AvailabilityZone: seedZone}, ok.ID)
	assert.True(t, cerrors.IsNotFound(err), "an operation of another endpoint is not visible: %v", err)
}

func TestFlowOperations_AsyncSettle(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(clock), config.WithAsyncSettle()))
	_, fw := seedStack(t, m)

	op, err := m.StartFlowFlush(bg, nfdriver.FlowOperationRequest{FirewallARN: fw.ARN, AvailabilityZone: seedZone})
	require.NoError(t, err)
	assert.Equal(t, "IN_PROGRESS", op.Status)

	clock.Advance(10 * time.Second)

	got, err := m.DescribeFlowOperation(bg, nfdriver.FlowOperationScope{FirewallARN: fw.ARN, AvailabilityZone: seedZone}, op.ID)
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", got.Status)
}

func TestAnalysisReport_Start_List_Get(t *testing.T) {
	m, clock := newClockMock(t)
	_, fw := seedStack(t, m)

	_, err := m.UpdateFirewallAnalysisSettings(bg, "fw-1", []string{"TLS_SNI", "HTTP_HOST"}, "")
	require.NoError(t, err)

	r1, err := m.StartAnalysisReport(bg, "fw-1", "", "TLS_SNI")
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", r1.Status)

	clock.Advance(time.Minute)

	r2, err := m.StartAnalysisReport(bg, "", fw.ARN, "HTTP_HOST")
	require.NoError(t, err)

	list, _, err := m.ListAnalysisReports(bg, "fw-1", "", nfdriver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, r1.ID, list[0].ID)

	got, err := m.GetAnalysisReport(bg, "fw-1", "", r2.ID)
	require.NoError(t, err)
	assert.Equal(t, "HTTP_HOST", got.AnalysisType)

	_, err = m.GetAnalysisReport(bg, "fw-1", "", "ghost")
	assert.True(t, cerrors.IsNotFound(err))
}

func TestAnalysisReport_TypeNotEnabled(t *testing.T) {
	m := newMock()
	seedStack(t, m)

	_, err := m.StartAnalysisReport(bg, "fw-1", "", "TLS_SNI")
	assert.True(t, cerrors.IsInvalidArgument(err), "analysis not enabled: %v", err)

	_, err = m.UpdateFirewallAnalysisSettings(bg, "fw-1", []string{"NOPE"}, "")
	assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)

	_, err = m.StartAnalysisReport(bg, "fw-1", "", "BOGUS")
	assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)
}

func TestFlowAndReportOps_UnknownFirewall(t *testing.T) {
	m := newMock()

	_, err := m.StartAnalysisReport(bg, "ghost", "", "TLS_SNI")
	assert.True(t, cerrors.IsNotFound(err), "%v", err)

	_, _, err = m.ListAnalysisReports(bg, "ghost", "", nfdriver.ListOptions{})
	assert.True(t, cerrors.IsNotFound(err), "%v", err)

	_, err = m.StartFlowFlush(bg, nfdriver.FlowOperationRequest{
		FirewallARN: "arn:aws:network-firewall:us-east-1:000000000000:firewall/ghost", AvailabilityZone: "a",
	})
	assert.True(t, cerrors.IsNotFound(err), "%v", err)
}
