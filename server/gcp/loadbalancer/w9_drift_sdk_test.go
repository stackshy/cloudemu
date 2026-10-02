package loadbalancer_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

const (
	w9Region          = "us-central1"
	emptyLabelFP      = "42WmSpB8rSM="
	regionalHCRefBase = "projects/" + testProject + "/regions/"
)

func w9Clients(t *testing.T) (*httptest.Server, *gcpcompute.ForwardingRulesClient, *gcpcompute.RegionBackendServicesClient) {
	t.Helper()

	ts := newGCPLBServer(t)

	bs, err := gcpcompute.NewRegionBackendServicesRESTClient(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewRegionBackendServicesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = bs.Close() })

	return ts, newRegionalForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client())), bs
}

func insertRegionHC(ctx context.Context, t *testing.T, ts *httptest.Server, region, name string) {
	t.Helper()

	c, err := gcpcompute.NewRegionHealthChecksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewRegionHealthChecksRESTClient: %v", err)
	}

	defer func() { _ = c.Close() }()

	waitOp(ctx, t, "region HC insert", func() (*gcpcompute.Operation, error) {
		return c.Insert(ctx, &computepb.InsertRegionHealthCheckRequest{
			Project: testProject, Region: region,
			HealthCheckResource: &computepb.HealthCheck{Name: ptrStr(name), Type: ptrStr("TCP")},
		})
	})
}

func insertRegionBS(ctx context.Context, c *gcpcompute.RegionBackendServicesClient, name, scheme string, hcs ...string) error {
	bs := &computepb.BackendService{Name: ptrStr(name), Protocol: ptrStr("TCP"), HealthChecks: hcs}
	if scheme != "" {
		bs.LoadBalancingScheme = ptrStr(scheme)
	}

	return callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.Insert(ctx, &computepb.InsertRegionBackendServiceRequest{
			Project: testProject, Region: w9Region, BackendServiceResource: bs,
		})
	})
}

func getRegionalFR(ctx context.Context, t *testing.T, c *gcpcompute.ForwardingRulesClient, name string) *computepb.ForwardingRule {
	t.Helper()

	got, err := c.Get(ctx, &computepb.GetForwardingRuleRequest{Project: testProject, Region: w9Region, ForwardingRule: name})
	if err != nil {
		t.Fatalf("Get %s: %v", name, err)
	}

	return got
}

func insertRegionalFR(ctx context.Context, c *gcpcompute.ForwardingRulesClient, fr *computepb.ForwardingRule) error {
	return callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.Insert(ctx, &computepb.InsertForwardingRuleRequest{
			Project: testProject, Region: w9Region, ForwardingRuleResource: fr,
		})
	})
}

func setRegionalLabels(ctx context.Context, c *gcpcompute.ForwardingRulesClient, name, fp string, labels map[string]string) error {
	return callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.SetLabels(ctx, &computepb.SetLabelsForwardingRuleRequest{
			Project: testProject, Region: w9Region, Resource: name,
			RegionSetLabelsRequestResource: &computepb.RegionSetLabelsRequest{Labels: labels, LabelFingerprint: ptrStr(fp)},
		})
	})
}

