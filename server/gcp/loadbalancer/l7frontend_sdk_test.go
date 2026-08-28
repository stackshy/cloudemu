package loadbalancer_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const testZone = "us-central1-a"

// clientOpts is the shared REST client option set pointing at the test server.
func clientOpts(ts *httptest.Server) []option.ClientOption {
	return []option.ClientOption{
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	}
}

// TestSDKGCPL7Chain provisions a full external L7 HTTP LB the way a real user
// does and asserts the front-end chain resolves end-to-end:
//
//	forwardingRule → targetHttpProxy → urlMap → backendService → instanceGroup
//
// It then registers an instance in the group and asserts listInstances and
// backendServices.getHealth reflect the membership, and that delete cleans up.
//
//nolint:gocyclo,cyclop,maintidx // one linear end-to-end scenario, kept in a single test for readability
func TestSDKGCPL7Chain(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	igClient, err := gcpcompute.NewInstanceGroupsRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewInstanceGroupsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = igClient.Close() })

	bsClient, err := gcpcompute.NewBackendServicesRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewBackendServicesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = bsClient.Close() })

	umClient, err := gcpcompute.NewUrlMapsRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewUrlMapsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = umClient.Close() })

	proxyClient, err := gcpcompute.NewTargetHttpProxiesRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewTargetHttpProxiesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = proxyClient.Close() })

	frClient, err := gcpcompute.NewGlobalForwardingRulesRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewGlobalForwardingRulesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = frClient.Close() })

	// 1. instance group (zonal, unmanaged).
	waitOp(ctx, t, "instanceGroup Insert", func() (*gcpcompute.Operation, error) {
		return igClient.Insert(ctx, &computepb.InsertInstanceGroupRequest{
			Project:               testProject,
			Zone:                  testZone,
			InstanceGroupResource: &computepb.InstanceGroup{Name: ptrStr("web-ig")},
		})
	})

	ig, err := igClient.Get(ctx, &computepb.GetInstanceGroupRequest{
		Project: testProject, Zone: testZone, InstanceGroup: "web-ig",
	})
	if err != nil {
		t.Fatalf("instanceGroup Get: %v", err)
	}

	igLink := ig.GetSelfLink()
	if igLink == "" {
		t.Fatal("instanceGroup selfLink empty")
	}

	// 2. backend service referencing the instance group.
	waitOp(ctx, t, "backendService Insert", func() (*gcpcompute.Operation, error) {
		return bsClient.Insert(ctx, &computepb.InsertBackendServiceRequest{
			Project: testProject,
			BackendServiceResource: &computepb.BackendService{
				Name:                ptrStr("web-bs"),
				Protocol:            ptrStr("HTTP"),
				LoadBalancingScheme: ptrStr("EXTERNAL_MANAGED"),
				Backends:            []*computepb.Backend{{Group: ptrStr(igLink)}},
			},
		})
	})

	bsLink := getBackendServiceLink(ctx, t, bsClient, "web-bs")

	// 3. url map whose default service is the backend service.
	waitOp(ctx, t, "urlMap Insert", func() (*gcpcompute.Operation, error) {
		return umClient.Insert(ctx, &computepb.InsertUrlMapRequest{
			Project:        testProject,
			UrlMapResource: &computepb.UrlMap{Name: ptrStr("web-map"), DefaultService: ptrStr(bsLink)},
		})
	})

	umLink := getURLMapLink(ctx, t, umClient, "web-map")

	// 4. target HTTP proxy pointing at the url map (created empty, then setUrlMap).
	waitOp(ctx, t, "targetHttpProxy Insert", func() (*gcpcompute.Operation, error) {
		return proxyClient.Insert(ctx, &computepb.InsertTargetHttpProxyRequest{
			Project:                testProject,
			TargetHttpProxyResource: &computepb.TargetHttpProxy{Name: ptrStr("web-proxy")},
		})
	})

	waitOp(ctx, t, "targetHttpProxy SetUrlMap", func() (*gcpcompute.Operation, error) {
		return proxyClient.SetUrlMap(ctx, &computepb.SetUrlMapTargetHttpProxyRequest{
			Project:                 testProject,
			TargetHttpProxy:         "web-proxy",
			UrlMapReferenceResource: &computepb.UrlMapReference{UrlMap: ptrStr(umLink)},
		})
	})

	proxy, err := proxyClient.Get(ctx, &computepb.GetTargetHttpProxyRequest{Project: testProject, TargetHttpProxy: "web-proxy"})
	if err != nil {
		t.Fatalf("targetHttpProxy Get: %v", err)
	}

	if lastSeg(proxy.GetUrlMap()) != "web-map" {
		t.Fatalf("proxy.urlMap = %q, want ...web-map (setUrlMap not applied)", proxy.GetUrlMap())
	}

	proxyLink := proxy.GetSelfLink()

	// 5. global forwarding rule targeting the proxy.
	waitOp(ctx, t, "forwardingRule Insert", func() (*gcpcompute.Operation, error) {
		return frClient.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
			Project: testProject,
			ForwardingRuleResource: &computepb.ForwardingRule{
				Name:      ptrStr("web-fr"),
				Target:    ptrStr(proxyLink),
				PortRange: ptrStr("80"),
			},
		})
	})

	// --- walk the chain fr → proxy → urlMap → backendService → instanceGroup ---
	fr, err := frClient.Get(ctx, &computepb.GetGlobalForwardingRuleRequest{Project: testProject, ForwardingRule: "web-fr"})
	if err != nil {
		t.Fatalf("forwardingRule Get: %v", err)
	}

	if lastSeg(fr.GetTarget()) != "web-proxy" {
		t.Fatalf("fr.target = %q, want ...web-proxy", fr.GetTarget())
	}

	um, err := umClient.Get(ctx, &computepb.GetUrlMapRequest{Project: testProject, UrlMap: "web-map"})
	if err != nil {
		t.Fatalf("urlMap Get: %v", err)
	}

	if lastSeg(um.GetDefaultService()) != "web-bs" {
		t.Fatalf("urlMap.defaultService = %q, want ...web-bs", um.GetDefaultService())
	}

	bs, err := bsClient.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "web-bs"})
	if err != nil {
		t.Fatalf("backendService Get: %v", err)
	}

	if len(bs.GetBackends()) != 1 || lastSeg(bs.GetBackends()[0].GetGroup()) != "web-ig" {
		t.Fatalf("backendService.backends = %v, want one referencing web-ig", bs.GetBackends())
	}

	// --- register an instance and assert membership is reflected ---
	instURL := "projects/" + testProject + "/zones/" + testZone + "/instances/vm-1"

	waitOp(ctx, t, "instanceGroup AddInstances", func() (*gcpcompute.Operation, error) {
		return igClient.AddInstances(ctx, &computepb.AddInstancesInstanceGroupRequest{
			Project: testProject, Zone: testZone, InstanceGroup: "web-ig",
			InstanceGroupsAddInstancesRequestResource: &computepb.InstanceGroupsAddInstancesRequest{
				Instances: []*computepb.InstanceReference{{Instance: ptrStr(instURL)}},
			},
		})
	})

	assertListInstances(ctx, t, igClient, instURL)
	assertGetHealth(ctx, t, bsClient, igLink, instURL)

	// --- reference integrity: the group can't be deleted while the backend uses it ---
	_, err = igClient.Delete(ctx, &computepb.DeleteInstanceGroupRequest{Project: testProject, Zone: testZone, InstanceGroup: "web-ig"})
	if err == nil {
		t.Fatal("instanceGroup Delete while referenced: want error, got nil")
	}

	// --- teardown in dependency order ---
	waitOp(ctx, t, "forwardingRule Delete", func() (*gcpcompute.Operation, error) {
		return frClient.Delete(ctx, &computepb.DeleteGlobalForwardingRuleRequest{Project: testProject, ForwardingRule: "web-fr"})
	})
	waitOp(ctx, t, "targetHttpProxy Delete", func() (*gcpcompute.Operation, error) {
		return proxyClient.Delete(ctx, &computepb.DeleteTargetHttpProxyRequest{Project: testProject, TargetHttpProxy: "web-proxy"})
	})
	waitOp(ctx, t, "urlMap Delete", func() (*gcpcompute.Operation, error) {
		return umClient.Delete(ctx, &computepb.DeleteUrlMapRequest{Project: testProject, UrlMap: "web-map"})
	})
	waitOp(ctx, t, "backendService Delete", func() (*gcpcompute.Operation, error) {
		return bsClient.Delete(ctx, &computepb.DeleteBackendServiceRequest{Project: testProject, BackendService: "web-bs"})
	})
	waitOp(ctx, t, "instanceGroup Delete", func() (*gcpcompute.Operation, error) {
		return igClient.Delete(ctx, &computepb.DeleteInstanceGroupRequest{Project: testProject, Zone: testZone, InstanceGroup: "web-ig"})
	})

	if _, err := igClient.Get(ctx, &computepb.GetInstanceGroupRequest{Project: testProject, Zone: testZone, InstanceGroup: "web-ig"}); err == nil {
		t.Fatal("instanceGroup Get after delete: want error, got nil")
	}
}

