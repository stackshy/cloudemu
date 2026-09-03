package loadbalancer_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// newHealthChecksClient builds a HealthChecks REST client pointed at ts.
func newHealthChecksClient(t *testing.T, ts *httptest.Server) *gcpcompute.HealthChecksClient {
	t.Helper()

	client, err := gcpcompute.NewHealthChecksRESTClient(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewHealthChecksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// insertHealthCheck creates a global HTTP health check named name.
func insertHealthCheck(ctx context.Context, t *testing.T, ts *httptest.Server, name string) {
	t.Helper()

	client := newHealthChecksClient(t, ts)

	op, err := client.Insert(ctx, &computepb.InsertHealthCheckRequest{
		Project:             testProject,
		HealthCheckResource: &computepb.HealthCheck{Name: ptrStr(name), Type: ptrStr("HTTP")},
	})
	if err != nil {
		t.Fatalf("HealthCheck Insert %s: %v", name, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("HealthCheck Insert %s wait: %v", name, err)
	}
}

// hcRef renders a global health-check self-link reference.
func hcRef(name string) string {
	return "projects/" + testProject + "/global/healthChecks/" + name
}

// insertBSWithHC creates a backend service referencing the given health checks.
func insertBSWithHC(ctx context.Context, t *testing.T, c *gcpcompute.BackendServicesClient, name string, hcs ...string) {
	t.Helper()

	op, err := c.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: testProject,
		BackendServiceResource: &computepb.BackendService{
			Name:         ptrStr(name),
			Protocol:     ptrStr("HTTP"),
			HealthChecks: hcs,
		},
	})
	if err != nil {
		t.Fatalf("BackendService Insert %s: %v", name, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("BackendService Insert %s wait: %v", name, err)
	}
}

// assertResourceInUse fails unless err is a 400 resourceInUseByAnotherResource.
func assertResourceInUse(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("delete of in-use resource: want error, got nil")
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("error = %v, want a googleapi.Error", err)
	}

	if gerr.Code != 400 {
		t.Errorf("error code = %d, want 400 (%v)", gerr.Code, err)
	}

	reason := gerr.Message
	for _, item := range gerr.Errors {
		if item.Reason != "" {
			reason = item.Reason
		}
	}

	if reason != "resourceInUseByAnotherResource" {
		t.Errorf("reason = %q, want resourceInUseByAnotherResource (%v)", reason, err)
	}
}

// TestSDKGCPBackendServiceInUseByForwardingRule covers B1(a): a backend service
// still fronted by a forwarding rule cannot be deleted; once the forwarding rule
// is gone, the backend service deletes cleanly.
func TestSDKGCPBackendServiceInUseByForwardingRule(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	bs := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))
	fr := newForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	insertHealthCheck(ctx, t, ts, "hc-a")
	insertBSWithHC(ctx, t, bs, "bs-a", hcRef("hc-a"))

	frOp, err := fr.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
		Project: testProject,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name:           ptrStr("fr-a"),
			IPProtocol:     ptrStr("TCP"),
			PortRange:      ptrStr("80"),
			BackendService: ptrStr("projects/" + testProject + "/global/backendServices/bs-a"),
		},
	})
	if err != nil {
		t.Fatalf("ForwardingRule Insert: %v", err)
	}

	if err := frOp.Wait(ctx); err != nil {
		t.Fatalf("ForwardingRule Insert wait: %v", err)
	}

	// Delete while referenced → rejected.
	_, err = bs.Delete(ctx, &computepb.DeleteBackendServiceRequest{Project: testProject, BackendService: "bs-a"})
	assertResourceInUse(t, err)

	// Remove the forwarding rule, then the backend service deletes.
	delFR, err := fr.Delete(ctx, &computepb.DeleteGlobalForwardingRuleRequest{Project: testProject, ForwardingRule: "fr-a"})
	if err != nil {
		t.Fatalf("ForwardingRule Delete: %v", err)
	}

	if err := delFR.Wait(ctx); err != nil {
		t.Fatalf("ForwardingRule Delete wait: %v", err)
	}

	delBS, err := bs.Delete(ctx, &computepb.DeleteBackendServiceRequest{Project: testProject, BackendService: "bs-a"})
	if err != nil {
		t.Fatalf("BackendService Delete after unreference: %v", err)
	}

	if err := delBS.Wait(ctx); err != nil {
		t.Fatalf("BackendService Delete wait: %v", err)
	}
}