// TestSDKGCPRegionalForwardingRuleRoundTrip covers GLB-03 and GLB-06: a regional
// internal allPorts rule behind a regional backend service that uses a global
// health check.
func TestSDKGCPRegionalForwardingRuleRoundTrip(t *testing.T) {
	ctx := context.Background()
	ts, frc, bsc := w9Clients(t)

	insertHealthCheck(ctx, t, ts, "hc")

	if err := insertRegionBS(ctx, bsc, "bs", "INTERNAL", hcRef("hc")); err != nil {
		t.Fatalf("regional BS with global HC: %v", err)
	}

	if err := insertRegionalFR(ctx, frc, &computepb.ForwardingRule{
		Name:                ptrStr("fr"),
		LoadBalancingScheme: ptrStr("INTERNAL"),
		BackendService:      ptrStr("projects/" + testProject + "/regions/" + w9Region + "/backendServices/bs"),
		AllPorts:            ptrBool(true),
		AllowGlobalAccess:   ptrBool(true),
		IpVersion:           ptrStr("IPV4"),
		Labels:              map[string]string{"a": "b"},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got := getRegionalFR(ctx, t, frc, "fr")

	if !strings.HasSuffix(got.GetRegion(), "/regions/"+w9Region) {
		t.Errorf("region = %q", got.GetRegion())
	}

	if !got.GetAllPorts() || !got.GetAllowGlobalAccess() {
		t.Errorf("allPorts=%v allowGlobalAccess=%v, want both true", got.GetAllPorts(), got.GetAllowGlobalAccess())
	}

	if got.PortRange != nil {
		t.Errorf("portRange = %q, want omitted for allPorts", got.GetPortRange())
	}

	if got.GetLabels()["a"] != "b" || got.GetLabelFingerprint() == "" || got.GetLabelFingerprint() == emptyLabelFP {
		t.Errorf("labels = %v fp = %q", got.GetLabels(), got.GetLabelFingerprint())
	}

	if got.GetIpVersion() != "IPV4" || got.GetNetworkTier() != "PREMIUM" || got.GetFingerprint() == "" {
		t.Errorf("ipVersion=%q networkTier=%q fingerprint=%q", got.GetIpVersion(), got.GetNetworkTier(), got.GetFingerprint())
	}

	// GUARD: fields never sent stay absent rather than synthesized as false.
	if err := insertRegionalFR(ctx, frc, &computepb.ForwardingRule{Name: ptrStr("plain"), PortRange: ptrStr("80")}); err != nil {
		t.Fatalf("Insert plain: %v", err)
	}

	plain := getRegionalFR(ctx, t, frc, "plain")
	if plain.AllPorts != nil || plain.AllowGlobalAccess != nil || plain.GetLabelFingerprint() != emptyLabelFP {
		t.Errorf("plain rule: allPorts=%v allowGlobalAccess=%v labelFingerprint=%q",
			plain.AllPorts, plain.AllowGlobalAccess, plain.GetLabelFingerprint())
	}

	err := insertRegionalFR(ctx, frc, &computepb.ForwardingRule{
		Name: ptrStr("bad"), AllPorts: ptrBool(true), PortRange: ptrStr("80"),
	})
	assertHTTPCode(t, err, 400)
}

// TestSDKGCPForwardingRuleSetLabels follows Terraform's create-then-setLabels
// flow (GLB-16) and the labelFingerprint check.
func TestSDKGCPForwardingRuleSetLabels(t *testing.T) {
	ctx := context.Background()
	_, frc, _ := w9Clients(t)

	if err := insertRegionalFR(ctx, frc, &computepb.ForwardingRule{
		Name: ptrStr("fr"), PortRange: ptrStr("80"), Labels: map[string]string{"goog-terraform-provisioned": "true"},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	first := getRegionalFR(ctx, t, frc, "fr")

	if err := setRegionalLabels(ctx, frc, "fr", first.GetLabelFingerprint(), first.GetLabels()); err != nil {
		t.Fatalf("SetLabels with read fingerprint: %v", err)
	}

	if err := setRegionalLabels(ctx, frc, "fr", first.GetLabelFingerprint(), map[string]string{"env": "prod"}); err != nil {
		t.Fatalf("SetLabels env=prod: %v", err)
	}

	got := getRegionalFR(ctx, t, frc, "fr")
	if len(got.GetLabels()) != 1 || got.GetLabels()["env"] != "prod" {
		t.Errorf("labels = %v, want only env=prod (replace)", got.GetLabels())
	}

	if got.GetLabelFingerprint() == first.GetLabelFingerprint() || got.GetFingerprint() == first.GetFingerprint() {
		t.Error("fingerprints did not change after setLabels")
	}

	assertHTTPCode(t, setRegionalLabels(ctx, frc, "fr", first.GetLabelFingerprint(), map[string]string{"x": "y"}), 412)

	if err := setRegionalLabels(ctx, frc, "fr", "", nil); err != nil {
		t.Fatalf("SetLabels clear: %v", err)
	}

	if fp := getRegionalFR(ctx, t, frc, "fr").GetLabelFingerprint(); fp != emptyLabelFP {
		t.Errorf("empty labelFingerprint = %q", fp)
	}
}

// TestSDKGCPForwardingRuleSetLabelsScoped proves setLabels on a regional rule
// does not touch a global rule of the same name, and the reverse.
func TestSDKGCPForwardingRuleSetLabelsScoped(t *testing.T) {
	ctx := context.Background()
	ts, frc, _ := w9Clients(t)
	gfr := newForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	if err := insertRegionalFR(ctx, frc, &computepb.ForwardingRule{Name: ptrStr("dup"), PortRange: ptrStr("80")}); err != nil {
		t.Fatalf("regional Insert: %v", err)
	}

	waitOp(ctx, t, "global insert", func() (*gcpcompute.Operation, error) {
		return gfr.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
			Project: testProject, ForwardingRuleResource: &computepb.ForwardingRule{Name: ptrStr("dup"), PortRange: ptrStr("80")},
		})
	})

	if err := setRegionalLabels(ctx, frc, "dup", "", map[string]string{"scope": "regional"}); err != nil {
		t.Fatalf("regional SetLabels: %v", err)
	}

	waitOp(ctx, t, "global setLabels", func() (*gcpcompute.Operation, error) {
		return gfr.SetLabels(ctx, &computepb.SetLabelsGlobalForwardingRuleRequest{
			Project: testProject, Resource: "dup",
			GlobalSetLabelsRequestResource: &computepb.GlobalSetLabelsRequest{Labels: map[string]string{"scope": "global"}},
		})
	})

	g, err := gfr.Get(ctx, &computepb.GetGlobalForwardingRuleRequest{Project: testProject, ForwardingRule: "dup"})
	if err != nil {
		t.Fatalf("global Get: %v", err)
	}

	if g.GetLabels()["scope"] != "global" || g.Region != nil {
		t.Errorf("global rule labels = %v region = %q", g.GetLabels(), g.GetRegion())
	}

	if r := getRegionalFR(ctx, t, frc, "dup"); r.GetLabels()["scope"] != "regional" {
		t.Errorf("regional rule labels = %v", r.GetLabels())
	}
}

// TestSDKGCPForwardingRulePatchAllowGlobalAccess covers the in-place
// allow_global_access update, which Terraform sends without a fingerprint.
func TestSDKGCPForwardingRulePatchAllowGlobalAccess(t *testing.T) {
	ctx := context.Background()
	_, frc, _ := w9Clients(t)

	if err := insertRegionalFR(ctx, frc, &computepb.ForwardingRule{
		Name: ptrStr("fr"), PortRange: ptrStr("80"), AllowGlobalAccess: ptrBool(true), LoadBalancingScheme: ptrStr("INTERNAL"),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	before := getRegionalFR(ctx, t, frc, "fr")

	patch := func(fr *computepb.ForwardingRule) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return frc.Patch(ctx, &computepb.PatchForwardingRuleRequest{
				Project: testProject, Region: w9Region, ForwardingRule: "fr", ForwardingRuleResource: fr,
			})
		})
	}

	if err := patch(&computepb.ForwardingRule{AllowGlobalAccess: ptrBool(false)}); err != nil {
		t.Fatalf("Patch without fingerprint: %v", err)
	}

	got := getRegionalFR(ctx, t, frc, "fr")
	if got.AllowGlobalAccess == nil || got.GetAllowGlobalAccess() || got.GetPortRange() != "80" ||
		got.GetLoadBalancingScheme() != "INTERNAL" {
		t.Errorf("after patch: allowGlobalAccess=%v portRange=%q scheme=%q",
			got.AllowGlobalAccess, got.GetPortRange(), got.GetLoadBalancingScheme())
	}

	assertHTTPCode(t, patch(&computepb.ForwardingRule{
		AllowGlobalAccess: ptrBool(true), Fingerprint: ptrStr(before.GetFingerprint()),
	}), 412)
}

// TestSDKGCPRegionBackendServiceRegion covers GLB-05.
func TestSDKGCPRegionBackendServiceRegion(t *testing.T) {
	ctx := context.Background()
	ts, _, bsc := w9Clients(t)

	const network = "projects/" + testProject + "/global/networks/n"

	waitOp(ctx, t, "insert", func() (*gcpcompute.Operation, error) {
		return bsc.Insert(ctx, &computepb.InsertRegionBackendServiceRequest{
			Project: testProject, Region: w9Region,
			BackendServiceResource: &computepb.BackendService{
				Name: ptrStr("bs"), LoadBalancingScheme: ptrStr("INTERNAL"), Network: ptrStr(network),
			},
		})
	})

	got, err := bsc.Get(ctx, &computepb.GetRegionBackendServiceRequest{Project: testProject, Region: w9Region, BackendService: "bs"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !strings.HasSuffix(got.GetRegion(), "/regions/"+w9Region) {
		t.Errorf("regional BS region = %q", got.GetRegion())
	}

	// network is ForceNew in Terraform, so dropping it forces a replace.
	if got.GetNetwork() != network {
		t.Errorf("network = %q, want %q", got.GetNetwork(), network)
	}

	gbs := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))
	insertBS(ctx, t, gbs, "gbs")

	g, err := gbs.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "gbs"})
	if err != nil {
		t.Fatalf("global Get: %v", err)
	}

	if g.Region != nil {
		t.Errorf("global BS region = %q, want omitted", g.GetRegion())
	}
}

