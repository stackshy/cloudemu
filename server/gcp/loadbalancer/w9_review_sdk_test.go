package loadbalancer_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// TestSDKGCPRegionBackendServicePatchGetConcurrent runs PATCH and GET on one
// regional backend service at once. Under -race it fails if a patch writes the
// Tags map that readers hold.
func TestSDKGCPRegionBackendServicePatchGetConcurrent(t *testing.T) {
	const workers = 6

	ctx := context.Background()
	_, _, bsc := w9Clients(t)

	if err := insertRegionBS(ctx, bsc, "bs", "INTERNAL"); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var wg sync.WaitGroup

	errs := make(chan error, 2*workers)

	for i := range workers {
		wg.Add(2)

		go func() {
			defer wg.Done()

			errs <- callOp(ctx, func() (*gcpcompute.Operation, error) {
				return bsc.Patch(ctx, &computepb.PatchRegionBackendServiceRequest{
					Project: testProject, Region: w9Region, BackendService: "bs",
					BackendServiceResource: &computepb.BackendService{TimeoutSec: ptrInt32(int32(10 + i))},
				})
			})
		}()

		go func() {
			defer wg.Done()

			_, err := bsc.Get(ctx, &computepb.GetRegionBackendServiceRequest{
				Project: testProject, Region: w9Region, BackendService: "bs",
			})
			errs <- err
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent call: %v", err)
		}
	}
}

func ptrInt32(v int32) *int32 { return &v }

// TestSDKGCPRegionalMutateNotFoundName checks a 404 from setLabels or PATCH on
// a missing regional resource names the resource as the client sent it.
func TestSDKGCPRegionalMutateNotFoundName(t *testing.T) {
	ctx := context.Background()
	_, frc, bsc := w9Clients(t)

	cases := []struct {
		name string
		call func() error
	}{
		{"forwardingRule setLabels", func() error {
			return setRegionalLabels(ctx, frc, "nope", "", map[string]string{"a": "b"})
		}},
		{"forwardingRule patch", func() error {
			return callOp(ctx, func() (*gcpcompute.Operation, error) {
				return frc.Patch(ctx, &computepb.PatchForwardingRuleRequest{
					Project: testProject, Region: w9Region, ForwardingRule: "nope",
					ForwardingRuleResource: &computepb.ForwardingRule{NetworkTier: ptrStr("STANDARD")},
				})
			})
		}},
		{"backendService patch", func() error {
			return callOp(ctx, func() (*gcpcompute.Operation, error) {
				return bsc.Patch(ctx, &computepb.PatchRegionBackendServiceRequest{
					Project: testProject, Region: w9Region, BackendService: "nope",
					BackendServiceResource: &computepb.BackendService{Description: ptrStr("d")},
				})
			})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			assertHTTPCode(t, err, 404)

			var gerr *googleapi.Error
			if !errors.As(err, &gerr) {
				t.Fatalf("error = %v, want googleapi.Error", err)
			}

			if !strings.Contains(gerr.Message, `"nope"`) || strings.Contains(gerr.Message, w9Region) {
				t.Errorf("message = %q, want the plain name \"nope\"", gerr.Message)
			}
		})
	}
}

// TestSDKGCPForwardingRuleFingerprintScoped checks a global and a regional rule
// of the same name have distinct fingerprints, so one's fingerprint is stale
// on the other.
func TestSDKGCPForwardingRuleFingerprintScoped(t *testing.T) {
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

	g, err := gfr.Get(ctx, &computepb.GetGlobalForwardingRuleRequest{Project: testProject, ForwardingRule: "dup"})
	if err != nil {
		t.Fatalf("global Get: %v", err)
	}

	if r := getRegionalFR(ctx, t, frc, "dup"); r.GetFingerprint() == g.GetFingerprint() {
		t.Fatalf("global and regional fingerprints are both %q", g.GetFingerprint())
	}

	assertHTTPCode(t, callOp(ctx, func() (*gcpcompute.Operation, error) {
		return frc.Patch(ctx, &computepb.PatchForwardingRuleRequest{
			Project: testProject, Region: w9Region, ForwardingRule: "dup",
			ForwardingRuleResource: &computepb.ForwardingRule{NetworkTier: ptrStr("STANDARD"), Fingerprint: ptrStr(g.GetFingerprint())},
		})
	}), 412)
}

// TestSDKGCPForwardingRulePatchNoChange checks a PATCH that changes no mutable
// field leaves the fingerprint as it was, and a real change still moves it.
func TestSDKGCPForwardingRulePatchNoChange(t *testing.T) {
	ctx := context.Background()
	_, frc, _ := w9Clients(t)

	if err := insertRegionalFR(ctx, frc, &computepb.ForwardingRule{
		Name: ptrStr("fr"), PortRange: ptrStr("80"), AllowGlobalAccess: ptrBool(true),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	patch := func(fr *computepb.ForwardingRule) {
		t.Helper()

		if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
			return frc.Patch(ctx, &computepb.PatchForwardingRuleRequest{
				Project: testProject, Region: w9Region, ForwardingRule: "fr", ForwardingRuleResource: fr,
			})
		}); err != nil {
			t.Fatalf("Patch: %v", err)
		}
	}

	fp0 := getRegionalFR(ctx, t, frc, "fr").GetFingerprint()

	cases := []struct {
		name string
		body *computepb.ForwardingRule
	}{
		{"immutable only", &computepb.ForwardingRule{PortRange: ptrStr("81"), IPAddress: ptrStr("1.2.3.4")}},
		{"same allowGlobalAccess", &computepb.ForwardingRule{AllowGlobalAccess: ptrBool(true)}},
	}

	for _, tc := range cases {
		patch(tc.body)

		got := getRegionalFR(ctx, t, frc, "fr")
		if got.GetFingerprint() != fp0 || got.GetPortRange() != "80" {
			t.Errorf("%s: fingerprint %q -> %q, portRange %q", tc.name, fp0, got.GetFingerprint(), got.GetPortRange())
		}
	}

	patch(&computepb.ForwardingRule{AllowGlobalAccess: ptrBool(false)})

	if getRegionalFR(ctx, t, frc, "fr").GetFingerprint() == fp0 {
		t.Error("fingerprint unchanged after a real change")
	}
}
