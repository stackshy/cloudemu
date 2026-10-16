package networkfirewall_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	nftypes "github.com/aws/aws-sdk-go-v2/service/networkfirewall/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var bg = context.Background()

// stack is a policy plus a firewall using it, created through the SDK.
type stack struct {
	c          *networkfirewall.Client
	policyARN  string
	firewallID string
	fwARN      string
}

func newStack(t *testing.T) *stack {
	t.Helper()

	c := newClient(t)

	pol, err := c.CreateFirewallPolicy(bg, &networkfirewall.CreateFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-1"),
		FirewallPolicy: &nftypes.FirewallPolicy{
			StatelessDefaultActions: []string{"aws:forward_to_sfe"}, StatelessFragmentDefaultActions: []string{"aws:forward_to_sfe"},
		},
	})
	require.NoError(t, err)

	fw, err := c.CreateFirewall(bg, &networkfirewall.CreateFirewallInput{
		FirewallName: aws.String("fw-1"), FirewallPolicyArn: pol.FirewallPolicyResponse.FirewallPolicyArn,
		VpcId: aws.String("vpc-1a"), SubnetMappings: []nftypes.SubnetMapping{{SubnetId: aws.String("subnet-1")}},
	})
	require.NoError(t, err)

	return &stack{c: c, policyARN: aws.ToString(pol.FirewallPolicyResponse.FirewallPolicyArn), fwARN: aws.ToString(fw.Firewall.FirewallArn)}
}

func (s *stack) token(t *testing.T) *string {
	t.Helper()

	d, err := s.c.DescribeFirewall(bg, &networkfirewall.DescribeFirewallInput{FirewallName: aws.String("fw-1")})
	require.NoError(t, err)

	return d.UpdateToken
}

func TestSDK_UpdateToken_Flow(t *testing.T) {
	s := newStack(t)
	stale := s.token(t)

	first, err := s.c.UpdateFirewallDeleteProtection(bg, &networkfirewall.UpdateFirewallDeleteProtectionInput{
		FirewallName: aws.String("fw-1"), DeleteProtection: true, UpdateToken: stale,
	})
	require.NoError(t, err)
	assert.NotEqual(t, aws.ToString(stale), aws.ToString(first.UpdateToken), "the token rotates on every mutation")

	_, err = s.c.UpdateFirewallDeleteProtection(bg, &networkfirewall.UpdateFirewallDeleteProtectionInput{
		FirewallName: aws.String("fw-1"), DeleteProtection: false, UpdateToken: stale,
	})

	var invalidToken *nftypes.InvalidTokenException
	require.ErrorAs(t, err, &invalidToken)

	_, err = s.c.UpdateFirewallDeleteProtection(bg, &networkfirewall.UpdateFirewallDeleteProtectionInput{
		FirewallName: aws.String("fw-1"), DeleteProtection: false,
	})
	require.NoError(t, err, "an update without a token is unconditional")

	// Policies and rule groups need a current token too.
	_, err = s.c.UpdateFirewallPolicy(bg, &networkfirewall.UpdateFirewallPolicyInput{
		FirewallPolicyArn: aws.String(s.policyARN), UpdateToken: aws.String("11111111-1111-1111-1111-111111111111"),
		FirewallPolicy: &nftypes.FirewallPolicy{
			StatelessDefaultActions: []string{"aws:drop"}, StatelessFragmentDefaultActions: []string{"aws:drop"},
		},
	})
	require.ErrorAs(t, err, &invalidToken)
}