// TestSDKGCPTargetHTTPSProxyWithCertificate covers the HTTPS front-end: an SSL
// certificate, and a target HTTPS proxy that binds it via setSslCertificates and
// a url map via setUrlMap.
func TestSDKGCPTargetHTTPSProxyWithCertificate(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	certClient, err := gcpcompute.NewSslCertificatesRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewSslCertificatesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = certClient.Close() })

	proxyClient, err := gcpcompute.NewTargetHttpsProxiesRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewTargetHttpsProxiesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = proxyClient.Close() })

	waitOp(ctx, t, "sslCertificate Insert", func() (*gcpcompute.Operation, error) {
		return certClient.Insert(ctx, &computepb.InsertSslCertificateRequest{
			Project: testProject,
			SslCertificateResource: &computepb.SslCertificate{
				Name:        ptrStr("web-cert"),
				Certificate: ptrStr("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"),
			},
		})
	})

	cert, err := certClient.Get(ctx, &computepb.GetSslCertificateRequest{Project: testProject, SslCertificate: "web-cert"})
	if err != nil {
		t.Fatalf("sslCertificate Get: %v", err)
	}

	certLink := cert.GetSelfLink()
	if certLink == "" {
		t.Fatal("sslCertificate selfLink empty")
	}

	waitOp(ctx, t, "targetHttpsProxy Insert", func() (*gcpcompute.Operation, error) {
		return proxyClient.Insert(ctx, &computepb.InsertTargetHttpsProxyRequest{
			Project:                  testProject,
			TargetHttpsProxyResource: &computepb.TargetHttpsProxy{Name: ptrStr("web-https-proxy")},
		})
	})

	waitOp(ctx, t, "targetHttpsProxy SetSslCertificates", func() (*gcpcompute.Operation, error) {
		return proxyClient.SetSslCertificates(ctx, &computepb.SetSslCertificatesTargetHttpsProxyRequest{
			Project:         testProject,
			TargetHttpsProxy: "web-https-proxy",
			TargetHttpsProxiesSetSslCertificatesRequestResource: &computepb.TargetHttpsProxiesSetSslCertificatesRequest{
				SslCertificates: []string{certLink},
			},
		})
	})

	proxy, err := proxyClient.Get(ctx, &computepb.GetTargetHttpsProxyRequest{Project: testProject, TargetHttpsProxy: "web-https-proxy"})
	if err != nil {
		t.Fatalf("targetHttpsProxy Get: %v", err)
	}

	if len(proxy.GetSslCertificates()) != 1 || lastSeg(proxy.GetSslCertificates()[0]) != "web-cert" {
		t.Fatalf("proxy.sslCertificates = %v, want [...web-cert]", proxy.GetSslCertificates())
	}

	// The certificate is pinned by the proxy and must not be deletable.
	if _, err := certClient.Delete(ctx, &computepb.DeleteSslCertificateRequest{Project: testProject, SslCertificate: "web-cert"}); err == nil {
		t.Fatal("sslCertificate Delete while bound: want error, got nil")
	}
}