// TestSDKGCPRegionBackendServiceGlobalHealthCheck covers GLB-06: which health
// check scopes a backend service may reference.
func TestSDKGCPRegionBackendServiceGlobalHealthCheck(t *testing.T) {
	tests := []struct {
		name     string
		regional bool
		scheme   string
		ref      func(base string) string
		wantCode int
	}{
		{"internal relative global", true, "INTERNAL", func(string) string { return hcRef("ghc") }, 0},
		{"internal full URL global", true, "INTERNAL", func(b string) string {
			return b + "/compute/v1/" + hcRef("ghc")
		}, 0},
		{"default scheme global", true, "", func(string) string { return hcRef("ghc") }, 0},
		{"external global", true, "EXTERNAL", func(string) string { return hcRef("ghc") }, 400},
		{"internal managed global", true, "INTERNAL_MANAGED", func(string) string { return hcRef("ghc") }, 400},
		{"same region regional", true, "EXTERNAL", func(string) string {
			return regionalHCRefBase + w9Region + "/healthChecks/rhc"
		}, 0},
		{"other region regional", true, "INTERNAL", func(string) string {
			return regionalHCRefBase + "europe-west1/healthChecks/rhc"
		}, 400},
		{"global BS regional HC", false, "EXTERNAL", func(string) string {
			return regionalHCRefBase + w9Region + "/healthChecks/rhc"
		}, 400},
		{"missing global", true, "INTERNAL", func(string) string { return hcRef("nope") }, 400},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ts, _, bsc := w9Clients(t)

			insertHealthCheck(ctx, t, ts, "ghc")
			insertRegionHC(ctx, t, ts, w9Region, "rhc")

			var err error

			if tc.regional {
				err = insertRegionBS(ctx, bsc, "bs", tc.scheme, tc.ref(ts.URL))
			} else {
				gbs := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))
				err = callOp(ctx, func() (*gcpcompute.Operation, error) {
					return gbs.Insert(ctx, &computepb.InsertBackendServiceRequest{
						Project: testProject, BackendServiceResource: &computepb.BackendService{
							Name: ptrStr("bs"), LoadBalancingScheme: ptrStr(tc.scheme), HealthChecks: []string{tc.ref(ts.URL)},
						},
					})
				})
			}

			if tc.wantCode == 0 {
				if err != nil {
					t.Fatalf("Insert: %v", err)
				}

				return
			}

			assertHTTPCode(t, err, tc.wantCode)
		})
	}
}

