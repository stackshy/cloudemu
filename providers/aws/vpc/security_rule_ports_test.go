package vpc

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func TestSecurityGroupRulePortValidation(t *testing.T) {
	tests := []struct {
		name    string
		rule    driver.SecurityRule
		wantErr bool
	}{
		{name: "tcp valid", rule: driver.SecurityRule{Protocol: "tcp", FromPort: 22, ToPort: 22}},
		{name: "tcp port above max", rule: driver.SecurityRule{Protocol: "tcp", FromPort: 99999, ToPort: 99999}, wantErr: true},
		{name: "udp from greater than to", rule: driver.SecurityRule{Protocol: "udp", FromPort: 100, ToPort: 50}, wantErr: true},
		{name: "tcp by number negative", rule: driver.SecurityRule{Protocol: "6", FromPort: -1, ToPort: 80}, wantErr: true},
		{name: "icmp any type", rule: driver.SecurityRule{Protocol: "icmp", FromPort: -1, ToPort: -1}},
		{name: "icmp type 8 any code", rule: driver.SecurityRule{Protocol: "icmp", FromPort: 8, ToPort: -1}},
		{name: "icmp type above 255", rule: driver.SecurityRule{Protocol: "icmp", FromPort: 256, ToPort: 0}, wantErr: true},
		{name: "icmpv6 code below -1", rule: driver.SecurityRule{Protocol: "58", FromPort: 0, ToPort: -2}, wantErr: true},
		{name: "all protocols ignores ports", rule: driver.SecurityRule{Protocol: "-1", FromPort: -1, ToPort: -1}},
	}

	for _, egress := range []bool{false, true} {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				m := newTestMock()
				v := createTestVPC(m)

				sg, err := m.CreateSecurityGroup(context.Background(),
					driver.SecurityGroupConfig{Name: "sg", Description: "sg", VPCID: v.ID})
				requireNoError(t, err)

				add := m.AddIngressRule
				if egress {
					add = m.AddEgressRule
				}

				tt.rule.CIDR = "10.0.0.0/16"
				err = add(context.Background(), sg.ID, tt.rule)

				switch {
				case tt.wantErr && !cerrors.IsInvalidArgument(err):
					t.Fatalf("egress=%v err = %v, want InvalidArgument", egress, err)
				case !tt.wantErr && err != nil:
					t.Fatalf("egress=%v unexpected error: %v", egress, err)
				}
			})
		}
	}
}

func TestModifySecurityGroupRuleRejectsBadPorts(t *testing.T) {
	m := newTestMock()
	groupID := sgWithRules(t, m)

	err := m.ModifySecurityGroupRule(context.Background(), groupID, "sgr-ingress", driver.SecurityRule{
		Protocol: "tcp", FromPort: 0, ToPort: 70000, CIDR: "0.0.0.0/0",
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}

	if got := ruleByID(t, m, groupID, "sgr-ingress"); got.ToPort != 22 {
		t.Fatalf("rejected modify changed the rule: %+v", got)
	}
}
