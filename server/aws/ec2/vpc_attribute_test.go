package ec2

import (
	"net/http"
	"net/url"
	"testing"
)

func TestModifyVpcAttributeAcceptsDNSAttributes(t *testing.T) {
	h := newFullHandler()
	vpcID, _ := mkVPCAndSubnet(t, h)

	for _, attr := range []string{"EnableDnsHostnames", "EnableDnsSupport"} {
		resp := do(t, h, http.MethodPost, "/", url.Values{
			"Action": {"ModifyVpcAttribute"}, "VpcId": {vpcID},
			attr + ".Value": {"true"},
		})
		if resp.Code != http.StatusOK {
			t.Errorf("ModifyVpcAttribute(%s) = %d: %s", attr, resp.Code, resp.Body.String())
		}
	}
}

func TestModifyVpcAttributeUnknownVPCFails(t *testing.T) {
	h := newFullHandler()

	resp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"ModifyVpcAttribute"}, "VpcId": {"vpc-nope"},
		"EnableDnsSupport.Value": {"true"},
	})
	if resp.Code == http.StatusOK {
		t.Errorf("modifying an unknown VPC should fail, got 200: %s", resp.Body.String())
	}
}
