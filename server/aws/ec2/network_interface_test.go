package ec2

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A NAT gateway holds an ENI for as long as it lives. A caller deleting a VPC
// drains ENIs first precisely because that interface would otherwise refuse
// the delete, so the NAT->ENI link is the behaviour under test, not an
// incidental detail of the emulator.
func TestNATGatewayHoldsENIUntilDeleted(t *testing.T) {
	h := newFullHandler()
	vpcID, subnetID := mkVPCAndSubnet(t, h)

	natResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateNatGateway"}, "SubnetId": {subnetID},
	})
	if natResp.Code != http.StatusOK {
		t.Fatalf("CreateNatGateway = %d: %s", natResp.Code, natResp.Body.String())
	}

	natID := between(natResp.Body.String(), "<natGatewayId>", "</natGatewayId>")
	if natID == "" {
		t.Fatalf("CreateNatGateway returned no id: %s", natResp.Body.String())
	}

	descResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DescribeNetworkInterfaces"}, "Filter.1.Name": {"vpc-id"}, "Filter.1.Value.1": {vpcID},
	})
	if descResp.Code != http.StatusOK {
		t.Fatalf("DescribeNetworkInterfaces = %d: %s", descResp.Code, descResp.Body.String())
	}

	eniID := between(descResp.Body.String(), "<networkInterfaceId>", "</networkInterfaceId>")
	if eniID == "" {
		t.Fatalf("NAT gateway did not hold an ENI: %s", descResp.Body.String())
	}

	if !strings.Contains(descResp.Body.String(), "<status>in-use</status>") {
		t.Errorf("NAT ENI should be in-use: %s", descResp.Body.String())
	}

	// Deleting an attached ENI must fail — that refusal is what tells a
	// caller its drain is not finished yet.
	delAttached := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DeleteNetworkInterface"}, "NetworkInterfaceId": {eniID},
	})
	if delAttached.Code == http.StatusOK {
		t.Errorf("deleting an attached ENI should fail, got 200: %s", delAttached.Body.String())
	}

	delNAT := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DeleteNatGateway"}, "NatGatewayId": {natID},
	})
	if delNAT.Code != http.StatusOK {
		t.Fatalf("DeleteNatGateway = %d: %s", delNAT.Code, delNAT.Body.String())
	}

	after := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DescribeNetworkInterfaces"}, "Filter.1.Name": {"vpc-id"}, "Filter.1.Value.1": {vpcID},
	})
	if strings.Contains(after.Body.String(), eniID) {
		t.Errorf("ENI %s outlived its NAT gateway: %s", eniID, after.Body.String())
	}
}

// A caller drains by vpc-id and deletes whatever comes back. If the filter
// were ignored, it would delete interfaces belonging to somebody else's VPC.
func TestDescribeNetworkInterfacesHonoursVPCFilter(t *testing.T) {
	h := newFullHandler()
	vpcA, subnetA := mkVPCAndSubnet(t, h)
	_, subnetB := mkVPCAndSubnet(t, h)

	for _, sub := range []string{subnetA, subnetB} {
		resp := do(t, h, http.MethodPost, "/", url.Values{
			"Action": {"CreateNatGateway"}, "SubnetId": {sub},
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("CreateNatGateway(%s) = %d: %s", sub, resp.Code, resp.Body.String())
		}
	}

	all := do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeNetworkInterfaces"}})
	if got := strings.Count(all.Body.String(), "<networkInterfaceId>"); got != 2 {
		t.Fatalf("unfiltered describe = %d ENIs, want 2: %s", got, all.Body.String())
	}

	filtered := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DescribeNetworkInterfaces"}, "Filter.1.Name": {"vpc-id"}, "Filter.1.Value.1": {vpcA},
	})

	if got := strings.Count(filtered.Body.String(), "<networkInterfaceId>"); got != 1 {
		t.Errorf("vpc-filtered describe = %d ENIs, want 1: %s", got, filtered.Body.String())
	}

	if !strings.Contains(filtered.Body.String(), vpcA) {
		t.Errorf("filtered result should belong to %s: %s", vpcA, filtered.Body.String())
	}
}

func TestDetachThenDeleteNetworkInterface(t *testing.T) {
	h := newFullHandler()
	vpcID, subnetID := mkVPCAndSubnet(t, h)

	do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateNatGateway"}, "SubnetId": {subnetID},
	})

	desc := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DescribeNetworkInterfaces"}, "Filter.1.Name": {"vpc-id"}, "Filter.1.Value.1": {vpcID},
	})

	eniID := between(desc.Body.String(), "<networkInterfaceId>", "</networkInterfaceId>")
	attachID := between(desc.Body.String(), "<attachmentId>", "</attachmentId>")

	if eniID == "" || attachID == "" {
		t.Fatalf("missing eni/attachment: %s", desc.Body.String())
	}

	detach := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DetachNetworkInterface"}, "AttachmentId": {attachID}, "Force": {"true"},
	})
	if detach.Code != http.StatusOK {
		t.Fatalf("DetachNetworkInterface = %d: %s", detach.Code, detach.Body.String())
	}

	del := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DeleteNetworkInterface"}, "NetworkInterfaceId": {eniID},
	})
	if del.Code != http.StatusOK {
		t.Fatalf("DeleteNetworkInterface after detach = %d: %s", del.Code, del.Body.String())
	}
}

func TestDetachUnknownAttachmentFails(t *testing.T) {
	h := newFullHandler()

	resp := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DetachNetworkInterface"}, "AttachmentId": {"eni-attach-nope"},
	})
	if resp.Code == http.StatusOK {
		t.Errorf("detaching an unknown attachment should fail, got 200: %s", resp.Body.String())
	}
}

// Real EC2 answers InvalidParameterValue for a filter it does not recognise.
// Silently returning nothing would tell a caller draining a VPC that there is
// nothing left to drain, and it would proceed to a delete that then fails with
// DependencyViolation.
func TestDescribeNetworkInterfacesRejectsUnknownFilter(t *testing.T) {
	h := newFullHandler()
	vpcID, subnetID := mkVPCAndSubnet(t, h)

	do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"CreateNatGateway"}, "SubnetId": {subnetID},
	})

	resp := do(t, h, http.MethodPost, "/", url.Values{
		"Action":        {"DescribeNetworkInterfaces"},
		"Filter.1.Name": {"attachment.instance-id"}, "Filter.1.Value.1": {"i-whatever"},
	})
	if resp.Code == http.StatusOK {
		t.Errorf("an unimplemented filter must not be silently ignored: %s", resp.Body.String())
	}

	// A supported filter still works.
	ok := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DescribeNetworkInterfaces"}, "Filter.1.Name": {"vpc-id"}, "Filter.1.Value.1": {vpcID},
	})
	if ok.Code != http.StatusOK {
		t.Errorf("supported filter = %d: %s", ok.Code, ok.Body.String())
	}
}