func TestSDK_ChangeProtection_AllOps(t *testing.T) {
	s := newStack(t)

	sub, err := s.c.UpdateSubnetChangeProtection(bg, &networkfirewall.UpdateSubnetChangeProtectionInput{
		FirewallName: aws.String("fw-1"), SubnetChangeProtection: true,
	})
	require.NoError(t, err)
	assert.True(t, sub.SubnetChangeProtection)

	pol, err := s.c.UpdateFirewallPolicyChangeProtection(bg, &networkfirewall.UpdateFirewallPolicyChangeProtectionInput{
		FirewallArn: aws.String(s.fwARN), FirewallPolicyChangeProtection: true, UpdateToken: sub.UpdateToken,
	})
	require.NoError(t, err)
	assert.True(t, pol.FirewallPolicyChangeProtection)

	az, err := s.c.UpdateAvailabilityZoneChangeProtection(bg, &networkfirewall.UpdateAvailabilityZoneChangeProtectionInput{
		FirewallName: aws.String("fw-1"), AvailabilityZoneChangeProtection: true,
	})
	require.NoError(t, err)
	assert.True(t, az.AvailabilityZoneChangeProtection)

	d, err := s.c.DescribeFirewall(bg, &networkfirewall.DescribeFirewallInput{FirewallName: aws.String("fw-1")})
	require.NoError(t, err)
	assert.True(t, d.Firewall.SubnetChangeProtection && d.Firewall.FirewallPolicyChangeProtection && d.Firewall.AvailabilityZoneChangeProtection)

	var invalidOp *nftypes.InvalidOperationException

	_, err = s.c.AssociateSubnets(bg, &networkfirewall.AssociateSubnetsInput{
		FirewallName: aws.String("fw-1"), SubnetMappings: []nftypes.SubnetMapping{{SubnetId: aws.String("subnet-2")}},
	})
	require.ErrorAs(t, err, &invalidOp)

	_, err = s.c.DisassociateSubnets(bg, &networkfirewall.DisassociateSubnetsInput{FirewallName: aws.String("fw-1"), SubnetIds: []string{"subnet-1"}})
	require.ErrorAs(t, err, &invalidOp)

	other, err := s.c.CreateFirewallPolicy(bg, &networkfirewall.CreateFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-2"),
		FirewallPolicy: &nftypes.FirewallPolicy{
			StatelessDefaultActions: []string{"aws:pass"}, StatelessFragmentDefaultActions: []string{"aws:pass"},
		},
	})
	require.NoError(t, err)

	_, err = s.c.AssociateFirewallPolicy(bg, &networkfirewall.AssociateFirewallPolicyInput{
		FirewallName: aws.String("fw-1"), FirewallPolicyArn: other.FirewallPolicyResponse.FirewallPolicyArn,
	})
	require.ErrorAs(t, err, &invalidOp)

	// Availability Zone operations only apply to transit gateway-attached firewalls.
	_, err = s.c.AssociateAvailabilityZones(bg, &networkfirewall.AssociateAvailabilityZonesInput{
		FirewallName: aws.String("fw-1"), AvailabilityZoneMappings: []nftypes.AvailabilityZoneMapping{{AvailabilityZone: aws.String("us-east-1b")}},
	})
	require.ErrorAs(t, err, &invalidOp)

	tgw, err := s.c.CreateFirewall(bg, &networkfirewall.CreateFirewallInput{
		FirewallName: aws.String("fw-tgw"), FirewallPolicyArn: aws.String(s.policyARN), TransitGatewayId: aws.String("tgw-0abc"),
		AvailabilityZoneMappings: []nftypes.AvailabilityZoneMapping{{AvailabilityZone: aws.String("us-east-1a")}},
	})
	require.NoError(t, err)
	assert.Equal(t, "tgw-0abc", aws.ToString(tgw.Firewall.TransitGatewayId))

	added, err := s.c.AssociateAvailabilityZones(bg, &networkfirewall.AssociateAvailabilityZonesInput{
		FirewallName: aws.String("fw-tgw"), AvailabilityZoneMappings: []nftypes.AvailabilityZoneMapping{{AvailabilityZone: aws.String("us-east-1b")}},
	})
	require.NoError(t, err)
	assert.Len(t, added.AvailabilityZoneMappings, 2)

	removed, err := s.c.DisassociateAvailabilityZones(bg, &networkfirewall.DisassociateAvailabilityZonesInput{
		FirewallName: aws.String("fw-tgw"), AvailabilityZoneMappings: []nftypes.AvailabilityZoneMapping{{AvailabilityZone: aws.String("us-east-1a")}},
	})
	require.NoError(t, err)
	require.Len(t, removed.AvailabilityZoneMappings, 1)
	assert.Equal(t, "us-east-1b", aws.ToString(removed.AvailabilityZoneMappings[0].AvailabilityZone))
}

func TestSDK_UpdateFirewallDescription(t *testing.T) {
	s := newStack(t)

	set, err := s.c.UpdateFirewallDescription(bg, &networkfirewall.UpdateFirewallDescriptionInput{
		FirewallName: aws.String("fw-1"), Description: aws.String("edge"), UpdateToken: s.token(t),
	})
	require.NoError(t, err)
	assert.Equal(t, "edge", aws.ToString(set.Description))
	assert.Equal(t, s.fwARN, aws.ToString(set.FirewallArn))
	assert.Equal(t, "fw-1", aws.ToString(set.FirewallName))

	cleared, err := s.c.UpdateFirewallDescription(bg, &networkfirewall.UpdateFirewallDescriptionInput{FirewallArn: aws.String(s.fwARN)})
	require.NoError(t, err)
	assert.Empty(t, aws.ToString(cleared.Description))

	d, _ := s.c.DescribeFirewall(bg, &networkfirewall.DescribeFirewallInput{FirewallName: aws.String("fw-1")})
	assert.Empty(t, aws.ToString(d.Firewall.Description))
}

func TestSDK_CreateFirewall_NoVpcId(t *testing.T) {
	s := newStack(t)

	_, err := s.c.CreateFirewall(bg, &networkfirewall.CreateFirewallInput{
		FirewallName: aws.String("fw-novpc"), FirewallPolicyArn: aws.String(s.policyARN),
		SubnetMappings: []nftypes.SubnetMapping{{SubnetId: aws.String("subnet-1")}},
	})

	var invalid *nftypes.InvalidRequestException
	require.ErrorAs(t, err, &invalid)
}

