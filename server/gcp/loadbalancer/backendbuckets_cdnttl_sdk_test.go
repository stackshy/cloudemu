package loadbalancer_test

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
)

// TestSDKGCPBackendBucketCDNModeTTLRules covers the cdnPolicy cross-field TTL
// rules GCP enforces: USE_ORIGIN_HEADERS takes every TTL from the origin,
// FORCE_CACHE_ALL has no maxTtl, clientTtl cannot exceed maxTtl, and
// defaultTtl is capped by the 86400 default maxTtl when none is sent.
func TestSDKGCPBackendBucketCDNModeTTLRules(t *testing.T) {
	ts := newCDNServer(t, gcsBucket)
	ctx := context.Background()
	c := newBackendBucketsClient(t, ts)

	withPolicy := func(p *computepb.BackendBucketCdnPolicy) *computepb.BackendBucket {
		return &computepb.BackendBucket{Name: ptrStr("ttl-bb"), BucketName: ptrStr(gcsBucket), CdnPolicy: p}
	}

	origin := ptrStr("USE_ORIGIN_HEADERS")
	forceAll := ptrStr("FORCE_CACHE_ALL")

	cases := map[string]*computepb.BackendBucket{
		"USE_ORIGIN_HEADERS defaultTtl": withPolicy(&computepb.BackendBucketCdnPolicy{CacheMode: origin, DefaultTtl: ptrI32(60)}),
		"USE_ORIGIN_HEADERS maxTtl":     withPolicy(&computepb.BackendBucketCdnPolicy{CacheMode: origin, MaxTtl: ptrI32(60)}),
		"USE_ORIGIN_HEADERS clientTtl":  withPolicy(&computepb.BackendBucketCdnPolicy{CacheMode: origin, ClientTtl: ptrI32(60)}),
		"FORCE_CACHE_ALL maxTtl":        withPolicy(&computepb.BackendBucketCdnPolicy{CacheMode: forceAll, MaxTtl: ptrI32(600)}),
		"clientTtl>maxTtl":              withPolicy(&computepb.BackendBucketCdnPolicy{MaxTtl: ptrI32(100), ClientTtl: ptrI32(5000)}),
		"defaultTtl>default maxTtl":     withPolicy(&computepb.BackendBucketCdnPolicy{DefaultTtl: ptrI32(90000)}),
	}

	for name, bb := range cases {
		t.Run(name, func(t *testing.T) {
			err := callOp(ctx, func() (*gcpcompute.Operation, error) {
				return c.Insert(ctx, &computepb.InsertBackendBucketRequest{Project: testProject, BackendBucketResource: bb})
			})
			assertHTTPCode(t, err, 400)
		})
	}
}

// TestSDKGCPBackendBucketCDNTTLDefaults: the TTLs GCP reports for each
// cacheMode when the caller leaves them out.
func TestSDKGCPBackendBucketCDNTTLDefaults(t *testing.T) {
	ts := newCDNServer(t, gcsBucket)
	ctx := context.Background()
	c := newBackendBucketsClient(t, ts)

	type ttls struct{ def, max, client int32 }

	cases := []struct {
		name string
		mode *string
		max  *int32
		want ttls
	}{
		{"static-bb", nil, nil, ttls{3600, 86400, 3600}},
		{"static-lowmax-bb", ptrStr("CACHE_ALL_STATIC"), ptrI32(100), ttls{100, 100, 100}},
		{"force-bb", ptrStr("FORCE_CACHE_ALL"), nil, ttls{3600, 0, 3600}},
		{"origin-bb", ptrStr("USE_ORIGIN_HEADERS"), nil, ttls{0, 0, 0}},
	}

	for _, tc := range cases {
		insertBB(ctx, t, c, &computepb.BackendBucket{
			Name: ptrStr(tc.name), BucketName: ptrStr(gcsBucket), EnableCdn: ptrBool(true),
			CdnPolicy: &computepb.BackendBucketCdnPolicy{CacheMode: tc.mode, MaxTtl: tc.max},
		})

		p := getBB(ctx, t, c, tc.name).GetCdnPolicy()
		got := ttls{p.GetDefaultTtl(), p.GetMaxTtl(), p.GetClientTtl()}

		if got != tc.want {
			t.Errorf("%s: (defaultTtl, maxTtl, clientTtl) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSDKGCPBackendBucketCDNModeSwitchPatch: patching a CACHE_ALL_STATIC
// bucket (which carries defaulted TTLs) to USE_ORIGIN_HEADERS succeeds and
// clears those TTLs, while a TTL the patch itself sends is still refused.
func TestSDKGCPBackendBucketCDNModeSwitchPatch(t *testing.T) {
	ts := newCDNServer(t, gcsBucket)
	ctx := context.Background()
	c := newBackendBucketsClient(t, ts)

	insertBB(ctx, t, c, &computepb.BackendBucket{
		Name: ptrStr("switch-bb"), BucketName: ptrStr(gcsBucket), EnableCdn: ptrBool(true),
	})

	patch := func(p *computepb.BackendBucketCdnPolicy) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return c.Patch(ctx, &computepb.PatchBackendBucketRequest{
				Project: testProject, BackendBucket: "switch-bb",
				BackendBucketResource: &computepb.BackendBucket{CdnPolicy: p},
			})
		})
	}

	assertHTTPCode(t, patch(&computepb.BackendBucketCdnPolicy{
		CacheMode: ptrStr("USE_ORIGIN_HEADERS"), DefaultTtl: ptrI32(60),
	}), 400)

	if err := patch(&computepb.BackendBucketCdnPolicy{CacheMode: ptrStr("USE_ORIGIN_HEADERS")}); err != nil {
		t.Fatalf("Patch to USE_ORIGIN_HEADERS: %v", err)
	}

	p := getBB(ctx, t, c, "switch-bb").GetCdnPolicy()
	if p.GetCacheMode() != "USE_ORIGIN_HEADERS" || p.DefaultTtl != nil || p.MaxTtl != nil || p.ClientTtl != nil {
		t.Errorf("after switch: cacheMode=%q defaultTtl=%v maxTtl=%v clientTtl=%v, want USE_ORIGIN_HEADERS with no TTLs",
			p.GetCacheMode(), p.DefaultTtl, p.MaxTtl, p.ClientTtl)
	}
}
