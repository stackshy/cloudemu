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

	// List + delete.
	list, err := client.ListFirewalls(ctx, &networkfirewall.ListFirewallsInput{})
	require.NoError(t, err)
	assert.Len(t, list.Firewalls, 1)

	_, err = client.DeleteFirewall(ctx, &networkfirewall.DeleteFirewallInput{FirewallName: aws.String("fw-1")})
	require.NoError(t, err)

	_, err = client.DescribeFirewall(ctx, &networkfirewall.DescribeFirewallInput{FirewallName: aws.String("fw-1")})
	require.Error(t, err, "firewall should be gone after delete")
}