func TestSDK_DeleteGuards_TypedErrors(t *testing.T) {
	s := newStack(t)

	var invalidOp *nftypes.InvalidOperationException

	_, err := s.c.DeleteFirewallPolicy(bg, &networkfirewall.DeleteFirewallPolicyInput{FirewallPolicyName: aws.String("pol-1")})
	require.ErrorAs(t, err, &invalidOp, "policy in use by a firewall")

	_, err = s.c.UpdateFirewallDeleteProtection(bg, &networkfirewall.UpdateFirewallDeleteProtectionInput{FirewallName: aws.String("fw-1"), DeleteProtection: true})
	require.NoError(t, err)

	_, err = s.c.DeleteFirewall(bg, &networkfirewall.DeleteFirewallInput{FirewallName: aws.String("fw-1")})
	require.ErrorAs(t, err, &invalidOp, "delete protection")

	rg, err := s.c.CreateRuleGroup(bg, &networkfirewall.CreateRuleGroupInput{RuleGroupName: aws.String("rg-1"), Type: nftypes.RuleGroupTypeStateful, Capacity: aws.Int32(5)})
	require.NoError(t, err)

	_, err = s.c.CreateFirewallPolicy(bg, &networkfirewall.CreateFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-rg"),
		FirewallPolicy: &nftypes.FirewallPolicy{
			StatelessDefaultActions: []string{"aws:pass"}, StatelessFragmentDefaultActions: []string{"aws:pass"},
			StatefulRuleGroupReferences: []nftypes.StatefulRuleGroupReference{{ResourceArn: rg.RuleGroupResponse.RuleGroupArn}},
		},
	})
	require.NoError(t, err)

	_, err = s.c.DeleteRuleGroup(bg, &networkfirewall.DeleteRuleGroupInput{RuleGroupName: aws.String("rg-1"), Type: nftypes.RuleGroupTypeStateful})
	require.ErrorAs(t, err, &invalidOp, "rule group referenced by a policy")
}

func TestSDK_LoggingConfiguration_RoundTrip(t *testing.T) {
	s := newStack(t)

	cfg := func(dests ...nftypes.LogDestinationConfig) *networkfirewall.UpdateLoggingConfigurationInput {
		return &networkfirewall.UpdateLoggingConfigurationInput{
			FirewallName: aws.String("fw-1"), LoggingConfiguration: &nftypes.LoggingConfiguration{LogDestinationConfigs: dests},
		}
	}
	s3 := func(lt nftypes.LogType, bucket string) nftypes.LogDestinationConfig {
		return nftypes.LogDestinationConfig{LogType: lt, LogDestinationType: nftypes.LogDestinationTypeS3, LogDestination: map[string]string{"bucketName": bucket}}
	}

	var invalid *nftypes.InvalidRequestException

	_, err := s.c.UpdateLoggingConfiguration(bg, cfg(nftypes.LogDestinationConfig{
		LogType: nftypes.LogType("BOGUS"), LogDestinationType: nftypes.LogDestinationTypeS3, LogDestination: map[string]string{"bucketName": "b"},
	}))
	require.ErrorAs(t, err, &invalid)

	_, err = s.c.UpdateLoggingConfiguration(bg, cfg(s3(nftypes.LogTypeFlow, "b1")))
	require.NoError(t, err)

	_, err = s.c.UpdateLoggingConfiguration(bg, cfg(s3(nftypes.LogTypeFlow, "b1"), s3(nftypes.LogTypeAlert, "b1"), s3(nftypes.LogTypeTls, "b1")))
	require.ErrorAs(t, err, &invalid, "two additions in one call")

	dash := true
	in := cfg(s3(nftypes.LogTypeFlow, "b1"), s3(nftypes.LogTypeAlert, "b2"))
	in.EnableMonitoringDashboard = &dash

	up, err := s.c.UpdateLoggingConfiguration(bg, in)
	require.NoError(t, err)
	assert.Len(t, up.LoggingConfiguration.LogDestinationConfigs, 2)
	assert.True(t, aws.ToBool(up.EnableMonitoringDashboard))

	d, err := s.c.DescribeLoggingConfiguration(bg, &networkfirewall.DescribeLoggingConfigurationInput{FirewallName: aws.String("fw-1")})
	require.NoError(t, err)
	assert.Equal(t, up.LoggingConfiguration.LogDestinationConfigs, d.LoggingConfiguration.LogDestinationConfigs)
	assert.True(t, aws.ToBool(d.EnableMonitoringDashboard))

	// An omitted configuration disables logging.
	_, err = s.c.UpdateLoggingConfiguration(bg, &networkfirewall.UpdateLoggingConfigurationInput{FirewallName: aws.String("fw-1")})
	require.ErrorAs(t, err, &invalid, "removing two destinations at once")
}

func TestSDK_CreateRuleGroup_Capacity0(t *testing.T) {
	c := newClient(t)

	_, err := c.CreateRuleGroup(bg, &networkfirewall.CreateRuleGroupInput{RuleGroupName: aws.String("rg"), Type: nftypes.RuleGroupTypeStateful, Capacity: aws.Int32(0)})

	var invalid *nftypes.InvalidRequestException
	require.ErrorAs(t, err, &invalid)

	_, err = c.CreateRuleGroup(bg, &networkfirewall.CreateRuleGroupInput{
		RuleGroupName: aws.String("rg"), Type: nftypes.RuleGroupTypeStateful, Capacity: aws.Int32(5),
		RuleGroup: &nftypes.RuleGroup{RulesSource: &nftypes.RulesSource{RulesString: aws.String("pass tcp any any -> any any (sid:1;)")}},
		Rules:     aws.String("pass tcp any any -> any any (sid:2;)"),
	})
	require.ErrorAs(t, err, &invalid, "RuleGroup and Rules together")
}

