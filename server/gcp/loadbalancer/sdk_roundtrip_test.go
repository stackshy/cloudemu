package loadbalancer_test

import (
	"context"
	"net/http/httptest"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const testProject = "proj-1"

func ptrStr(s string) *string { return &s }

func newGCPLBServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	// Register Compute too so the shared /global/operations polling endpoint is
	// wired up — LB mutating ops return Operation envelopes the SDK polls there.
	// This also proves the backendServices / forwardingRules resource types
	// aren't shadowed by the compute (instances/operations/…) handler.
	srv := gcpserver.New(gcpserver.Drivers{LB: cloudP.LB, Compute: cloudP.GCE})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func TestSDKGCPBackendServiceRoundTrip(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewBackendServicesRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewBackendServicesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insertOp, err := client.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: testProject,
		BackendServiceResource: &computepb.BackendService{
			Name:     ptrStr("web-backend"),
			Protocol: ptrStr("HTTP"),
			Port:     func() *int32 { p := int32(80); return &p }(),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetBackendServiceRequest{
		Project:        testProject,
		BackendService: "web-backend",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "web-backend" {
		t.Fatalf("name = %q, want web-backend", got.GetName())
	}

	if got.GetProtocol() != "HTTP" {
		t.Fatalf("protocol = %q, want HTTP", got.GetProtocol())
	}

	// List.
	var names []string

	it := client.List(ctx, &computepb.ListBackendServicesRequest{Project: testProject})

	for {
		bs, iErr := it.Next()
		if iErr == iterator.Done {
			break
		}

		if iErr != nil {
			t.Fatalf("List: %v", iErr)
		}

		names = append(names, bs.GetName())
	}

	if len(names) != 1 || names[0] != "web-backend" {
		t.Fatalf("list = %v, want [web-backend]", names)
	}

	delOp, err := client.Delete(ctx, &computepb.DeleteBackendServiceRequest{
		Project:        testProject,
		BackendService: "web-backend",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("Delete wait: %v", err)
	}

	_, err = client.Get(ctx, &computepb.GetBackendServiceRequest{
		Project:        testProject,
		BackendService: "web-backend",
	})
	if err == nil {
		t.Fatal("Get after delete: want error, got nil")
	}
}

func TestSDKGCPForwardingRuleRoundTrip(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	bsClient, err := gcpcompute.NewBackendServicesRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewBackendServicesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = bsClient.Close() })

	frClient, err := gcpcompute.NewGlobalForwardingRulesRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewGlobalForwardingRulesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = frClient.Close() })

	// Backing backend service the forwarding rule references.
	bsOp, err := bsClient.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: testProject,
		BackendServiceResource: &computepb.BackendService{
			Name:     ptrStr("fr-backend"),
			Protocol: ptrStr("TCP"),
		},
	})
	if err != nil {
		t.Fatalf("BackendService Insert: %v", err)
	}

	if err := bsOp.Wait(ctx); err != nil {
		t.Fatalf("BackendService Insert wait: %v", err)
	}

	frOp, err := frClient.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
		Project: testProject,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name:           ptrStr("my-lb"),
			IPProtocol:     ptrStr("TCP"),
			PortRange:      ptrStr("80"),
			BackendService: ptrStr("projects/" + testProject + "/global/backendServices/fr-backend"),
		},
	})
	if err != nil {
		t.Fatalf("ForwardingRule Insert: %v", err)
	}

	if err := frOp.Wait(ctx); err != nil {
		t.Fatalf("ForwardingRule Insert wait: %v", err)
	}

	got, err := frClient.Get(ctx, &computepb.GetGlobalForwardingRuleRequest{
		Project:        testProject,
		ForwardingRule: "my-lb",
	})
	if err != nil {
		t.Fatalf("ForwardingRule Get: %v", err)
	}

	if got.GetName() != "my-lb" {
		t.Fatalf("name = %q, want my-lb", got.GetName())
	}

	if got.GetPortRange() != "80" {
		t.Fatalf("portRange = %q, want 80", got.GetPortRange())
	}

	if got.GetBackendService() == "" {
		t.Fatal("expected forwarding rule to reflect its backend service")
	}

	delOp, err := frClient.Delete(ctx, &computepb.DeleteGlobalForwardingRuleRequest{
		Project:        testProject,
		ForwardingRule: "my-lb",
	})
	if err != nil {
		t.Fatalf("ForwardingRule Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("ForwardingRule Delete wait: %v", err)
	}

	_, err = frClient.Get(ctx, &computepb.GetGlobalForwardingRuleRequest{
		Project:        testProject,
		ForwardingRule: "my-lb",
	})
	if err == nil {
		t.Fatal("Get after delete: want error, got nil")
	}
}
