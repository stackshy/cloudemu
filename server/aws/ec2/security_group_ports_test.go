package ec2_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func newPortTestGroup(t *testing.T, ctx context.Context, c *ec2.Client) string {
	t.Helper()

	vpcID := createSGTestVPC(t, ctx, c, "10.0.0.0/16")

	sg, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName: aws.String("ports"), Description: aws.String("ports"), VpcId: aws.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}

	return aws.ToString(sg.GroupId)
}

func TestAuthorizeSecurityGroupPortValidation(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID := newPortTestGroup(t, ctx, c)

	tests := []struct {
		name     string
		protocol string
		from, to int32
		wantCode string
	}{
		{name: "port above 65535", protocol: "tcp", from: 99999, to: 99999, wantCode: "InvalidParameterValue"},
		{name: "from greater than to", protocol: "tcp", from: 443, to: 80, wantCode: "InvalidParameterValue"},
		{name: "icmp type above 255", protocol: "icmp", from: 300, to: 0, wantCode: "InvalidParameterValue"},
		{name: "valid tcp range", protocol: "tcp", from: 80, to: 443},
		{name: "icmp any", protocol: "icmp", from: -1, to: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perm := []ec2types.IpPermission{{
				IpProtocol: aws.String(tt.protocol), FromPort: aws.Int32(tt.from), ToPort: aws.Int32(tt.to),
				IpRanges: []ec2types.IpRange{{CidrIp: aws.String("10.1.0.0/16")}},
			}}

			_, err := c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
				GroupId: aws.String(groupID), IpPermissions: perm,
			})
			assertSGErrCode(t, "ingress", err, tt.wantCode)

			_, err = c.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
				GroupId: aws.String(groupID), IpPermissions: perm,
			})
			assertSGErrCode(t, "egress", err, tt.wantCode)
		})
	}
}

func TestModifySecurityGroupRulesPortValidation(t *testing.T) {
	ctx := context.Background()
	c := newSGServer(t)
	groupID, ruleID := authorizeIngressRuleID(t, ctx, c)

	_, err := c.ModifySecurityGroupRules(ctx, &ec2.ModifySecurityGroupRulesInput{
		GroupId: aws.String(groupID),
		SecurityGroupRules: []ec2types.SecurityGroupRuleUpdate{{
			SecurityGroupRuleId: aws.String(ruleID),
			SecurityGroupRule: &ec2types.SecurityGroupRuleRequest{
				IpProtocol: aws.String("tcp"), FromPort: aws.Int32(0), ToPort: aws.Int32(70000),
				CidrIpv4: aws.String("0.0.0.0/0"),
			},
		}},
	})
	assertSGErrCode(t, "modify", err, "InvalidParameterValue")
}

// TestAuthorizeSecurityGroupNonIntegerPort sends a raw form, because the SDK
// cannot put a non-integer in FromPort.
func TestAuthorizeSecurityGroupNonIntegerPort(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	vpc, err := cloud.VPC.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	sg, err := cloud.VPC.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{Name: "raw", Description: "raw", VPCID: vpc.ID})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}

	form := url.Values{
		"Action":                            {"AuthorizeSecurityGroupIngress"},
		"Version":                           {"2016-11-15"},
		"GroupId":                           {sg.ID},
		"IpPermissions.1.IpProtocol":        {"tcp"},
		"IpPermissions.1.FromPort":          {"abc"},
		"IpPermissions.1.ToPort":            {"80"},
		"IpPermissions.1.IpRanges.1.CidrIp": {"10.1.0.0/16"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "<Code>InvalidParameterValue</Code>") {
		t.Fatalf("status %d body %s, want 400 InvalidParameterValue", resp.StatusCode, body)
	}
}

func assertSGErrCode(t *testing.T, op string, err error, want string) {
	t.Helper()

	if want == "" {
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", op, err)
		}

		return
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != want {
		t.Fatalf("%s: err = %v, want %s", op, err, want)
	}
}
