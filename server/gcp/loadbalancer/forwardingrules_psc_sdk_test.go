package loadbalancer_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	pscNetwork    = "projects/" + testProject + "/global/networks/consumer-vpc"
	pscSubnetwork = "projects/" + testProject + "/regions/" + testRegion + "/subnetworks/consumer-subnet"
	pscAttachment = "projects/producer-proj/regions/" + testRegion + "/serviceAttachments/producer-sa"
	pscAddress    = "projects/" + testProject + "/regions/" + testRegion + "/addresses/psc-endpoint-ip"
	pscAccepted   = "ACCEPTED"
)

func newRegionalForwardingRulesClient(t *testing.T, url string, httpc option.ClientOption) *gcpcompute.ForwardingRulesClient {
	t.Helper()

	client, err := gcpcompute.NewForwardingRulesRESTClient(context.Background(),
		option.WithEndpoint(url), option.WithoutAuthentication(), httpc)
	if err != nil {
		t.Fatalf("NewForwardingRulesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// assertPSCRule checks the fields a PSC consumer rule must carry on Get.
func assertPSCRule(t *testing.T, got *computepb.ForwardingRule, wantIP, wantTarget string) {
	t.Helper()

	if got.GetNetwork() != pscNetwork {
		t.Errorf("network = %q, want %q (dropped on insert)", got.GetNetwork(), pscNetwork)
	}

	if got.GetIPAddress() != wantIP {
		t.Errorf("IPAddress = %q, want %q", got.GetIPAddress(), wantIP)
	}

	if got.GetTarget() != wantTarget {
		t.Errorf("target = %q, want %q", got.GetTarget(), wantTarget)
	}

	if got.LoadBalancingScheme != nil {
		t.Errorf("loadBalancingScheme = %q, want unset for a PSC rule", got.GetLoadBalancingScheme())
	}

	if got.GetPscConnectionStatus() != pscAccepted {
		t.Errorf("pscConnectionStatus = %q, want %s", got.GetPscConnectionStatus(), pscAccepted)
	}

	if got.GetPscConnectionId() == 0 {
		t.Error("pscConnectionId = 0, want a non-zero connection id")
	}
}

// TestSDKGCPForwardingRulePSCServiceAttachment drives a regional Private
// Service Connect consumer rule (target = a producer's serviceAttachments
// self-link) through the real ForwardingRulesRESTClient. The producer
// attachment must exist (GCP refuses an endpoint for one it cannot find); it
// accepts automatically, so the connection is ACCEPTED.
func TestSDKGCPForwardingRulePSCServiceAttachment(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	client := newRegionalForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	insertAttachment(ctx, t, ts, attachment("producer-sa", "ACCEPT_AUTOMATIC"))

	op, err := client.Insert(ctx, &computepb.InsertForwardingRuleRequest{
		Project: testProject,
		Region:  testRegion,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name:       ptrStr("psc-endpoint"),
			Network:    ptrStr(pscNetwork),
			Subnetwork: ptrStr(pscSubnetwork),
			IPAddress:  ptrStr(pscAddress),
			Target:     ptrStr(pscAttachment),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	get := &computepb.GetForwardingRuleRequest{Project: testProject, Region: testRegion, ForwardingRule: "psc-endpoint"}

	got, err := client.Get(ctx, get)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertPSCRule(t, got, pscAddress, pscAttachment)

	if got.GetSubnetwork() != pscSubnetwork {
		t.Errorf("subnetwork = %q, want %q", got.GetSubnetwork(), pscSubnetwork)
	}

	again, err := client.Get(ctx, get)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if again.GetPscConnectionId() != got.GetPscConnectionId() {
		t.Errorf("pscConnectionId changed between reads: %d then %d", got.GetPscConnectionId(), again.GetPscConnectionId())
	}
}

// TestSDKGCPForwardingRulePSCGoogleAPIs drives a global PSC endpoint for
// Google APIs (target "all-apis") through the real
// GlobalForwardingRulesRESTClient, with a literal IP address.
func TestSDKGCPForwardingRulePSCGoogleAPIs(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	client := newForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	const endpointIP = "10.3.0.5"

	op, err := client.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
		Project: testProject,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name:      ptrStr("pscgoogleapis"),
			Network:   ptrStr(pscNetwork),
			IPAddress: ptrStr(endpointIP),
			Target:    ptrStr("all-apis"),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetGlobalForwardingRuleRequest{Project: testProject, ForwardingRule: "pscgoogleapis"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertPSCRule(t, got, endpointIP, "all-apis")
}

// TestSDKGCPForwardingRuleGoogleAPIsBundleRegionalRejected: all-apis / vpc-sc
// are only valid targets on a global forwarding rule.
func TestSDKGCPForwardingRuleGoogleAPIsBundleRegionalRejected(t *testing.T) {
	ts := newGCPLBServer(t)
	client := newRegionalForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	_, err := client.Insert(context.Background(), &computepb.InsertForwardingRuleRequest{
		Project: testProject,
		Region:  testRegion,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name:    ptrStr("regional-vpcsc"),
			Network: ptrStr(pscNetwork),
			Target:  ptrStr("vpc-sc"),
		},
	})

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != http.StatusBadRequest {
		t.Fatalf("Insert regional vpc-sc: err = %v, want 400", err)
	}
}

// TestSDKGCPForwardingRuleExternalKeepsNetwork: a non-PSC rule sent without a
// scheme still defaults to EXTERNAL, and now keeps the network it was sent.
func TestSDKGCPForwardingRuleExternalKeepsNetwork(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	client := newRegionalForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	op, err := client.Insert(ctx, &computepb.InsertForwardingRuleRequest{
		Project: testProject,
		Region:  testRegion,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name:       ptrStr("plain-fr"),
			Network:    ptrStr(pscNetwork),
			IPProtocol: ptrStr("TCP"),
			PortRange:  ptrStr("80"),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetForwardingRuleRequest{Project: testProject, Region: testRegion, ForwardingRule: "plain-fr"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetLoadBalancingScheme() != "EXTERNAL" {
		t.Errorf("loadBalancingScheme = %q, want EXTERNAL default", got.GetLoadBalancingScheme())
	}

	if got.GetNetwork() != pscNetwork {
		t.Errorf("network = %q, want %q", got.GetNetwork(), pscNetwork)
	}

	if got.PscConnectionStatus != nil || got.PscConnectionId != nil {
		t.Errorf("non-PSC rule reports psc fields: status=%q id=%d", got.GetPscConnectionStatus(), got.GetPscConnectionId())
	}
}
