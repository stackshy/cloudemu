// Package regionctx carries the AWS region a caller actually addressed on a
// request context, so in-memory backends stamp resources and ARNs with the
// region the client used rather than the emulator's fixed default.
//
// The AWS query/REST wire protocols carry no region parameter; the only place a
// caller states which region it believes it is talking to is the SigV4
// credential scope (Credential=AKID/DATE/<REGION>/<SERVICE>/aws4_request). The
// server derives that region once, in a pre-dispatch hook, and stamps it here;
// every driver method then reads it back with RegionOr, falling back to the
// configured default when the request carried no region (an unsigned request,
// or the typed Go API with no HTTP request at all). That fallback keeps the
// library path and all existing default-region behavior byte-for-byte
// identical.
package regionctx

import "context"

type regionKey struct{}

// WithRegion returns a copy of ctx carrying region. An empty region is not
// stored, so RegionOr falls back to the caller's default rather than an empty
// string.
func WithRegion(ctx context.Context, region string) context.Context {
	if region == "" {
		return ctx
	}

	return context.WithValue(ctx, regionKey{}, region)
}

// RegionOr returns the request region stamped on ctx, or fallback when none is
// present. Backends pass their configured default (opts.Region) as fallback so
// callers with no request region keep the existing behavior.
func RegionOr(ctx context.Context, fallback string) string {
	if r, ok := ctx.Value(regionKey{}).(string); ok && r != "" {
		return r
	}

	return fallback
}
