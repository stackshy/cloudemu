package loadbalancer_test

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

const (
	projA = "p-a"
	projB = "p-b"
)

type projectLBClients struct {
	hc  *gcpcompute.HealthChecksClient
	bs  *gcpcompute.RegionBackendServicesClient
	fr  *gcpcompute.ForwardingRulesClient
	rhc *gcpcompute.RegionHealthChecksClient
}

func newProjectLBClients(t *testing.T) projectLBClients {
	t.Helper()

	ts := newGCPLBServer(t)
	ctx := context.Background()
	opts := []option.ClientOption{option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client())}

	bs, err := gcpcompute.NewRegionBackendServicesRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("backend services client: %v", err)
	}

	rhc, err := gcpcompute.NewRegionHealthChecksRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("region health checks client: %v", err)
	}

	t.Cleanup(func() { _ = bs.Close(); _ = rhc.Close() })

	return projectLBClients{
		hc:  newHealthChecksClient(t, ts),
		bs:  bs,
		fr:  newRegionalForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client())),
		rhc: rhc,
	}
}

// insertProjectStack creates health checks "hc" (global and regional), the
// regional backend service "bs" and the internal forwarding rule "fr" in
// project, each tagged with the project in a mutable field.
func insertProjectStack(ctx context.Context, t *testing.T, c projectLBClients, project string, timeout int32) {
	t.Helper()

	steps := []struct {
		label string
		call  func() (*gcpcompute.Operation, error)
	}{
		{"global hc", func() (*gcpcompute.Operation, error) {
			return c.hc.Insert(ctx, &computepb.InsertHealthCheckRequest{Project: project, HealthCheckResource: &computepb.HealthCheck{
				Name: ptrStr("hc"), Type: ptrStr("TCP"), Description: ptrStr(project),
				TcpHealthCheck: &computepb.TCPHealthCheck{Port: ptrI32(80)},
			}})
		}},
		{"region hc", func() (*gcpcompute.Operation, error) {
			return c.rhc.Insert(ctx, &computepb.InsertRegionHealthCheckRequest{Project: project, Region: w9Region,
				HealthCheckResource: &computepb.HealthCheck{Name: ptrStr("hc"), Type: ptrStr("TCP"), Description: ptrStr(project)}})
		}},
		{"bs", func() (*gcpcompute.Operation, error) {
			return c.bs.Insert(ctx, &computepb.InsertRegionBackendServiceRequest{Project: project, Region: w9Region,
				BackendServiceResource: &computepb.BackendService{
					Name: ptrStr("bs"), Protocol: ptrStr("TCP"), LoadBalancingScheme: ptrStr("INTERNAL"),
					TimeoutSec: ptrI32(timeout), HealthChecks: []string{"projects/" + project + "/global/healthChecks/hc"},
				}})
		}},
		{"fr", func() (*gcpcompute.Operation, error) {
			return c.fr.Insert(ctx, &computepb.InsertForwardingRuleRequest{Project: project, Region: w9Region,
				ForwardingRuleResource: &computepb.ForwardingRule{
					Name: ptrStr("fr"), LoadBalancingScheme: ptrStr("INTERNAL"), AllPorts: ptrBool(true),
					BackendService: ptrStr("projects/" + project + "/regions/" + w9Region + "/backendServices/bs"),
					Description:    ptrStr(project),
				}})
		}},
	}

	for _, s := range steps {
		if err := callOp(ctx, s.call); err != nil {
			t.Fatalf("%s insert %s: %v", project, s.label, err)
		}
	}
}