// TestGCPRegionInstanceGroupCRUD exercises the regional instance-group
// collection over raw HTTP (the compute SDK's RegionInstanceGroups client has no
// Insert), proving insert/get/list/delete round-trip at region scope.
func TestGCPRegionInstanceGroupCRUD(t *testing.T) {
	ts := newGCPLBServer(t)

	base := ts.URL + "/compute/v1/projects/" + testProject + "/regions/us-central1/regionInstanceGroups"

	if code, body := doJSON(t, ts, http.MethodPost, base, `{"name":"reg-ig"}`); code != http.StatusOK {
		t.Fatalf("insert: status %d, body %s", code, body)
	}

	code, body := doJSON(t, ts, http.MethodGet, base+"/reg-ig", "")
	if code != http.StatusOK {
		t.Fatalf("get: status %d, body %s", code, body)
	}

	if !strings.Contains(body, `"reg-ig"`) || !strings.Contains(body, `/regions/us-central1/`) {
		t.Fatalf("get body missing name/region self-link: %s", body)
	}

	if code, body := doJSON(t, ts, http.MethodGet, base, ""); code != http.StatusOK || !strings.Contains(body, `"reg-ig"`) {
		t.Fatalf("list: status %d, body %s", code, body)
	}

	if code, body := doJSON(t, ts, http.MethodDelete, base+"/reg-ig", ""); code != http.StatusOK {
		t.Fatalf("delete: status %d, body %s", code, body)
	}

	if code, _ := doJSON(t, ts, http.MethodGet, base+"/reg-ig", ""); code != http.StatusNotFound {
		t.Fatalf("get after delete: status %d, want 404", code)
	}
}