// TestSDKGCPRegionBackendServicePatchGlobalHealthCheck proves patch resolves a
// global ref using the stored INTERNAL scheme.
func TestSDKGCPRegionBackendServicePatchGlobalHealthCheck(t *testing.T) {
	ctx := context.Background()
	ts, _, bsc := w9Clients(t)

	insertHealthCheck(ctx, t, ts, "ghc")

	if err := insertRegionBS(ctx, bsc, "bs", "INTERNAL"); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	waitOp(ctx, t, "patch", func() (*gcpcompute.Operation, error) {
		return bsc.Patch(ctx, &computepb.PatchRegionBackendServiceRequest{
			Project: testProject, Region: w9Region, BackendService: "bs",
			BackendServiceResource: &computepb.BackendService{HealthChecks: []string{hcRef("ghc")}},
		})
	})
}

// TestSDKGCPHealthCheckDeleteScopeAware covers the delete-in-use guard across
// scopes.
func TestSDKGCPHealthCheckDeleteScopeAware(t *testing.T) {
	ctx := context.Background()
	ts, _, bsc := w9Clients(t)

	insertHealthCheck(ctx, t, ts, "hc")
	insertRegionHC(ctx, t, ts, w9Region, "hc")

	if err := insertRegionBS(ctx, bsc, "bs", "INTERNAL", hcRef("hc")); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rhc, err := gcpcompute.NewRegionHealthChecksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	t.Cleanup(func() { _ = rhc.Close() })

	// The regional hc is not referenced; only the global one is.
	waitOp(ctx, t, "delete regional hc", func() (*gcpcompute.Operation, error) {
		return rhc.Delete(ctx, &computepb.DeleteRegionHealthCheckRequest{Project: testProject, Region: w9Region, HealthCheck: "hc"})
	})

	ghc := newHealthChecksClient(t, ts)
	assertHTTPCode(t, callOp(ctx, func() (*gcpcompute.Operation, error) {
		return ghc.Delete(ctx, &computepb.DeleteHealthCheckRequest{Project: testProject, HealthCheck: "hc"})
	}), 400)
}

