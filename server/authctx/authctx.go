// Package authctx carries the authenticated caller on a request context.
//
// The AWS SigV4 authentication gate populates an AWS Principal after it verifies
// a request signature; the Azure claims-auth gate populates an AzurePrincipal
// after it validates a bearer token's claims. Handlers that need to know who is
// calling read the value back with PrincipalFrom / AzurePrincipalFrom. When
// authentication is disabled (the default) no principal is stored and the
// accessors report ok=false. The two principal types use distinct context keys,
// so the AWS and Azure gates never collide.
package authctx

import "context"

// Principal is the caller resolved from a verified request signature.
type Principal struct {
	AccessKeyID string
	UserName    string
	ARN         string
	AccountID   string
	// UserID is the caller's unique id (an IAM user's "AIDA..." value), when
	// known. Empty for a temporary STS credential, whose identity the STS
	// handler tracks separately.
	UserID string
}

type principalKey struct{}

// WithPrincipal returns a copy of ctx carrying p.
//
//nolint:gocritic // hugeParam: Principal is passed by value to keep this small, stable public signature.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal stored on ctx, or ok=false when the
// request was not authenticated.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// AzurePrincipal is the caller resolved from a validated Azure AD bearer token.
//
// cloudemu does not hold Azure AD's signing key, so it cannot cryptographically
// verify a real Azure token. These fields therefore come from the token's
// UNVERIFIED claims: the Azure gate validates the token's structure, audience,
// expiry and the presence of a principal claim, but NOT its signature. Treat
// the values as an authenticated caller's identity only under that documented
// limitation.
type AzurePrincipal struct {
	// ObjectID is the caller's Azure AD object id (the "oid" claim), when present.
	ObjectID string
	// AppID is the calling application id (the "appid" or "azp" claim), when present.
	AppID string
	// TenantID is the Azure AD tenant (the "tid" claim), when present.
	TenantID string
}

type azurePrincipalKey struct{}

// WithAzurePrincipal returns a copy of ctx carrying p.
func WithAzurePrincipal(ctx context.Context, p AzurePrincipal) context.Context {
	return context.WithValue(ctx, azurePrincipalKey{}, p)
}

// AzurePrincipalFrom returns the Azure principal stored on ctx, or ok=false when
// the request was not authenticated.
func AzurePrincipalFrom(ctx context.Context) (AzurePrincipal, bool) {
	p, ok := ctx.Value(azurePrincipalKey{}).(AzurePrincipal)
	return p, ok
}