// --- helpers ---

// waitOp runs a mutating call returning an LRO and waits for it to complete.
func waitOp(ctx context.Context, t *testing.T, label string, call func() (*gcpcompute.Operation, error)) {
	t.Helper()

	op, err := call()
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("%s wait: %v", label, err)
	}
}

func getBackendServiceLink(ctx context.Context, t *testing.T, c *gcpcompute.BackendServicesClient, name string) string {
	t.Helper()

	bs, err := c.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: name})
	if err != nil {
		t.Fatalf("backendService Get %s: %v", name, err)
	}

	return bs.GetSelfLink()
}

func getURLMapLink(ctx context.Context, t *testing.T, c *gcpcompute.UrlMapsClient, name string) string {
	t.Helper()

	um, err := c.Get(ctx, &computepb.GetUrlMapRequest{Project: testProject, UrlMap: name})
	if err != nil {
		t.Fatalf("urlMap Get %s: %v", name, err)
	}

	return um.GetSelfLink()
}

func assertListInstances(ctx context.Context, t *testing.T, c *gcpcompute.InstanceGroupsClient, wantInstance string) {
	t.Helper()

	it := c.ListInstances(ctx, &computepb.ListInstancesInstanceGroupsRequest{
		Project: testProject, Zone: testZone, InstanceGroup: "web-ig",
		InstanceGroupsListInstancesRequestResource: &computepb.InstanceGroupsListInstancesRequest{},
	})

	var got []string

	for {
		m, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("listInstances: %v", err)
		}

		got = append(got, m.GetInstance())
	}

	if len(got) != 1 || lastSeg(got[0]) != lastSeg(wantInstance) {
		t.Fatalf("listInstances = %v, want [%s]", got, wantInstance)
	}
}

