package loadbalancer_test

import (
	"context"
	"net"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

func newForwardingRulesClient(t *testing.T, url string, httpc option.ClientOption) *gcpcompute.GlobalForwardingRulesClient {
	t.Helper()

	client, err := gcpcompute.NewGlobalForwardingRulesRESTClient(context.Background(),
		option.WithEndpoint(url), option.WithoutAuthentication(), httpc)
	if err != nil {
		t.Fatalf("NewGlobalForwardingRulesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// TestSDKGCPForwardingRuleNoBackendFields reproduces three findings on
// forwardingRules.get when the rule has no linked backend service:
//   - [HIGH] portRange lost;
//   - [MEDIUM] IPAddress returned as a hostname, not an IPv4/IPv6 string;
//   - [MEDIUM] loadBalancingScheme collapsed EXTERNAL_MANAGED → EXTERNAL.
//
// It also covers the #643-deferred creationTimestamp.
func TestSDKGCPForwardingRuleNoBackendFields(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client := newForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	op, err := client.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
		Project: testProject,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name:                ptrStr("edge-fr"),
			IPProtocol:          ptrStr("TCP"),
			PortRange:           ptrStr("443"),
			LoadBalancingScheme: ptrStr("EXTERNAL_MANAGED"),
			// No backendService: exercises the field-round-trip path.
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetGlobalForwardingRuleRequest{Project: testProject, ForwardingRule: "edge-fr"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetPortRange() != "443" {
		t.Errorf("portRange = %q, want 443 (lost without a backend service)", got.GetPortRange())
	}

	if got.GetLoadBalancingScheme() != "EXTERNAL_MANAGED" {
		t.Errorf("loadBalancingScheme = %q, want EXTERNAL_MANAGED (collapsed)", got.GetLoadBalancingScheme())
	}

	if ip := net.ParseIP(got.GetIPAddress()); ip == nil {
		t.Errorf("IPAddress = %q, want a parseable IP, not a hostname", got.GetIPAddress())
	}

	if got.GetCreationTimestamp() == "" {
		t.Error("creationTimestamp empty")
	}
}

// TestSDKGCPForwardingRuleDuplicate reproduces the #643-deferred portion: a
// duplicate-name insert must fail with 409, not silently succeed.
func TestSDKGCPForwardingRuleDuplicate(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client := newForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	insert := func() error {
		op, err := client.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
			Project: testProject,
			ForwardingRuleResource: &computepb.ForwardingRule{
				Name:      ptrStr("dup-fr"),
				PortRange: ptrStr("80"),
			},
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	}

	if err := insert(); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	if err := insert(); err == nil {
		t.Fatal("duplicate forwarding rule insert: want error, got nil")
	}
}
