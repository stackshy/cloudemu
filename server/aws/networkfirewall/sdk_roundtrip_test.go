package networkfirewall_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	nftypes "github.com/aws/aws-sdk-go-v2/service/networkfirewall/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *networkfirewall.Client {
	t.Helper()

	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{NetworkFirewall: provider.NetworkFirewall})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	require.NoError(t, err)

	return networkfirewall.NewFromConfig(cfg, func(o *networkfirewall.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKNetworkFirewall(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	// Rule group.
	rg, err := client.CreateRuleGroup(ctx, &networkfirewall.CreateRuleGroupInput{
		RuleGroupName: aws.String("rg-1"),
		Type:          nftypes.RuleGroupTypeStateful,
		Capacity:      aws.Int32(100),
		Description:   aws.String("stateful rules"),
	})
	require.NoError(t, err)
	assert.Equal(t, "rg-1", aws.ToString(rg.RuleGroupResponse.RuleGroupName))
	assert.NotEmpty(t, aws.ToString(rg.RuleGroupResponse.RuleGroupArn))

	// Firewall policy.
	pol, err := client.CreateFirewallPolicy(ctx, &networkfirewall.CreateFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-1"),
		FirewallPolicy: &nftypes.FirewallPolicy{
			StatelessDefaultActions:         []string{"aws:forward_to_sfe"},
			StatelessFragmentDefaultActions: []string{"aws:forward_to_sfe"},
		},
	})
	require.NoError(t, err)
	policyARN := aws.ToString(pol.FirewallPolicyResponse.FirewallPolicyArn)
	require.NotEmpty(t, policyARN)

	descPol, err := client.DescribeFirewallPolicy(ctx, &networkfirewall.DescribeFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-1"),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"aws:forward_to_sfe"}, descPol.FirewallPolicy.StatelessDefaultActions)

	// Firewall referencing the policy.
	fw, err := client.CreateFirewall(ctx, &networkfirewall.CreateFirewallInput{
		FirewallName:      aws.String("fw-1"),
		FirewallPolicyArn: aws.String(policyARN),
		VpcId:             aws.String("vpc-123"),
		SubnetMappings:    []nftypes.SubnetMapping{{SubnetId: aws.String("subnet-1")}},
	})
	require.NoError(t, err)
	assert.Equal(t, "fw-1", aws.ToString(fw.Firewall.FirewallName))
	assert.Equal(t, policyARN, aws.ToString(fw.Firewall.FirewallPolicyArn))

	descFw, err := client.DescribeFirewall(ctx, &networkfirewall.DescribeFirewallInput{
		FirewallName: aws.String("fw-1"),
	})
	require.NoError(t, err)
	assert.Equal(t, "vpc-123", aws.ToString(descFw.Firewall.VpcId))
	require.Len(t, descFw.Firewall.SubnetMappings, 1)
	assert.Equal(t, "subnet-1", aws.ToString(descFw.Firewall.SubnetMappings[0].SubnetId))

	// Depth: subnet association, delete protection, logging, tags.
	_, err = client.AssociateSubnets(ctx, &networkfirewall.AssociateSubnetsInput{
		FirewallName:   aws.String("fw-1"),
		SubnetMappings: []nftypes.SubnetMapping{{SubnetId: aws.String("subnet-2")}},
	})
	require.NoError(t, err)

	descAfterAssoc, err := client.DescribeFirewall(ctx, &networkfirewall.DescribeFirewallInput{
		FirewallName: aws.String("fw-1"),
	})
	require.NoError(t, err)
	assert.Len(t, descAfterAssoc.Firewall.SubnetMappings, 2)

	_, err = client.DisassociateSubnets(ctx, &networkfirewall.DisassociateSubnetsInput{
		FirewallName: aws.String("fw-1"), SubnetIds: []string{"subnet-2"},
	})
	require.NoError(t, err)

	_, err = client.UpdateFirewallDeleteProtection(ctx, &networkfirewall.UpdateFirewallDeleteProtectionInput{
		FirewallName: aws.String("fw-1"), DeleteProtection: true,
	})
	require.NoError(t, err)

	_, err = client.UpdateLoggingConfiguration(ctx, &networkfirewall.UpdateLoggingConfigurationInput{
		FirewallName: aws.String("fw-1"),
		LoggingConfiguration: &nftypes.LoggingConfiguration{
			LogDestinationConfigs: []nftypes.LogDestinationConfig{{
				LogType:            nftypes.LogTypeFlow,
				LogDestinationType: nftypes.LogDestinationTypeCloudwatchLogs,
				LogDestination:     map[string]string{"logGroup": "nf-logs"},
			}},
		},
	})
	require.NoError(t, err)

	descLog, err := client.DescribeLoggingConfiguration(ctx, &networkfirewall.DescribeLoggingConfigurationInput{
		FirewallName: aws.String("fw-1"),
	})
	require.NoError(t, err)
	require.Len(t, descLog.LoggingConfiguration.LogDestinationConfigs, 1)
	assert.Equal(t, nftypes.LogTypeFlow, descLog.LoggingConfiguration.LogDestinationConfigs[0].LogType)

	fwARN := aws.ToString(descFw.Firewall.FirewallArn)
	_, err = client.TagResource(ctx, &networkfirewall.TagResourceInput{
		ResourceArn: aws.String(fwARN),
		Tags:        []nftypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	require.NoError(t, err)

	_, err = client.UntagResource(ctx, &networkfirewall.UntagResourceInput{
		ResourceArn: aws.String(fwARN), TagKeys: []string{"env"},
	})
	require.NoError(t, err)

	// Turn off delete protection so the delete below succeeds.
	_, err = client.UpdateFirewallDeleteProtection(ctx, &networkfirewall.UpdateFirewallDeleteProtectionInput{
		FirewallName: aws.String("fw-1"), DeleteProtection: false,
	})
	require.NoError(t, err)

	// List + delete.
	list, err := client.ListFirewalls(ctx, &networkfirewall.ListFirewallsInput{})
	require.NoError(t, err)
	assert.Len(t, list.Firewalls, 1)

	_, err = client.DeleteFirewall(ctx, &networkfirewall.DeleteFirewallInput{FirewallName: aws.String("fw-1")})
	require.NoError(t, err)

	_, err = client.DescribeFirewall(ctx, &networkfirewall.DescribeFirewallInput{FirewallName: aws.String("fw-1")})
	require.Error(t, err, "firewall should be gone after delete")

	// Error path: the JSON error envelope decodes into a typed SDK error.
	_, err = client.DescribeFirewall(ctx, &networkfirewall.DescribeFirewallInput{FirewallName: aws.String("does-not-exist")})
	var notFound *nftypes.ResourceNotFoundException
	require.ErrorAs(t, err, &notFound, "expected ResourceNotFoundException for unknown firewall")
}

// TestSDKDescribeFirewallStatus verifies DescribeFirewall reports a non-empty
// FirewallId plus a populated FirewallStatus (ConfigurationSyncStateSummary and
// SyncStates), which the FirewallReady waiter depends on.
func TestSDKDescribeFirewallStatus(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	pol, err := client.CreateFirewallPolicy(ctx, &networkfirewall.CreateFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-status"),
		FirewallPolicy: &nftypes.FirewallPolicy{
			StatelessDefaultActions:         []string{"aws:forward_to_sfe"},
			StatelessFragmentDefaultActions: []string{"aws:forward_to_sfe"},
		},
	})
	require.NoError(t, err)

	_, err = client.CreateFirewall(ctx, &networkfirewall.CreateFirewallInput{
		FirewallName:      aws.String("fw-status"),
		FirewallPolicyArn: pol.FirewallPolicyResponse.FirewallPolicyArn,
		VpcId:             aws.String("vpc-1"),
		SubnetMappings:    []nftypes.SubnetMapping{{SubnetId: aws.String("subnet-a")}},
	})
	require.NoError(t, err)

	desc, err := client.DescribeFirewall(ctx, &networkfirewall.DescribeFirewallInput{
		FirewallName: aws.String("fw-status"),
	})
	require.NoError(t, err)

	assert.NotEmpty(t, aws.ToString(desc.Firewall.FirewallId), "FirewallId must be populated")
	require.NotNil(t, desc.FirewallStatus)
	assert.Equal(t, nftypes.ConfigurationSyncStateInSync, desc.FirewallStatus.ConfigurationSyncStateSummary)
	require.Len(t, desc.FirewallStatus.SyncStates, 1, "one SyncState per attached subnet")

	for _, ss := range desc.FirewallStatus.SyncStates {
		require.NotNil(t, ss.Attachment)
		assert.Equal(t, "subnet-a", aws.ToString(ss.Attachment.SubnetId))
	}
}

// TestSDKUpdateFirewallPolicy verifies UpdateFirewallPolicy mutates a policy's
// stateless default actions rather than 400-ing as an unknown operation.
func TestSDKUpdateFirewallPolicy(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	_, err := client.CreateFirewallPolicy(ctx, &networkfirewall.CreateFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-upd"),
		FirewallPolicy: &nftypes.FirewallPolicy{
			StatelessDefaultActions:         []string{"aws:pass"},
			StatelessFragmentDefaultActions: []string{"aws:pass"},
		},
	})
	require.NoError(t, err)

	_, err = client.UpdateFirewallPolicy(ctx, &networkfirewall.UpdateFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-upd"),
		UpdateToken:        aws.String("00000000-0000-0000-0000-000000000000"),
		Description:        aws.String("updated"),
		FirewallPolicy: &nftypes.FirewallPolicy{
			StatelessDefaultActions:         []string{"aws:drop"},
			StatelessFragmentDefaultActions: []string{"aws:drop"},
		},
	})
	require.NoError(t, err)

	desc, err := client.DescribeFirewallPolicy(ctx, &networkfirewall.DescribeFirewallPolicyInput{
		FirewallPolicyName: aws.String("pol-upd"),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"aws:drop"}, desc.FirewallPolicy.StatelessDefaultActions)
	assert.Equal(t, "updated", aws.ToString(desc.FirewallPolicyResponse.Description))
}

// TestSDKUpdateRuleGroup verifies UpdateRuleGroup mutates a rule group's
// description rather than being rejected as an unknown operation.
func TestSDKUpdateRuleGroup(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	_, err := client.CreateRuleGroup(ctx, &networkfirewall.CreateRuleGroupInput{
		RuleGroupName: aws.String("rg-upd"),
		Type:          nftypes.RuleGroupTypeStateful,
		Capacity:      aws.Int32(100),
		Description:   aws.String("before"),
	})
	require.NoError(t, err)

	_, err = client.UpdateRuleGroup(ctx, &networkfirewall.UpdateRuleGroupInput{
		RuleGroupName: aws.String("rg-upd"),
		Type:          nftypes.RuleGroupTypeStateful,
		UpdateToken:   aws.String("00000000-0000-0000-0000-000000000000"),
		Description:   aws.String("after"),
	})
	require.NoError(t, err)

	desc, err := client.DescribeRuleGroup(ctx, &networkfirewall.DescribeRuleGroupInput{
		RuleGroupName: aws.String("rg-upd"),
		Type:          nftypes.RuleGroupTypeStateful,
	})
	require.NoError(t, err)
	assert.Equal(t, "after", aws.ToString(desc.RuleGroupResponse.Description))
}

// TestSDKListTagsForResource verifies tags applied via TagResource are readable
// through ListTagsForResource (Terraform refresh flows).
func TestSDKListTagsForResource(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	rg, err := client.CreateRuleGroup(ctx, &networkfirewall.CreateRuleGroupInput{
		RuleGroupName: aws.String("rg-tags"),
		Type:          nftypes.RuleGroupTypeStateless,
		Capacity:      aws.Int32(10),
	})
	require.NoError(t, err)

	arn := aws.ToString(rg.RuleGroupResponse.RuleGroupArn)
	_, err = client.TagResource(ctx, &networkfirewall.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []nftypes.Tag{{Key: aws.String("team"), Value: aws.String("net")}},
	})
	require.NoError(t, err)

	tags, err := client.ListTagsForResource(ctx, &networkfirewall.ListTagsForResourceInput{
		ResourceArn: aws.String(arn),
	})
	require.NoError(t, err)
	require.Len(t, tags.Tags, 1)
	assert.Equal(t, "team", aws.ToString(tags.Tags[0].Key))
	assert.Equal(t, "net", aws.ToString(tags.Tags[0].Value))
}
