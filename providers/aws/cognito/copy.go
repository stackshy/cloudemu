package cognito

import "github.com/stackshy/cloudemu/v2/services/cognito/driver"

// The stored driver values carry slice, pointer, and map members, so every store
// read/write deep-copies to keep callers from aliasing internal state.

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyStringSlice(in []string) []string {
	if in == nil {
		return nil
	}

	out := make([]string, len(in))
	copy(out, in)

	return out
}

func copyInt32Ptr(n *int32) *int32 {
	if n == nil {
		return nil
	}

	v := *n

	return &v
}

func copyStringConstraints(in *driver.StringAttributeConstraints) *driver.StringAttributeConstraints {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func copyNumberConstraints(in *driver.NumberAttributeConstraints) *driver.NumberAttributeConstraints {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func copySchemaAttributes(in []driver.SchemaAttribute) []driver.SchemaAttribute {
	if in == nil {
		return nil
	}

	out := make([]driver.SchemaAttribute, len(in))

	for i, a := range in {
		a.StringAttributeConstraints = copyStringConstraints(a.StringAttributeConstraints)
		a.NumberAttributeConstraints = copyNumberConstraints(a.NumberAttributeConstraints)
		out[i] = a
	}

	return out
}

func copyTokenValidityUnits(in *driver.TokenValidityUnits) *driver.TokenValidityUnits {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

// copyUserPool deep-copies a user pool. Taken by value so it satisfies the
// func(V) V deep-copy callback signature used across the store and snapshot.
//
//nolint:gocritic // hugeParam: value signature required by the func(V) V copy callback
func copyUserPool(in driver.UserPool) driver.UserPool {
	out := in
	out.SoftwareTokenMfaConfig = copySoftwareTokenMfa(in.SoftwareTokenMfaConfig)
	out.SmsMfaConfig = copySmsMfa(in.SmsMfaConfig)
	out.SchemaAttributes = copySchemaAttributes(in.SchemaAttributes)
	out.AutoVerifiedAttributes = copyStringSlice(in.AutoVerifiedAttributes)
	out.AliasAttributes = copyStringSlice(in.AliasAttributes)
	out.UsernameAttributes = copyStringSlice(in.UsernameAttributes)
	out.Tags = copyStringMap(in.Tags)

	return out
}

// copyUserPoolClient deep-copies an app client. Taken by value so it satisfies
// the func(V) V deep-copy callback signature used across the store and snapshot.
//
//nolint:gocritic // hugeParam: value signature required by the func(V) V copy callback
func copyUserPoolClient(in driver.UserPoolClient) driver.UserPoolClient {
	out := in
	out.AccessTokenValidity = copyInt32Ptr(in.AccessTokenValidity)
	out.IDTokenValidity = copyInt32Ptr(in.IDTokenValidity)
	out.TokenValidityUnits = copyTokenValidityUnits(in.TokenValidityUnits)
	out.ExplicitAuthFlows = copyStringSlice(in.ExplicitAuthFlows)
	out.CallbackURLs = copyStringSlice(in.CallbackURLs)
	out.LogoutURLs = copyStringSlice(in.LogoutURLs)
	out.AllowedOAuthFlows = copyStringSlice(in.AllowedOAuthFlows)
	out.AllowedOAuthScopes = copyStringSlice(in.AllowedOAuthScopes)
	out.SupportedIdentityProviders = copyStringSlice(in.SupportedIdentityProviders)
	out.ReadAttributes = copyStringSlice(in.ReadAttributes)
	out.WriteAttributes = copyStringSlice(in.WriteAttributes)

	return out
}

// copyUserPoolDomain deep-copies a domain. It holds no reference members, so a
// value copy suffices; the wrapper keeps a uniform copy vocabulary.
//
//nolint:gocritic // hugeParam: value signature required by the func(V) V copy callback
func copyUserPoolDomain(in driver.UserPoolDomain) driver.UserPoolDomain { return in }