// TestSDKGCPBackendServiceInUseByURLMap covers the url-map arm of B1(a): a
// backend service named as a url-map's defaultService cannot be deleted until
// the url-map is gone.
func TestSDKGCPBackendServiceInUseByURLMap(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	bs := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	um, err := gcpcompute.NewUrlMapsRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewUrlMapsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = um.Close() })

	insertBS(ctx, t, bs, "bs-um")

	umOp, err := um.Insert(ctx, &computepb.InsertUrlMapRequest{
		Project: testProject,
		UrlMapResource: &computepb.UrlMap{
			Name:           ptrStr("map-1"),
			DefaultService: ptrStr("projects/" + testProject + "/global/backendServices/bs-um"),
		},
	})
	if err != nil {
		t.Fatalf("UrlMap Insert: %v", err)
	}

	if err := umOp.Wait(ctx); err != nil {
		t.Fatalf("UrlMap Insert wait: %v", err)
	}

	_, err = bs.Delete(ctx, &computepb.DeleteBackendServiceRequest{Project: testProject, BackendService: "bs-um"})
	assertResourceInUse(t, err)

	delUM, err := um.Delete(ctx, &computepb.DeleteUrlMapRequest{Project: testProject, UrlMap: "map-1"})
	if err != nil {
		t.Fatalf("UrlMap Delete: %v", err)
	}

	if err := delUM.Wait(ctx); err != nil {
		t.Fatalf("UrlMap Delete wait: %v", err)
	}

	delBS, err := bs.Delete(ctx, &computepb.DeleteBackendServiceRequest{Project: testProject, BackendService: "bs-um"})
	if err != nil {
		t.Fatalf("BackendService Delete after url-map gone: %v", err)
	}

	if err := delBS.Wait(ctx); err != nil {
		t.Fatalf("BackendService Delete wait: %v", err)
	}
}

// TestSDKGCPHealthCheckInUse covers B1(b): a health check referenced by a
// backend service cannot be deleted; once the backend service is gone, it
// deletes cleanly.
func TestSDKGCPHealthCheckInUse(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	bs := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))
	hc := newHealthChecksClient(t, ts)

	insertHealthCheck(ctx, t, ts, "hc-b")
	insertBSWithHC(ctx, t, bs, "bs-b", hcRef("hc-b"))

	// Referenced → delete rejected.
	_, err := hc.Delete(ctx, &computepb.DeleteHealthCheckRequest{Project: testProject, HealthCheck: "hc-b"})
	assertResourceInUse(t, err)

	// Drop the referencing backend service, then the health check deletes.
	delBS, err := bs.Delete(ctx, &computepb.DeleteBackendServiceRequest{Project: testProject, BackendService: "bs-b"})
	if err != nil {
		t.Fatalf("BackendService Delete: %v", err)
	}

	if err := delBS.Wait(ctx); err != nil {
		t.Fatalf("BackendService Delete wait: %v", err)
	}

	delHC, err := hc.Delete(ctx, &computepb.DeleteHealthCheckRequest{Project: testProject, HealthCheck: "hc-b"})
	if err != nil {
		t.Fatalf("HealthCheck Delete after unreference: %v", err)
	}

	if err := delHC.Wait(ctx); err != nil {
		t.Fatalf("HealthCheck Delete wait: %v", err)
	}
}

// TestSDKGCPBackendServiceHealthCheckRefValidation covers B2(c): a backend
// service whose healthChecks[] names a missing health check is rejected, while
// one pointing at an existing health check succeeds.
func TestSDKGCPBackendServiceHealthCheckRefValidation(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	bs := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	// Dangling reference → rejected.
	badOp, err := bs.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: testProject,
		BackendServiceResource: &computepb.BackendService{
			Name:         ptrStr("bs-bad"),
			Protocol:     ptrStr("HTTP"),
			HealthChecks: []string{hcRef("ghost-hc")},
		},
	})
	if err == nil {
		err = badOp.Wait(ctx)
	}

	if err == nil {
		t.Fatal("insert referencing missing health check: want error, got nil")
	}

	if !strings.Contains(err.Error(), "healthChecks") {
		t.Errorf("error = %v, want a healthChecks[] validation error", err)
	}

	// Existing reference → accepted.
	insertHealthCheck(ctx, t, ts, "real-hc")
	insertBSWithHC(ctx, t, bs, "bs-good", hcRef("real-hc"))

	got, err := bs.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "bs-good"})
	if err != nil {
		t.Fatalf("Get bs-good: %v", err)
	}

	if len(got.GetHealthChecks()) != 1 {
		t.Errorf("healthChecks = %v, want one entry", got.GetHealthChecks())
	}
}

