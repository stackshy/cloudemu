package aws_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// regionHandler is a marker handler that echoes which region it belongs to.
func regionHandler(region string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, region)
	})
}

// signedFor returns a request carrying a SigV4 Authorization header whose
// credential scope names region (the only place the wire states a region).
func signedFor(region string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=AKID/20260101/"+region+"/s3/aws4_request, SignedHeaders=host, Signature=deadbeef")

	return r
}

// TestMuxDefaultAndSignedRouting proves an unsigned request routes to the
// pre-built default entry (build never runs), and a signed request for another
// region builds and routes to that region exactly once.
func TestMuxDefaultAndSignedRouting(t *testing.T) {
	var built atomic.Int64

	mux := awsserver.NewRegionMux(regionEast,
		awsserver.RegionEntry{Server: regionHandler(regionEast)},
		func(region string) awsserver.RegionEntry {
			built.Add(1)

			return awsserver.RegionEntry{Server: regionHandler(region)}
		},
	)

	// Unsigned → default region, no build.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Body.String() != regionEast {
		t.Fatalf("unsigned routed to %q, want default %q", rec.Body.String(), regionEast)
	}
	if built.Load() != 0 {
		t.Fatalf("build ran %d times for default/unsigned, want 0", built.Load())
	}

	// Signed us-west-2 → builds once, routes there.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, signedFor(regionWest))
	if rec.Body.String() != regionWest {
		t.Fatalf("signed routed to %q, want %q", rec.Body.String(), regionWest)
	}

	// A second us-west-2 request reuses the cached entry (no rebuild).
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, signedFor(regionWest))
	if built.Load() != 1 {
		t.Fatalf("build ran %d times, want 1 (region cached)", built.Load())
	}
}

// TestMuxSingleflight proves a burst of first-touch requests to a NEW region
// builds it exactly once, and that different new regions don't serialize behind
// each other.
func TestMuxSingleflight(t *testing.T) {
	var built atomic.Int64

	mux := awsserver.NewRegionMux(regionEast,
		awsserver.RegionEntry{Server: regionHandler(regionEast)},
		func(region string) awsserver.RegionEntry {
			built.Add(1)
			time.Sleep(20 * time.Millisecond) // simulate an expensive build

			return awsserver.RegionEntry{Server: regionHandler(region)}
		},
	)

	const n = 20

	var wg sync.WaitGroup

	wg.Add(n)

	for range n {
		go func() {
			defer wg.Done()

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, signedFor("ap-south-1"))
		}()
	}

	wg.Wait()

	if built.Load() != 1 {
		t.Fatalf("concurrent first-touch built the region %d times, want 1 (singleflight)", built.Load())
	}
}

// TestMuxEnumeratesLiveRegions proves LiveProviders/LiveEngines report every
// built region, and that all region providers share the same global IAM.
func TestMuxEnumeratesLiveRegions(t *testing.T) {
	base := cloudemu.NewAWS()
	globals := base.Globals()

	mux := awsserver.NewRegionMux(base.Region,
		awsserver.RegionEntry{Server: regionHandler(base.Region), Provider: base},
		func(region string) awsserver.RegionEntry {
			p := awsprovider.NewRegional(globals, config.WithRegion(region))

			return awsserver.RegionEntry{Server: regionHandler(region), Provider: p}
		},
	)

	// Only the default region is live initially.
	if got := len(mux.LiveProviders()); got != 1 {
		t.Fatalf("initial LiveProviders = %d, want 1 (default only)", got)
	}

	// Touch two more regions.
	w := mux.GetOrCreate(regionWest)
	_ = mux.GetOrCreate("eu-west-1")

	live := mux.LiveProviders()
	if len(live) != 3 {
		t.Fatalf("LiveProviders = %d, want 3", len(live))
	}
	if len(mux.LiveEngines()) != 3 {
		t.Fatalf("LiveEngines = %d, want 3", len(mux.LiveEngines()))
	}

	// Every region shares the same global IAM.
	if w.IAM != base.IAM {
		t.Error("us-west-2 provider does not share the default's IAM")
	}

	// GetOrCreate("") maps to the default region and returns the base provider.
	if mux.GetOrCreate("") != base {
		t.Error(`GetOrCreate("") did not return the default-region provider`)
	}
}