// TestSDKGCPLBSameNamesInTwoProjects inserts identically named health checks,
// backend services and forwarding rules in two projects and checks get, list,
// patch, setLabels and delete in one never reach the other.
func TestSDKGCPLBSameNamesInTwoProjects(t *testing.T) {
	ctx := context.Background()
	c := newProjectLBClients(t)

	insertProjectStack(ctx, t, c, projA, 10)
	insertProjectStack(ctx, t, c, projB, 20)

	assertProjectStack(ctx, t, c, projA, projA, 10, nil)
	assertProjectStack(ctx, t, c, projB, projB, 20, nil)

	mutations := []struct {
		label string
		call  func() (*gcpcompute.Operation, error)
	}{
		{"patch bs", func() (*gcpcompute.Operation, error) {
			return c.bs.Patch(ctx, &computepb.PatchRegionBackendServiceRequest{Project: projB, Region: w9Region,
				BackendService: "bs", BackendServiceResource: &computepb.BackendService{TimeoutSec: ptrI32(30)}})
		}},
		{"patch hc", func() (*gcpcompute.Operation, error) {
			return c.hc.Patch(ctx, &computepb.PatchHealthCheckRequest{Project: projB, HealthCheck: "hc",
				HealthCheckResource: &computepb.HealthCheck{Description: ptrStr("patched")}})
		}},
		{"setLabels fr", func() (*gcpcompute.Operation, error) {
			return c.fr.SetLabels(ctx, &computepb.SetLabelsForwardingRuleRequest{Project: projB, Region: w9Region,
				Resource: "fr", RegionSetLabelsRequestResource: &computepb.RegionSetLabelsRequest{
					Labels: map[string]string{"env": "b"}, LabelFingerprint: ptrStr(emptyLabelFP),
				}})
		}},
	}

	for _, m := range mutations {
		if err := callOp(ctx, m.call); err != nil {
			t.Fatalf("%s in %s: %v", m.label, projB, err)
		}
	}

	assertProjectStack(ctx, t, c, projA, projA, 10, nil)

	assertProjectStack(ctx, t, c, projB, "patched", 30, map[string]string{"env": "b"})

	deleteProjectStack(ctx, t, c, projB)
	assertProjectStack(ctx, t, c, projA, projA, 10, nil)

	_, err := c.fr.Get(ctx, &computepb.GetForwardingRuleRequest{Project: projB, Region: w9Region, ForwardingRule: "fr"})
	assertHTTPCode(t, err, 404)

	_, err = c.bs.Get(ctx, &computepb.GetRegionBackendServiceRequest{Project: projB, Region: w9Region, BackendService: "bs"})
	assertHTTPCode(t, err, 404)

	_, err = c.hc.Get(ctx, &computepb.GetHealthCheckRequest{Project: projB, HealthCheck: "hc"})
	assertHTTPCode(t, err, 404)
}

// TestSDKGCPLBReferencesResolveInRequestProject checks a backend service can't
// name a health check that exists only in another project, and that the
// health-check in-use guard only counts backend services of its own project.
func TestSDKGCPLBReferencesResolveInRequestProject(t *testing.T) {
	ctx := context.Background()
	c := newProjectLBClients(t)

	insertProjectStack(ctx, t, c, projA, 10)

	err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.bs.Insert(ctx, &computepb.InsertRegionBackendServiceRequest{Project: projB, Region: w9Region,
			BackendServiceResource: &computepb.BackendService{
				Name: ptrStr("bs"), Protocol: ptrStr("TCP"), LoadBalancingScheme: ptrStr("INTERNAL"),
				HealthChecks: []string{"projects/" + projB + "/global/healthChecks/hc"},
			}})
	})
	assertHTTPCode(t, err, 400)

	if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.hc.Insert(ctx, &computepb.InsertHealthCheckRequest{Project: projB, HealthCheckResource: &computepb.HealthCheck{
			Name: ptrStr("hc"), Type: ptrStr("TCP"), TcpHealthCheck: &computepb.TCPHealthCheck{Port: ptrI32(80)},
		}})
	}); err != nil {
		t.Fatalf("%s hc insert: %v", projB, err)
	}

	// p-a's backend service uses p-a's "hc"; p-b's same-named check is free.
	if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.hc.Delete(ctx, &computepb.DeleteHealthCheckRequest{Project: projB, HealthCheck: "hc"})
	}); err != nil {
		t.Fatalf("%s hc delete: %v", projB, err)
	}

	err = callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.hc.Delete(ctx, &computepb.DeleteHealthCheckRequest{Project: projA, HealthCheck: "hc"})
	})
	assertResourceInUse(t, err)
}

func getProjectHC(ctx context.Context, t *testing.T, c projectLBClients, project string) *computepb.HealthCheck {
	t.Helper()

	got, err := c.hc.Get(ctx, &computepb.GetHealthCheckRequest{Project: project, HealthCheck: "hc"})
	if err != nil {
		t.Fatalf("%s hc get: %v", project, err)
	}

	return got
}

