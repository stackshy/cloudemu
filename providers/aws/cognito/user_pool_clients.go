package cognito

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// App-client defaults for an API-created client.
const (
	defaultRefreshTokenValidity = 30
	defaultAuthSessionValidity  = 3
)

// defaultExplicitAuthFlows is the auth-flow set Cognito applies when a client is
// created without any explicit flows.
//
//nolint:gochecknoglobals // static default-flow definition
var defaultExplicitAuthFlows = []string{
	"ALLOW_REFRESH_TOKEN_AUTH",
	"ALLOW_USER_SRP_AUTH",
	"ALLOW_CUSTOM_AUTH",
}

// CreateUserPoolClient creates an app client, generating its id and (only when
// GenerateSecret is set) its secret, and materializing the token-validity,
// auth-flow, and existence-error defaults.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface
func (m *Mock) CreateUserPoolClient(_ context.Context, in driver.CreateUserPoolClientInput) (*driver.UserPoolClient, error) {
	if in.ClientName == "" {
		return nil, invalidParameter("ClientName is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.userPools.Has(in.UserPoolID) {
		return nil, resourceNotFound("User pool %s does not exist.", in.UserPoolID)
	}

	now := m.now()
	client := buildClient(in)
	client.ClientID = newClientID()
	client.CreationDate = now
	client.LastModifiedDate = now

	if in.GenerateSecret {
		client.ClientSecret = newClientSecret()
	}

	m.clients.Set(clientKey(in.UserPoolID, client.ClientID), copyUserPoolClient(client))

	out := copyUserPoolClient(client)

	return &out, nil
}

// DescribeUserPoolClient returns a deep copy of an app client.
func (m *Mock) DescribeUserPoolClient(_ context.Context, userPoolID, clientID string) (*driver.UserPoolClient, error) {
	client, ok := m.clients.Get(clientKey(userPoolID, clientID))
	if !ok {
		return nil, resourceNotFound("App client %s does not exist.", clientID)
	}

	out := copyUserPoolClient(client)

	return &out, nil
}

// UpdateUserPoolClient replaces the client's settings and returns the result.
// The client secret and creation date are preserved across an update.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface
func (m *Mock) UpdateUserPoolClient(_ context.Context, in driver.CreateUserPoolClientInput) (*driver.UserPoolClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clientKey(in.UserPoolID, in.ClientID)

	existing, ok := m.clients.Get(key)
	if !ok {
		return nil, resourceNotFound("App client %s does not exist.", in.ClientID)
	}

	now := m.now()
	client := buildClient(in)
	client.ClientID = existing.ClientID
	client.ClientSecret = existing.ClientSecret
	client.CreationDate = existing.CreationDate
	client.LastModifiedDate = now

	if client.ClientName == "" {
		client.ClientName = existing.ClientName
	}

	m.clients.Set(key, copyUserPoolClient(client))

	out := copyUserPoolClient(client)

	return &out, nil
}

// DeleteUserPoolClient removes an app client.
func (m *Mock) DeleteUserPoolClient(_ context.Context, userPoolID, clientID string) error {
	if !m.clients.Delete(clientKey(userPoolID, clientID)) {
		return resourceNotFound("App client %s does not exist.", clientID)
	}

	return nil
}

// ListUserPoolClients returns client descriptions in a pool, sorted by client id.
func (m *Mock) ListUserPoolClients(
	_ context.Context, userPoolID string, page driver.Pagination,
) ([]driver.UserPoolClientDescription, string, error) {
	all := make([]driver.UserPoolClientDescription, 0)

	for _, key := range sortedKeys(m.clients.Keys()) {
		client, ok := m.clients.Get(key)
		if !ok || client.UserPoolID != userPoolID {
			continue
		}

		all = append(all, driver.UserPoolClientDescription{
			ClientID:   client.ClientID,
			ClientName: client.ClientName,
			UserPoolID: client.UserPoolID,
		})
	}

	return paginate(all, page)
}

// buildClient materializes a client's full settings from the input, applying the
// Cognito defaults for the omitted fields. It does not set id, secret, or
// timestamps — the create/update callers own those.
//
//nolint:gocritic // hugeParam: input taken by value to match the driver call sites
func buildClient(in driver.CreateUserPoolClientInput) driver.UserPoolClient {
	return driver.UserPoolClient{
		ClientName:                      in.ClientName,
		UserPoolID:                      in.UserPoolID,
		RefreshTokenValidity:            int32OrDefault(in.RefreshTokenValidity, defaultRefreshTokenValidity),
		AccessTokenValidity:             copyInt32Ptr(in.AccessTokenValidity),
		IDTokenValidity:                 copyInt32Ptr(in.IDTokenValidity),
		TokenValidityUnits:              copyTokenValidityUnits(in.TokenValidityUnits),
		ExplicitAuthFlows:               resolveExplicitAuthFlows(in.ExplicitAuthFlows),
		AuthSessionValidity:             int32OrDefault(in.AuthSessionValidity, defaultAuthSessionValidity),
		EnableTokenRevocation:           boolOrDefault(in.EnableTokenRevocation, true),
		PreventUserExistenceErrors:      orDefault(in.PreventUserExistenceErrors, driver.PreventUserExistenceErrorsLegacy),
		CallbackURLs:                    copyStringSlice(in.CallbackURLs),
		LogoutURLs:                      copyStringSlice(in.LogoutURLs),
		DefaultRedirectURI:              in.DefaultRedirectURI,
		AllowedOAuthFlows:               copyStringSlice(in.AllowedOAuthFlows),
		AllowedOAuthScopes:              copyStringSlice(in.AllowedOAuthScopes),
		AllowedOAuthFlowsUserPoolClient: in.AllowedOAuthFlowsUserPoolClient,
		SupportedIdentityProviders:      copyStringSlice(in.SupportedIdentityProviders),
		ReadAttributes:                  copyStringSlice(in.ReadAttributes),
		WriteAttributes:                 copyStringSlice(in.WriteAttributes),
	}
}

// resolveExplicitAuthFlows returns the caller's flows, or the 3-flow default when
// none are supplied.
func resolveExplicitAuthFlows(in []string) []string {
	if len(in) == 0 {
		return copyStringSlice(defaultExplicitAuthFlows)
	}

	return copyStringSlice(in)
}

// int32OrDefault returns *v when non-nil, else def.
func int32OrDefault(v *int32, def int32) int32 {
	if v == nil {
		return def
	}

	return *v
}

// boolOrDefault returns *v when non-nil, else def.
func boolOrDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}

	return *v
}
