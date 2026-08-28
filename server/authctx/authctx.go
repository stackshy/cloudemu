// Package authctx carries the authenticated caller (the resolved IAM principal)
// on a request context. The AWS SigV4 authentication gate populates it after it
// verifies a request signature; handlers that need to know who is calling read
// it back with PrincipalFrom. When authentication is disabled (the default) no
// principal is stored and PrincipalFrom reports ok=false.
package authctx

import "context"

// Principal is the caller resolved from a verified request signature.
type Principal struct {
	AccessKeyID string
	UserName    string
	ARN         string
	AccountID   string
}

type principalKey struct{}

// WithPrincipal returns a copy of ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal stored on ctx, or ok=false when the
// request was not authenticated.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