// TestSDKGCPBackendServiceDeleteNotInUse covers B1(d) no-regression: a backend
// service with no dependents still deletes without a spurious in-use rejection.
func TestSDKGCPBackendServiceDeleteNotInUse(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	bs := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))
	insertBS(ctx, t, bs, "bs-free")

	del, err := bs.Delete(ctx, &computepb.DeleteBackendServiceRequest{Project: testProject, BackendService: "bs-free"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := del.Wait(ctx); err != nil {
		t.Fatalf("Delete wait: %v", err)
	}
}

// assertInvalidArgument fails unless err is a 400 googleapi.Error.
func assertInvalidArgument(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("dangling reference: want error, got nil")
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("error = %v, want a googleapi.Error", err)
	}

	if gerr.Code != 400 {
		t.Errorf("error code = %d, want 400 (%v)", gerr.Code, err)
	}
}

// TestSDKGCPURLMapDanglingBackendServiceRef covers B2(a): a url-map whose
// defaultService names a missing backend service is rejected on both insert and
// update, while a reference to an existing backend service succeeds.
func TestSDKGCPURLMapDanglingBackendServiceRef(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	bs := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	um, err := gcpcompute.NewUrlMapsRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewUrlMapsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = um.Close() })

	// Dangling reference on insert → rejected.
	insOp, err := um.Insert(ctx, &computepb.InsertUrlMapRequest{
		Project: testProject,
		UrlMapResource: &computepb.UrlMap{
			Name:           ptrStr("map-dangling"),
			DefaultService: ptrStr("projects/" + testProject + "/global/backendServices/ghost-bs"),
		},
	})
	if err == nil {
		err = insOp.Wait(ctx)
	}

	assertInvalidArgument(t, err)

	// Existing reference → accepted.
	insertBS(ctx, t, bs, "real-bs")

	realSvc := "projects/" + testProject + "/global/backendServices/real-bs"

	insertUMOp, err := um.Insert(ctx, &computepb.InsertUrlMapRequest{
		Project:        testProject,
		UrlMapResource: &computepb.UrlMap{Name: ptrStr("map-real"), DefaultService: ptrStr(realSvc)},
	})
	if err != nil {
		t.Fatalf("UrlMap Insert with a real backend service: %v", err)
	}

	if err := insertUMOp.Wait(ctx); err != nil {
		t.Fatalf("UrlMap Insert wait: %v", err)
	}

	// Dangling reference on update → rejected.
	updOp, err := um.Update(ctx, &computepb.UpdateUrlMapRequest{
		Project: testProject,
		UrlMap:  "map-real",
		UrlMapResource: &computepb.UrlMap{
			Name:           ptrStr("map-real"),
			DefaultService: ptrStr("projects/" + testProject + "/global/backendServices/ghost-bs-2"),
		},
	})
	if err == nil {
		err = updOp.Wait(ctx)
	}

	assertInvalidArgument(t, err)
}

// TestSDKGCPTargetHTTPProxyDanglingURLMapRef covers B2(b): a target HTTP proxy
// whose urlMap names a missing url-map is rejected, both on insert and via
// setUrlMap; once the url-map exists, both succeed.
func TestSDKGCPTargetHTTPProxyDanglingURLMapRef(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	proxy, err := gcpcompute.NewTargetHttpProxiesRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewTargetHttpProxiesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = proxy.Close() })

	// Dangling reference on insert → rejected.
	insOp, err := proxy.Insert(ctx, &computepb.InsertTargetHttpProxyRequest{
		Project: testProject,
		TargetHttpProxyResource: &computepb.TargetHttpProxy{
			Name:   ptrStr("proxy-dangling"),
			UrlMap: ptrStr("projects/" + testProject + "/global/urlMaps/ghost-map"),
		},
	})
	if err == nil {
		err = insOp.Wait(ctx)
	}

	assertInvalidArgument(t, err)

	// Create the proxy empty, then setUrlMap against a still-missing map → rejected.
	waitOp(ctx, t, "targetHttpProxy Insert", func() (*gcpcompute.Operation, error) {
		return proxy.Insert(ctx, &computepb.InsertTargetHttpProxyRequest{
			Project:                 testProject,
			TargetHttpProxyResource: &computepb.TargetHttpProxy{Name: ptrStr("proxy-empty")},
		})
	})

	setOp, err := proxy.SetUrlMap(ctx, &computepb.SetUrlMapTargetHttpProxyRequest{
		Project:         testProject,
		TargetHttpProxy: "proxy-empty",
		UrlMapReferenceResource: &computepb.UrlMapReference{
			UrlMap: ptrStr("projects/" + testProject + "/global/urlMaps/ghost-map-2"),
		},
	})
	if err == nil {
		err = setOp.Wait(ctx)
	}

	assertInvalidArgument(t, err)

	// A real url-map (no backend service needed for this insert since it omits
	// defaultService) makes both paths succeed.
	um, err := gcpcompute.NewUrlMapsRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewUrlMapsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = um.Close() })

	waitOp(ctx, t, "urlMap Insert", func() (*gcpcompute.Operation, error) {
		return um.Insert(ctx, &computepb.InsertUrlMapRequest{
			Project:        testProject,
			UrlMapResource: &computepb.UrlMap{Name: ptrStr("real-map")},
		})
	})

	waitOp(ctx, t, "targetHttpProxy SetUrlMap", func() (*gcpcompute.Operation, error) {
		return proxy.SetUrlMap(ctx, &computepb.SetUrlMapTargetHttpProxyRequest{
			Project:                 testProject,
			TargetHttpProxy:         "proxy-empty",
			UrlMapReferenceResource: &computepb.UrlMapReference{UrlMap: ptrStr("projects/" + testProject + "/global/urlMaps/real-map")},
		})
	})
}