// TestSDKGCPBackendServiceFingerprint covers GLB-09: the fingerprint changes on
// every mutation and a stale one is rejected.
func TestSDKGCPBackendServiceFingerprint(t *testing.T) {
	ctx := context.Background()
	ts, _, _ := w9Clients(t)
	c := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	// Insert ignores a client-sent fingerprint.
	waitOp(ctx, t, "insert", func() (*gcpcompute.Operation, error) {
		return c.Insert(ctx, &computepb.InsertBackendServiceRequest{
			Project: testProject, BackendServiceResource: &computepb.BackendService{Name: ptrStr("bs"), Fingerprint: ptrStr("bogus")},
		})
	})

	fp := func() string {
		got, err := c.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "bs"})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}

		return got.GetFingerprint()
	}

	patch := func(fingerprint *string) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return c.Patch(ctx, &computepb.PatchBackendServiceRequest{
				Project: testProject, BackendService: "bs",
				BackendServiceResource: &computepb.BackendService{Description: ptrStr("d"), Fingerprint: fingerprint},
			})
		})
	}

	fp0 := fp()
	if err := patch(ptrStr(fp0)); err != nil {
		t.Fatalf("patch with current fingerprint: %v", err)
	}

	fp1 := fp()
	if fp1 == fp0 {
		t.Fatal("fingerprint unchanged after patch")
	}

	assertHTTPCode(t, patch(ptrStr(fp0)), 412)

	if fp() != fp1 {
		t.Error("stale patch changed the fingerprint")
	}

	if err := patch(nil); err != nil {
		t.Fatalf("patch without fingerprint: %v", err)
	}

	fp2 := fp()

	waitOp(ctx, t, "addSignedUrlKey", func() (*gcpcompute.Operation, error) {
		return c.AddSignedUrlKey(ctx, &computepb.AddSignedUrlKeyBackendServiceRequest{
			Project: testProject, BackendService: "bs",
			SignedUrlKeyResource: &computepb.SignedUrlKey{KeyName: ptrStr("k"), KeyValue: ptrStr("dGVzdGtleXRlc3RrZXl0ZQ==")},
		})
	})

	if fp() == fp2 {
		t.Error("fingerprint unchanged after addSignedUrlKey")
	}
}

// TestSDKGCPForwardingRuleSetLabelsConcurrent checks overlapping setLabels calls
// all succeed and each one moves the fingerprint.
func TestSDKGCPForwardingRuleSetLabelsConcurrent(t *testing.T) {
	const workers = 8

	ctx := context.Background()
	_, frc, _ := w9Clients(t)

	if err := insertRegionalFR(ctx, frc, &computepb.ForwardingRule{Name: ptrStr("fr"), PortRange: ptrStr("80")}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	start := getRegionalFR(ctx, t, frc, "fr").GetFingerprint()

	var wg sync.WaitGroup

	errs := make(chan error, workers)

	for i := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			errs <- setRegionalLabels(ctx, frc, "fr", "", map[string]string{"w": string(rune('a' + i))})
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("SetLabels: %v", err)
		}
	}

	if getRegionalFR(ctx, t, frc, "fr").GetFingerprint() == start {
		t.Error("fingerprint unchanged after concurrent setLabels")
	}
}