func assertGetHealth(ctx context.Context, t *testing.T, c *gcpcompute.BackendServicesClient, group, wantInstance string) {
	t.Helper()

	health, err := c.GetHealth(ctx, &computepb.GetHealthBackendServiceRequest{
		Project:                        testProject,
		BackendService:                 "web-bs",
		ResourceGroupReferenceResource: &computepb.ResourceGroupReference{Group: ptrStr(group)},
	})
	if err != nil {
		t.Fatalf("getHealth: %v", err)
	}

	statuses := health.GetHealthStatus()
	if len(statuses) != 1 || lastSeg(statuses[0].GetInstance()) != lastSeg(wantInstance) {
		t.Fatalf("getHealth statuses = %v, want one for %s", statuses, wantInstance)
	}

	if statuses[0].GetHealthState() != "HEALTHY" {
		t.Errorf("healthState = %q, want HEALTHY", statuses[0].GetHealthState())
	}
}

// doJSON performs a raw JSON request against the test server and returns the
// status code and response body.
func doJSON(t *testing.T, ts *httptest.Server, method, url, body string) (int, string) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

// lastSeg returns the trailing path segment of a GCP reference.
func lastSeg(ref string) string {
	if idx := strings.LastIndex(ref, "/"); idx >= 0 {
		return ref[idx+1:]
	}

	return ref
}

// TestSDKGCPInstanceGroupDeleteScopedToZone guards the in-use delete check
// against matching an instance group by trailing name only: a same-named group
// in a different zone must remain deletable even while another zone's group of
// the same name is referenced by a backend service.
func TestSDKGCPInstanceGroupDeleteScopedToZone(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	const otherZone = "us-central1-b"

	igClient, err := gcpcompute.NewInstanceGroupsRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewInstanceGroupsRESTClient: %v", err)
	}
	t.Cleanup(func() { _ = igClient.Close() })

	bsClient, err := gcpcompute.NewBackendServicesRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewBackendServicesRESTClient: %v", err)
	}
	t.Cleanup(func() { _ = bsClient.Close() })

	// Same-named instance group in two different zones.
	for _, zone := range []string{testZone, otherZone} {
		z := zone
		waitOp(ctx, t, "instanceGroup Insert "+z, func() (*gcpcompute.Operation, error) {
			return igClient.Insert(ctx, &computepb.InsertInstanceGroupRequest{
				Project:               testProject,
				Zone:                  z,
				InstanceGroupResource: &computepb.InstanceGroup{Name: ptrStr("shared-ig")},
			})
		})
	}

	// A backend service references ONLY the testZone group.
	ig, err := igClient.Get(ctx, &computepb.GetInstanceGroupRequest{
		Project: testProject, Zone: testZone, InstanceGroup: "shared-ig",
	})
	if err != nil {
		t.Fatalf("instanceGroup Get: %v", err)
	}

	waitOp(ctx, t, "backendService Insert", func() (*gcpcompute.Operation, error) {
		return bsClient.Insert(ctx, &computepb.InsertBackendServiceRequest{
			Project: testProject,
			BackendServiceResource: &computepb.BackendService{
				Name:                ptrStr("shared-bs"),
				Protocol:            ptrStr("HTTP"),
				LoadBalancingScheme: ptrStr("EXTERNAL_MANAGED"),
				Backends:            []*computepb.Backend{{Group: ptrStr(ig.GetSelfLink())}},
			},
		})
	})

	// The unreferenced same-named group in the other zone must delete cleanly.
	if _, err := igClient.Delete(ctx, &computepb.DeleteInstanceGroupRequest{
		Project: testProject, Zone: otherZone, InstanceGroup: "shared-ig",
	}); err != nil {
		t.Fatalf("delete of unreferenced same-named group in %s should succeed, got: %v", otherZone, err)
	}

	// The referenced group must still be blocked.
	if _, err := igClient.Delete(ctx, &computepb.DeleteInstanceGroupRequest{
		Project: testProject, Zone: testZone, InstanceGroup: "shared-ig",
	}); err == nil {
		t.Fatalf("delete of the in-use group in %s should be blocked", testZone)
	}
}