func TestSDK_DeleteRuleGroup_InUse(t *testing.T) {
	TestSDK_DeleteGuards_TypedErrors(t)
}

func TestSDK_ComputedFieldsDeserialize(t *testing.T) {
	s := newStack(t)

	rg, err := s.c.CreateRuleGroup(bg, &networkfirewall.CreateRuleGroupInput{
		RuleGroupName: aws.String("rg-c"), Type: nftypes.RuleGroupTypeStateful, Capacity: aws.Int32(10),
		Rules: aws.String("pass tcp any any -> any any (sid:1;)\npass udp any any -> any any (sid:2;)\n"),
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), aws.ToInt32(rg.RuleGroupResponse.ConsumedCapacity))
	assert.Equal(t, nftypes.ResourceStatusActive, rg.RuleGroupResponse.RuleGroupStatus)
	assert.NotNil(t, rg.RuleGroupResponse.LastModifiedTime)

	pol, err := s.c.CreateFirewallPolicy(bg, &networkfirewall.CreateFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-c"),
		FirewallPolicy: &nftypes.FirewallPolicy{
			StatelessDefaultActions: []string{"aws:pass"}, StatelessFragmentDefaultActions: []string{"aws:pass"},
			StatefulRuleGroupReferences: []nftypes.StatefulRuleGroupReference{{ResourceArn: rg.RuleGroupResponse.RuleGroupArn}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(10), aws.ToInt32(pol.FirewallPolicyResponse.ConsumedStatefulRuleCapacity))

	d, err := s.c.DescribeFirewallPolicy(bg, &networkfirewall.DescribeFirewallPolicyInput{FirewallPolicyName: aws.String("pol-1")})
	require.NoError(t, err)
	assert.Equal(t, int32(1), aws.ToInt32(d.FirewallPolicyResponse.NumberOfAssociations))
	assert.Equal(t, nftypes.ResourceStatusActive, d.FirewallPolicyResponse.FirewallPolicyStatus)
	assert.NotNil(t, d.FirewallPolicyResponse.LastModifiedTime)

	rgd, err := s.c.DescribeRuleGroup(bg, &networkfirewall.DescribeRuleGroupInput{RuleGroupName: aws.String("rg-c"), Type: nftypes.RuleGroupTypeStateful})
	require.NoError(t, err)
	assert.Equal(t, int32(1), aws.ToInt32(rgd.RuleGroupResponse.NumberOfAssociations))
}

func TestSDK_ResourcePolicy(t *testing.T) {
	s := newStack(t)

	doc, _ := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect": "Allow", "Principal": map[string]string{"AWS": "arn:aws:iam::111122223333:root"},
			"Action": []string{"network-firewall:AssociateFirewallPolicy"}, "Resource": s.policyARN,
		}},
	})

	_, err := s.c.PutResourcePolicy(bg, &networkfirewall.PutResourcePolicyInput{ResourceArn: aws.String(s.policyARN), Policy: aws.String(string(doc))})
	require.NoError(t, err)

	got, err := s.c.DescribeResourcePolicy(bg, &networkfirewall.DescribeResourcePolicyInput{ResourceArn: aws.String(s.policyARN)})
	require.NoError(t, err)
	assert.JSONEq(t, string(doc), aws.ToString(got.Policy))

	var badPolicy *nftypes.InvalidResourcePolicyException

	_, err = s.c.PutResourcePolicy(bg, &networkfirewall.PutResourcePolicyInput{
		ResourceArn: aws.String(s.policyARN), Policy: aws.String(`{"Statement":[{"Action":"network-firewall:ListFirewalls"}]}`),
	})
	require.ErrorAs(t, err, &badPolicy)

	_, err = s.c.DeleteResourcePolicy(bg, &networkfirewall.DeleteResourcePolicyInput{ResourceArn: aws.String(s.policyARN)})
	require.NoError(t, err)

	var notFound *nftypes.ResourceNotFoundException

	_, err = s.c.DescribeResourcePolicy(bg, &networkfirewall.DescribeResourcePolicyInput{ResourceArn: aws.String(s.policyARN)})
	require.ErrorAs(t, err, &notFound)
}

func tlsConfig(certARN string) *nftypes.TLSInspectionConfiguration {
	return &nftypes.TLSInspectionConfiguration{
		ServerCertificateConfigurations: []nftypes.ServerCertificateConfiguration{{
			ServerCertificates: []nftypes.ServerCertificate{{ResourceArn: aws.String(certARN)}},
		}},
	}
}