// TestSDKGCPForwardingRuleDanglingTargetRef covers B2(d): a global forwarding
// rule whose target names a missing target-http-proxy is rejected; once the
// proxy exists, the insert succeeds.
func TestSDKGCPForwardingRuleDanglingTargetRef(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	fr := newForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	target := "projects/" + testProject + "/global/targetHttpProxies/ghost-proxy"

	insOp, err := fr.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
		Project: testProject,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name:       ptrStr("fr-dangling"),
			IPProtocol: ptrStr("TCP"),
			PortRange:  ptrStr("80"),
			Target:     ptrStr(target),
		},
	})
	if err == nil {
		err = insOp.Wait(ctx)
	}

	assertInvalidArgument(t, err)

	proxy, err := gcpcompute.NewTargetHttpProxiesRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewTargetHttpProxiesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = proxy.Close() })

	waitOp(ctx, t, "targetHttpProxy Insert", func() (*gcpcompute.Operation, error) {
		return proxy.Insert(ctx, &computepb.InsertTargetHttpProxyRequest{
			Project:                 testProject,
			TargetHttpProxyResource: &computepb.TargetHttpProxy{Name: ptrStr("ghost-proxy")},
		})
	})

	waitOp(ctx, t, "forwardingRule Insert with a real target", func() (*gcpcompute.Operation, error) {
		return fr.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
			Project: testProject,
			ForwardingRuleResource: &computepb.ForwardingRule{
				Name:       ptrStr("fr-real"),
				IPProtocol: ptrStr("TCP"),
				PortRange:  ptrStr("80"),
				Target:     ptrStr(target),
			},
		})
	})
}

// TestSDKGCPBackendServiceBackendGroupRefValidation covers B2(e): a backend
// service whose backends[].group names a missing instance group is rejected,
// while a reference to an existing one succeeds.
func TestSDKGCPBackendServiceBackendGroupRefValidation(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	bs := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	ghostGroup := "projects/" + testProject + "/zones/" + testZone + "/instanceGroups/ghost-ig"

	insOp, err := bs.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: testProject,
		BackendServiceResource: &computepb.BackendService{
			Name:     ptrStr("bs-ghost-group"),
			Protocol: ptrStr("HTTP"),
			Backends: []*computepb.Backend{{Group: ptrStr(ghostGroup)}},
		},
	})
	if err == nil {
		err = insOp.Wait(ctx)
	}

	assertInvalidArgument(t, err)
}

// TestSDKGCPBackendServiceInvalidBalancingMode covers B2(f): a backend service
// whose backends[].balancingMode is not one of the GCP-recognized values is
// rejected.
func TestSDKGCPBackendServiceInvalidBalancingMode(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	bs := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	igClient, err := gcpcompute.NewInstanceGroupsRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewInstanceGroupsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = igClient.Close() })

	waitOp(ctx, t, "instanceGroup Insert", func() (*gcpcompute.Operation, error) {
		return igClient.Insert(ctx, &computepb.InsertInstanceGroupRequest{
			Project:               testProject,
			Zone:                  testZone,
			InstanceGroupResource: &computepb.InstanceGroup{Name: ptrStr("mode-ig")},
		})
	})

	group := "projects/" + testProject + "/zones/" + testZone + "/instanceGroups/mode-ig"

	insOp, err := bs.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: testProject,
		BackendServiceResource: &computepb.BackendService{
			Name:     ptrStr("bs-bad-mode"),
			Protocol: ptrStr("HTTP"),
			Backends: []*computepb.Backend{{Group: ptrStr(group), BalancingMode: ptrStr("NONSENSE")}},
		},
	})
	if err == nil {
		err = insOp.Wait(ctx)
	}

	assertInvalidArgument(t, err)
}
