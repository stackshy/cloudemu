package loadbalancer_test

import (
	"context"
	"net/http"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
)

// TestSDKGCPCrossProjectRefsResolveInRefProject checks a reference that names
// another project is not resolved against the request project's same-named
// resource (GLB-N4): a health check, an instance group or a URL map in p-b
// can't back a p-a resource.
func TestSDKGCPCrossProjectRefsResolveInRefProject(t *testing.T) {
	ctx := context.Background()
	c := newProjectLBClients(t)

	insertProjectStack(ctx, t, c, projA, 10)

	err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.bs.Insert(ctx, &computepb.InsertRegionBackendServiceRequest{Project: projA, Region: w9Region,
			BackendServiceResource: &computepb.BackendService{
				Name: ptrStr("cross"), Protocol: ptrStr("TCP"), LoadBalancingScheme: ptrStr("INTERNAL"),
				HealthChecks: []string{"projects/" + projB + "/global/healthChecks/hc"},
			}})
	})
	assertHTTPCode(t, err, http.StatusBadRequest)

	// p-a's own "hc" is used only by p-a's "bs", so once that is gone the
	// check deletes cleanly: no cross-project reference pinned it.
	if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.fr.Delete(ctx, &computepb.DeleteForwardingRuleRequest{Project: projA, Region: w9Region, ForwardingRule: "fr"})
	}); err != nil {
		t.Fatalf("delete fr: %v", err)
	}

	if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.bs.Delete(ctx, &computepb.DeleteRegionBackendServiceRequest{Project: projA, Region: w9Region, BackendService: "bs"})
	}); err != nil {
		t.Fatalf("delete bs: %v", err)
	}

	if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.hc.Delete(ctx, &computepb.DeleteHealthCheckRequest{Project: projA, HealthCheck: "hc"})
	}); err != nil {
		t.Fatalf("delete hc: %v", err)
	}

	err = callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.fr.Insert(ctx, &computepb.InsertForwardingRuleRequest{Project: projA, Region: w9Region,
			ForwardingRuleResource: &computepb.ForwardingRule{
				Name: ptrStr("fr2"), LoadBalancingScheme: ptrStr("INTERNAL"), AllPorts: ptrBool(true),
				BackendService: ptrStr("projects/" + projB + "/regions/" + w9Region + "/backendServices/rbs"),
			}})
	})
	assertHTTPCode(t, err, http.StatusBadRequest)
}

// TestSDKGCPCrossProjectHealthCheckNotPinned (GUARD) inserts a same-named health check
// in two projects and a backend service in p-b using p-b's check: p-a's check
// stays deletable.
func TestSDKGCPCrossProjectHealthCheckNotPinned(t *testing.T) {
	ctx := context.Background()
	c := newProjectLBClients(t)

	insertProjectStack(ctx, t, c, projB, 10)

	if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.hc.Insert(ctx, &computepb.InsertHealthCheckRequest{Project: projA, HealthCheckResource: &computepb.HealthCheck{
			Name: ptrStr("hc"), Type: ptrStr("TCP"), TcpHealthCheck: &computepb.TCPHealthCheck{Port: ptrI32(80)},
		}})
	}); err != nil {
		t.Fatalf("p-a hc insert: %v", err)
	}

	if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.hc.Delete(ctx, &computepb.DeleteHealthCheckRequest{Project: projA, HealthCheck: "hc"})
	}); err != nil {
		t.Fatalf("p-a hc delete: %v", err)
	}
}

// TestSDKGCPServiceAttachmentIAM drives the service attachment IAM verbs
// (405 before) through the gapic client.
func TestSDKGCPServiceAttachmentIAM(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	c := newServiceAttachmentsClient(t, ts)

	insertAttachment(ctx, t, ts, attachment("sa", "ACCEPT_AUTOMATIC"))

	role := "roles/compute.viewer"
	if _, err := c.SetIamPolicy(ctx, &computepb.SetIamPolicyServiceAttachmentRequest{
		Project: testProject, Region: testRegion, Resource: "sa",
		RegionSetPolicyRequestResource: &computepb.RegionSetPolicyRequest{Policy: &computepb.Policy{
			Bindings: []*computepb.Binding{{Role: &role, Members: []string{"user:a@example.com"}}},
		}},
	}); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	got, err := c.GetIamPolicy(ctx, &computepb.GetIamPolicyServiceAttachmentRequest{Project: testProject, Region: testRegion, Resource: "sa"})
	if err != nil || len(got.GetBindings()) != 1 {
		t.Fatalf("GetIamPolicy = %v, %v", got.GetBindings(), err)
	}

	_, err = c.GetIamPolicy(ctx, &computepb.GetIamPolicyServiceAttachmentRequest{Project: testProject, Region: testRegion, Resource: "nope"})
	assertHTTPCode(t, err, http.StatusNotFound)
}