// assertProjectStack checks each resource of project is the one created there
// and that every list shows exactly one item.
func assertProjectStack(ctx context.Context, t *testing.T, c projectLBClients, project, hcDesc string, timeout int32,
	labels map[string]string,
) {
	t.Helper()

	if got := getProjectHC(ctx, t, c, project); got.GetDescription() != hcDesc {
		t.Errorf("%s hc description = %q, want %q", project, got.GetDescription(), hcDesc)
	}

	rhc, err := c.rhc.Get(ctx, &computepb.GetRegionHealthCheckRequest{Project: project, Region: w9Region, HealthCheck: "hc"})
	if err != nil || rhc.GetDescription() != project {
		t.Errorf("%s region hc = %q, %v", project, rhc.GetDescription(), err)
	}

	bs, err := c.bs.Get(ctx, &computepb.GetRegionBackendServiceRequest{Project: project, Region: w9Region, BackendService: "bs"})
	if err != nil || bs.GetTimeoutSec() != timeout {
		t.Errorf("%s bs timeoutSec = %d, %v; want %d", project, bs.GetTimeoutSec(), err, timeout)
	}

	fr, err := c.fr.Get(ctx, &computepb.GetForwardingRuleRequest{Project: project, Region: w9Region, ForwardingRule: "fr"})
	if err != nil || fr.GetDescription() != project || len(fr.GetLabels()) != len(labels) {
		t.Errorf("%s fr description = %q labels = %v, %v; want labels %v", project, fr.GetDescription(), fr.GetLabels(), err, labels)
	}

	assertProjectListCounts(ctx, t, c, project)
}

func assertProjectListCounts(ctx context.Context, t *testing.T, c projectLBClients, project string) {
	t.Helper()

	counts := map[string]int{}

	hcs := c.hc.List(ctx, &computepb.ListHealthChecksRequest{Project: project})
	for _, err := hcs.Next(); err == nil; _, err = hcs.Next() {
		counts["hc"]++
	}

	rhcs := c.rhc.List(ctx, &computepb.ListRegionHealthChecksRequest{Project: project, Region: w9Region})
	for _, err := rhcs.Next(); err == nil; _, err = rhcs.Next() {
		counts["region hc"]++
	}

	bss := c.bs.List(ctx, &computepb.ListRegionBackendServicesRequest{Project: project, Region: w9Region})
	for _, err := bss.Next(); err == nil; _, err = bss.Next() {
		counts["bs"]++
	}

	frs := c.fr.List(ctx, &computepb.ListForwardingRulesRequest{Project: project, Region: w9Region})
	for _, err := frs.Next(); err == nil; _, err = frs.Next() {
		counts["fr"]++
	}

	for _, k := range []string{"hc", "region hc", "bs", "fr"} {
		if counts[k] != 1 {
			t.Errorf("%s list %s = %d items, want 1", project, k, counts[k])
		}
	}
}

func deleteProjectStack(ctx context.Context, t *testing.T, c projectLBClients, project string) {
	t.Helper()

	steps := []struct {
		label string
		call  func() (*gcpcompute.Operation, error)
	}{
		{"fr", func() (*gcpcompute.Operation, error) {
			return c.fr.Delete(ctx, &computepb.DeleteForwardingRuleRequest{Project: project, Region: w9Region, ForwardingRule: "fr"})
		}},
		{"bs", func() (*gcpcompute.Operation, error) {
			return c.bs.Delete(ctx, &computepb.DeleteRegionBackendServiceRequest{Project: project, Region: w9Region, BackendService: "bs"})
		}},
		{"hc", func() (*gcpcompute.Operation, error) {
			return c.hc.Delete(ctx, &computepb.DeleteHealthCheckRequest{Project: project, HealthCheck: "hc"})
		}},
		{"region hc", func() (*gcpcompute.Operation, error) {
			return c.rhc.Delete(ctx, &computepb.DeleteRegionHealthCheckRequest{Project: project, Region: w9Region, HealthCheck: "hc"})
		}},
	}

	for _, s := range steps {
		if err := callOp(ctx, s.call); err != nil {
			t.Fatalf("%s delete %s: %v", project, s.label, err)
		}
	}
}