func TestSDK_TLSInspection_RoundTrip(t *testing.T) {
	c := newClient(t)
	certARN := "arn:aws:acm:us-east-1:123456789012:certificate/c1"

	created, err := c.CreateTLSInspectionConfiguration(bg, &networkfirewall.CreateTLSInspectionConfigurationInput{
		TLSInspectionConfigurationName: aws.String("tls-1"), TLSInspectionConfiguration: tlsConfig(certARN),
		Description: aws.String("d"), Tags: []nftypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	require.NoError(t, err)

	resp := created.TLSInspectionConfigurationResponse
	assert.Equal(t, nftypes.ResourceStatusActive, resp.TLSInspectionConfigurationStatus)
	require.Len(t, resp.Certificates, 1)
	assert.Equal(t, certARN, aws.ToString(resp.Certificates[0].CertificateArn))
	assert.NotNil(t, resp.LastModifiedTime)

	d, err := c.DescribeTLSInspectionConfiguration(bg, &networkfirewall.DescribeTLSInspectionConfigurationInput{TLSInspectionConfigurationArn: resp.TLSInspectionConfigurationArn})
	require.NoError(t, err)
	assert.Equal(t, aws.ToString(created.UpdateToken), aws.ToString(d.UpdateToken))
	require.NotNil(t, d.TLSInspectionConfiguration)
	assert.Len(t, d.TLSInspectionConfiguration.ServerCertificateConfigurations, 1)

	_, err = c.UpdateTLSInspectionConfiguration(bg, &networkfirewall.UpdateTLSInspectionConfigurationInput{
		TLSInspectionConfigurationName: aws.String("tls-1"), TLSInspectionConfiguration: tlsConfig(certARN), UpdateToken: aws.String("11111111-1111-1111-1111-111111111111"),
	})

	var invalidToken *nftypes.InvalidTokenException
	require.ErrorAs(t, err, &invalidToken)

	up, err := c.UpdateTLSInspectionConfiguration(bg, &networkfirewall.UpdateTLSInspectionConfigurationInput{
		TLSInspectionConfigurationName: aws.String("tls-1"), TLSInspectionConfiguration: tlsConfig("arn:aws:acm:us-east-1:123456789012:certificate/c2"),
		UpdateToken: d.UpdateToken, Description: aws.String("d2"),
	})
	require.NoError(t, err)
	assert.Equal(t, "d2", aws.ToString(up.TLSInspectionConfigurationResponse.Description))

	list, err := c.ListTLSInspectionConfigurations(bg, &networkfirewall.ListTLSInspectionConfigurationsInput{})
	require.NoError(t, err)
	require.Len(t, list.TLSInspectionConfigurations, 1)

	_, err = c.DeleteTLSInspectionConfiguration(bg, &networkfirewall.DeleteTLSInspectionConfigurationInput{TLSInspectionConfigurationName: aws.String("tls-1")})
	require.NoError(t, err)
}

func TestSDK_VPCEndpointAssociation_RoundTrip(t *testing.T) {
	s := newStack(t)

	created, err := s.c.CreateVpcEndpointAssociation(bg, &networkfirewall.CreateVpcEndpointAssociationInput{
		FirewallArn: aws.String(s.fwARN), VpcId: aws.String("vpc-2b"), SubnetMapping: &nftypes.SubnetMapping{SubnetId: aws.String("subnet-9")},
		Description: aws.String("extra"), Tags: []nftypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	require.NoError(t, err)

	arn := aws.ToString(created.VpcEndpointAssociation.VpcEndpointAssociationArn)
	assert.Equal(t, nftypes.FirewallStatusValueReady, created.VpcEndpointAssociationStatus.Status)
	assert.NotEmpty(t, created.VpcEndpointAssociationStatus.AssociationSyncState)

	d, err := s.c.DescribeVpcEndpointAssociation(bg, &networkfirewall.DescribeVpcEndpointAssociationInput{VpcEndpointAssociationArn: aws.String(arn)})
	require.NoError(t, err)
	assert.Equal(t, "subnet-9", aws.ToString(d.VpcEndpointAssociation.SubnetMapping.SubnetId))

	list, err := s.c.ListVpcEndpointAssociations(bg, &networkfirewall.ListVpcEndpointAssociationsInput{FirewallArn: aws.String(s.fwARN)})
	require.NoError(t, err)
	assert.Len(t, list.VpcEndpointAssociations, 1)

	tags, err := s.c.ListTagsForResource(bg, &networkfirewall.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	require.NoError(t, err)
	assert.Len(t, tags.Tags, 1)

	fwd, _ := s.c.DescribeFirewall(bg, &networkfirewall.DescribeFirewallInput{FirewallName: aws.String("fw-1")})
	assert.Equal(t, int32(1), aws.ToInt32(fwd.Firewall.NumberOfAssociations))

	var invalidOp *nftypes.InvalidOperationException

	_, err = s.c.DeleteFirewall(bg, &networkfirewall.DeleteFirewallInput{FirewallName: aws.String("fw-1")})
	require.ErrorAs(t, err, &invalidOp, "firewall with associations")

	_, err = s.c.DeleteVpcEndpointAssociation(bg, &networkfirewall.DeleteVpcEndpointAssociationInput{VpcEndpointAssociationArn: aws.String(arn)})
	require.NoError(t, err)

	var notFound *nftypes.ResourceNotFoundException

	_, err = s.c.DescribeVpcEndpointAssociation(bg, &networkfirewall.DescribeVpcEndpointAssociationInput{VpcEndpointAssociationArn: aws.String(arn)})
	require.ErrorAs(t, err, &notFound)
}

func TestSDK_Metadata_Ops(t *testing.T) {
	s := newStack(t)

	rg, err := s.c.CreateRuleGroup(bg, &networkfirewall.CreateRuleGroupInput{
		RuleGroupName: aws.String("rg-m"), Type: nftypes.RuleGroupTypeStateful, Capacity: aws.Int32(10),
		Rules:                aws.String(`alert tcp any any -> any 80 (msg:"web"; sid:7;)`),
		SummaryConfiguration: &nftypes.SummaryConfiguration{RuleOptions: []nftypes.SummaryRuleOption{nftypes.SummaryRuleOptionMsg, nftypes.SummaryRuleOptionSid}},
	})
	require.NoError(t, err)

	fm, err := s.c.DescribeFirewallMetadata(bg, &networkfirewall.DescribeFirewallMetadataInput{FirewallArn: aws.String(s.fwARN)})
	require.NoError(t, err)
	assert.Equal(t, s.policyARN, aws.ToString(fm.FirewallPolicyArn))
	assert.Equal(t, nftypes.FirewallStatusValueReady, fm.Status)
	assert.Contains(t, fm.SupportedAvailabilityZones, "us-east-1a")

	rm, err := s.c.DescribeRuleGroupMetadata(bg, &networkfirewall.DescribeRuleGroupMetadataInput{RuleGroupArn: rg.RuleGroupResponse.RuleGroupArn})
	require.NoError(t, err)
	assert.Equal(t, int32(10), aws.ToInt32(rm.Capacity))
	assert.Equal(t, nftypes.RuleGroupTypeStateful, rm.Type)

	sum, err := s.c.DescribeRuleGroupSummary(bg, &networkfirewall.DescribeRuleGroupSummaryInput{RuleGroupName: aws.String("rg-m"), Type: nftypes.RuleGroupTypeStateful})
	require.NoError(t, err)
	require.Len(t, sum.Summary.RuleSummaries, 1)
	assert.Equal(t, "web", aws.ToString(sum.Summary.RuleSummaries[0].Msg))
	assert.Equal(t, "7", aws.ToString(sum.Summary.RuleSummaries[0].SID))
}

func TestSDK_FlowAndAnalysis_Ops(t *testing.T) {
	s := newStack(t)

	_, err := s.c.UpdateFirewallAnalysisSettings(bg, &networkfirewall.UpdateFirewallAnalysisSettingsInput{
		FirewallName: aws.String("fw-1"), EnabledAnalysisTypes: []nftypes.EnabledAnalysisType{nftypes.EnabledAnalysisTypeTlsSni},
	})
	require.NoError(t, err)

	capture, err := s.c.StartFlowCapture(bg, &networkfirewall.StartFlowCaptureInput{FirewallArn: aws.String(s.fwARN), AvailabilityZone: aws.String("us-east-1a"), FlowFilters: []nftypes.FlowFilter{{SourcePort: aws.String("80")}}})
	require.NoError(t, err)
	assert.Equal(t, nftypes.FlowOperationStatusCompleted, capture.FlowOperationStatus)

	flush, err := s.c.StartFlowFlush(bg, &networkfirewall.StartFlowFlushInput{FirewallArn: aws.String(s.fwARN), AvailabilityZone: aws.String("us-east-1a"), FlowFilters: []nftypes.FlowFilter{{SourcePort: aws.String("80")}}})
	require.NoError(t, err)

	d, err := s.c.DescribeFlowOperation(bg, &networkfirewall.DescribeFlowOperationInput{
		FirewallArn: aws.String(s.fwARN), AvailabilityZone: aws.String("us-east-1a"), FlowOperationId: capture.FlowOperationId,
	})
	require.NoError(t, err)
	assert.Equal(t, nftypes.FlowOperationTypeFlowCapture, d.FlowOperationType)
	assert.NotNil(t, d.FlowRequestTimestamp)

	ops, err := s.c.ListFlowOperations(bg, &networkfirewall.ListFlowOperationsInput{
		FirewallArn: aws.String(s.fwARN), AvailabilityZone: aws.String("us-east-1a"), FlowOperationType: nftypes.FlowOperationTypeFlowFlush,
	})
	require.NoError(t, err)
	require.Len(t, ops.FlowOperations, 1)
	assert.Equal(t, aws.ToString(flush.FlowOperationId), aws.ToString(ops.FlowOperations[0].FlowOperationId))

	res, err := s.c.ListFlowOperationResults(bg, &networkfirewall.ListFlowOperationResultsInput{
		FirewallArn: aws.String(s.fwARN), AvailabilityZone: aws.String("us-east-1a"), FlowOperationId: capture.FlowOperationId,
	})
	require.NoError(t, err)
	assert.Empty(t, res.Flows, "no data plane, no flows")

	started, err := s.c.StartAnalysisReport(bg, &networkfirewall.StartAnalysisReportInput{FirewallName: aws.String("fw-1"), AnalysisType: nftypes.EnabledAnalysisTypeTlsSni})
	require.NoError(t, err)

	reports, err := s.c.ListAnalysisReports(bg, &networkfirewall.ListAnalysisReportsInput{FirewallArn: aws.String(s.fwARN)})
	require.NoError(t, err)
	require.Len(t, reports.AnalysisReports, 1)
	assert.Equal(t, nftypes.EnabledAnalysisTypeTlsSni, reports.AnalysisReports[0].AnalysisType)

	got, err := s.c.GetAnalysisReportResults(bg, &networkfirewall.GetAnalysisReportResultsInput{FirewallName: aws.String("fw-1"), AnalysisReportId: started.AnalysisReportId})
	require.NoError(t, err)
	assert.Equal(t, nftypes.EnabledAnalysisTypeTlsSni, got.AnalysisType)
	assert.Empty(t, got.AnalysisReportResults)

	var invalid *nftypes.InvalidRequestException

	_, err = s.c.StartAnalysisReport(bg, &networkfirewall.StartAnalysisReportInput{FirewallName: aws.String("fw-1"), AnalysisType: nftypes.EnabledAnalysisTypeHttpHost})
	require.ErrorAs(t, err, &invalid, "HTTP_HOST analysis is not enabled")
}

func TestSDK_Pagination_Paginators(t *testing.T) {
	s := newStack(t)

	for i := 2; i <= 4; i++ {
		_, err := s.c.CreateFirewall(bg, &networkfirewall.CreateFirewallInput{
			FirewallName: aws.String(fmt.Sprintf("fw-%d", i)), FirewallPolicyArn: aws.String(s.policyARN), VpcId: aws.String(fmt.Sprintf("vpc-%da", i)),
		})
		require.NoError(t, err)
	}

	pager := networkfirewall.NewListFirewallsPaginator(s.c, &networkfirewall.ListFirewallsInput{MaxResults: aws.Int32(1)})

	var names []string

	for pager.HasMorePages() {
		page, err := pager.NextPage(bg)
		require.NoError(t, err)
		require.Len(t, page.Firewalls, 1)

		names = append(names, aws.ToString(page.Firewalls[0].FirewallName))
	}

	assert.Equal(t, []string{"fw-1", "fw-2", "fw-3", "fw-4"}, names)

	filtered, err := s.c.ListFirewalls(bg, &networkfirewall.ListFirewallsInput{VpcIds: []string{"vpc-3a"}})
	require.NoError(t, err)
	require.Len(t, filtered.Firewalls, 1)
	assert.Equal(t, "fw-3", aws.ToString(filtered.Firewalls[0].FirewallName))

	var invalid *nftypes.InvalidRequestException

	_, err = s.c.ListFirewalls(bg, &networkfirewall.ListFirewallsInput{NextToken: aws.String("zzz!")})
	require.ErrorAs(t, err, &invalid)

	_, err = s.c.ListFirewallPolicies(bg, &networkfirewall.ListFirewallPoliciesInput{MaxResults: aws.Int32(101)})
	require.ErrorAs(t, err, &invalid)

	pols := networkfirewall.NewListFirewallPoliciesPaginator(s.c, &networkfirewall.ListFirewallPoliciesInput{MaxResults: aws.Int32(1)})

	seen := 0

	for pols.HasMorePages() {
		page, err := pols.NextPage(bg)
		require.NoError(t, err)

		seen += len(page.FirewallPolicies)
	}

	assert.Equal(t, 1, seen)
}

func TestSDK_Policy_FullRoundTrip(t *testing.T) {
	c := newClient(t)

	tls, err := c.CreateTLSInspectionConfiguration(bg, &networkfirewall.CreateTLSInspectionConfigurationInput{
		TLSInspectionConfigurationName: aws.String("tls-p"), TLSInspectionConfiguration: tlsConfig("arn:aws:acm:us-east-1:123456789012:certificate/c1"),
	})
	require.NoError(t, err)

	_, err = c.CreateFirewallPolicy(bg, &networkfirewall.CreateFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-full"),
		FirewallPolicy: &nftypes.FirewallPolicy{
			StatelessDefaultActions:         []string{"aws:pass", "mine"},
			StatelessFragmentDefaultActions: []string{"aws:drop"},
			StatelessCustomActions: []nftypes.CustomAction{{
				ActionName: aws.String("mine"),
				ActionDefinition: &nftypes.ActionDefinition{PublishMetricAction: &nftypes.PublishMetricAction{
					Dimensions: []nftypes.Dimension{{Value: aws.String("v")}},
				}},
			}},
			StatefulDefaultActions:        []string{"aws:drop_strict"},
			StatefulEngineOptions:         &nftypes.StatefulEngineOptions{RuleOrder: nftypes.RuleOrderStrictOrder},
			PolicyVariables:               &nftypes.PolicyVariables{RuleVariables: map[string]nftypes.IPSet{"HOME_NET": {Definition: []string{"10.0.0.0/8"}}}},
			TLSInspectionConfigurationArn: tls.TLSInspectionConfigurationResponse.TLSInspectionConfigurationArn,
			EnableTLSSessionHolding:       aws.Bool(true),
		},
	})
	require.NoError(t, err)

	d, err := c.DescribeFirewallPolicy(bg, &networkfirewall.DescribeFirewallPolicyInput{FirewallPolicyName: aws.String("pol-full")})
	require.NoError(t, err)

	p := d.FirewallPolicy
	assert.Equal(t, []string{"aws:pass", "mine"}, p.StatelessDefaultActions)
	assert.Equal(t, []string{"aws:drop_strict"}, p.StatefulDefaultActions)
	assert.Equal(t, nftypes.RuleOrderStrictOrder, p.StatefulEngineOptions.RuleOrder)
	require.Len(t, p.StatelessCustomActions, 1)
	assert.Equal(t, "mine", aws.ToString(p.StatelessCustomActions[0].ActionName))
	assert.Equal(t, []string{"10.0.0.0/8"}, p.PolicyVariables.RuleVariables["HOME_NET"].Definition)
	assert.True(t, aws.ToBool(p.EnableTLSSessionHolding))
	assert.Equal(t, aws.ToString(tls.TLSInspectionConfigurationResponse.TLSInspectionConfigurationArn), aws.ToString(p.TLSInspectionConfigurationArn))

	var invalid *nftypes.InvalidRequestException

	_, err = c.CreateFirewallPolicy(bg, &networkfirewall.CreateFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-bad"),
		FirewallPolicy: &nftypes.FirewallPolicy{
			StatelessDefaultActions: []string{"aws:nope"}, StatelessFragmentDefaultActions: []string{"aws:drop"},
		},
	})
	require.ErrorAs(t, err, &invalid)
}

func TestSDK_ListRuleGroups_FiltersAndTags(t *testing.T) {
	s := newStack(t)

	for _, rg := range []struct {
		name string
		typ  nftypes.RuleGroupType
	}{{"rg-a", nftypes.RuleGroupTypeStateless}, {"rg-b", nftypes.RuleGroupTypeStateful}} {
		_, err := s.c.CreateRuleGroup(bg, &networkfirewall.CreateRuleGroupInput{RuleGroupName: aws.String(rg.name), Type: rg.typ, Capacity: aws.Int32(3)})
		require.NoError(t, err)
	}

	all, err := s.c.ListRuleGroups(bg, &networkfirewall.ListRuleGroupsInput{})
	require.NoError(t, err)
	assert.Len(t, all.RuleGroups, 2)

	stateless, err := s.c.ListRuleGroups(bg, &networkfirewall.ListRuleGroupsInput{Type: nftypes.RuleGroupTypeStateless})
	require.NoError(t, err)
	require.Len(t, stateless.RuleGroups, 1)
	assert.Equal(t, "rg-a", aws.ToString(stateless.RuleGroups[0].Name))

	managed, err := s.c.ListRuleGroups(bg, &networkfirewall.ListRuleGroupsInput{Scope: nftypes.ResourceManagedStatusManaged})
	require.NoError(t, err)
	assert.Empty(t, managed.RuleGroups)

	// Tags work on every resource kind, addressed by ARN.
	for _, arn := range []string{s.policyARN, s.fwARN, aws.ToString(all.RuleGroups[0].Arn)} {
		_, err := s.c.TagResource(bg, &networkfirewall.TagResourceInput{ResourceArn: aws.String(arn), Tags: []nftypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}}})
		require.NoError(t, err)

		got, err := s.c.ListTagsForResource(bg, &networkfirewall.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
		require.NoError(t, err)
		assert.Len(t, got.Tags, 1)
	}

	fw, err := s.c.DescribeFirewall(bg, &networkfirewall.DescribeFirewallInput{FirewallName: aws.String("fw-1")})
	require.NoError(t, err)
	assert.Len(t, fw.Firewall.Tags, 1)
}

func TestSDK_DescribeByARN_And_Deletes(t *testing.T) {
	s := newStack(t)

	rg, err := s.c.CreateRuleGroup(bg, &networkfirewall.CreateRuleGroupInput{RuleGroupName: aws.String("rg"), Type: nftypes.RuleGroupTypeStateful, Capacity: aws.Int32(3)})
	require.NoError(t, err)

	byARN, err := s.c.DescribeRuleGroup(bg, &networkfirewall.DescribeRuleGroupInput{RuleGroupArn: rg.RuleGroupResponse.RuleGroupArn})
	require.NoError(t, err)
	assert.Equal(t, "rg", aws.ToString(byARN.RuleGroupResponse.RuleGroupName))

	_, err = s.c.DeleteRuleGroup(bg, &networkfirewall.DeleteRuleGroupInput{RuleGroupArn: rg.RuleGroupResponse.RuleGroupArn})
	require.NoError(t, err)

	var notFound *nftypes.ResourceNotFoundException

	_, err = s.c.DescribeRuleGroup(bg, &networkfirewall.DescribeRuleGroupInput{RuleGroupArn: rg.RuleGroupResponse.RuleGroupArn})
	require.ErrorAs(t, err, &notFound)

	_, err = s.c.DeleteFirewall(bg, &networkfirewall.DeleteFirewallInput{FirewallArn: aws.String(s.fwARN)})
	require.NoError(t, err)

	_, err = s.c.DeleteFirewallPolicy(bg, &networkfirewall.DeleteFirewallPolicyInput{FirewallPolicyArn: aws.String(s.policyARN)})
	require.NoError(t, err)

	var invalid *nftypes.InvalidRequestException

	_, err = s.c.DescribeFirewall(bg, &networkfirewall.DescribeFirewallInput{FirewallArn: aws.String("not-an-arn")})
	require.ErrorAs(t, err, &invalid)

	_, err = s.c.CreateRuleGroup(bg, &networkfirewall.CreateRuleGroupInput{RuleGroupName: aws.String("dup"), Type: nftypes.RuleGroupTypeStateful, Capacity: aws.Int32(1)})
	require.NoError(t, err)

	_, err = s.c.CreateRuleGroup(bg, &networkfirewall.CreateRuleGroupInput{RuleGroupName: aws.String("dup"), Type: nftypes.RuleGroupTypeStateful, Capacity: aws.Int32(1)})
	require.ErrorAs(t, err, &invalid, "a duplicate name is InvalidRequestException")
}
