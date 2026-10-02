package loadbalancer_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// wantBadRequest fails unless err is a googleapi 400 whose message names field.
func wantBadRequest(t *testing.T, err error, field string) {
	t.Helper()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != http.StatusBadRequest {
		t.Fatalf("err = %v, want 400", err)
	}

	if !strings.Contains(gerr.Message, "'resource."+field+"'") {
		t.Errorf("message = %q, want it to name resource.%s", gerr.Message, field)
	}
}

// TestSDKGCPForwardingRulePSCRequiresNetwork: a PSC consumer endpoint is an
// internal address in the consumer's VPC, so GCP refuses one with no network,
// for both a service-attachment and a Google APIs bundle target.
func TestSDKGCPForwardingRulePSCRequiresNetwork(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	regional := newRegionalForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))
	global := newForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	_, err := regional.Insert(ctx, &computepb.InsertForwardingRuleRequest{
		Project: testProject, Region: testRegion,
		ForwardingRuleResource: &computepb.ForwardingRule{Name: ptrStr("psc-nonet"), Target: ptrStr(pscAttachment)},
	})
	wantBadRequest(t, err, "network")

	_, err = global.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
		Project: testProject,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name: ptrStr("apisnonet"), IPAddress: ptrStr("10.3.0.9"), Target: ptrStr("all-apis"),
		},
	})
	wantBadRequest(t, err, "network")
}

// TestSDKGCPForwardingRulePSCRejectsScheme: a PSC rule's loadBalancingScheme
// must be empty; an explicit EXTERNAL (or INTERNAL) is refused.
func TestSDKGCPForwardingRulePSCRejectsScheme(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	regional := newRegionalForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))
	global := newForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	_, err := regional.Insert(ctx, &computepb.InsertForwardingRuleRequest{
		Project: testProject, Region: testRegion,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name: ptrStr("psc-ext"), Network: ptrStr(pscNetwork), Target: ptrStr(pscAttachment),
			LoadBalancingScheme: ptrStr("EXTERNAL"),
		},
	})
	wantBadRequest(t, err, "loadBalancingScheme")

	_, err = global.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
		Project: testProject,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name: ptrStr("apisint"), Network: ptrStr(pscNetwork), IPAddress: ptrStr("10.3.0.9"),
			Target: ptrStr("vpc-sc"), LoadBalancingScheme: ptrStr("INTERNAL"),
		},
	})
	wantBadRequest(t, err, "loadBalancingScheme")
}

// TestSDKGCPForwardingRulePSCInternalAddress: a PSC rule sent without an
// IPAddress gets an internal (10.x) address, never an external 34.x one.
func TestSDKGCPForwardingRulePSCInternalAddress(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	client := newRegionalForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	insertAttachment(ctx, t, ts, attachment("producer-sa", "ACCEPT_AUTOMATIC"))

	op, err := client.Insert(ctx, &computepb.InsertForwardingRuleRequest{
		Project: testProject, Region: testRegion,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name: ptrStr("psc-noip"), Network: ptrStr(pscNetwork), Target: ptrStr(pscAttachment),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetForwardingRuleRequest{Project: testProject, Region: testRegion, ForwardingRule: "psc-noip"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !strings.HasPrefix(got.GetIPAddress(), "10.") {
		t.Errorf("IPAddress = %q, want an internal 10.x address for a PSC endpoint", got.GetIPAddress())
	}
}
