package apigateway

import (
	"context"
	"sort"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

var _ driver.Authorizers = (*Mock)(nil)

const (
	msgAuthorizerNotFound = "Invalid Authorizer identifier specified"
	msgAuthorizerName     = "Authorizer name is required"
	msgAuthorizerType     = "Invalid authorizer type specified: must be TOKEN, REQUEST or COGNITO_USER_POOLS"
	msgAuthorizerURI      = "Authorizer URI is required for a TOKEN or REQUEST authorizer"
	msgAuthorizerProvider = "Provider ARNs are required for a COGNITO_USER_POOLS authorizer"
	msgAuthorizerTTL      = "Authorizer result TTL must be between 0 and 3600 seconds"
	msgAuthorizerIdentity = "An identity source is required for a REQUEST authorizer with a result TTL"

	defaultAuthorizerTTL = 300
	maxAuthorizerTTL     = 3600
	defaultTokenSource   = "method.request.header.Authorization"
)

// CreateAuthorizer adds an authorizer to a REST API.
func (m *Mock) CreateAuthorizer(
	_ context.Context, restAPIID string, in *driver.CreateAuthorizerInput,
) (*driver.Authorizer, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	az := &driver.Authorizer{
		ID: genShortID(), Name: in.Name, Type: in.Type, ProviderARNs: copyStrSlice(in.ProviderARNs),
		AuthType: in.AuthType, AuthorizerURI: in.AuthorizerURI, AuthorizerCredentials: in.AuthorizerCredentials,
		IdentitySource: in.IdentitySource, IdentityValidationExpression: in.IdentityValidationExpression,
		AuthorizerResultTTLInSeconds: copyIntPointer(in.AuthorizerResultTTLInSeconds),
	}

	if err := normalizeAuthorizer(az); err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	ad.authorizers[az.ID] = az
	out := copyAuthorizer(az)

	return &out, nil
}

// normalizeAuthorizer validates an authorizer's type-dependent members and
// applies the defaults (identity source and result TTL).
func normalizeAuthorizer(az *driver.Authorizer) error {
	if az.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, msgAuthorizerName)
	}

	if err := validateAuthorizerKind(az); err != nil {
		return err
	}

	if ttl := az.AuthorizerResultTTLInSeconds; ttl != nil && (*ttl < 0 || *ttl > maxAuthorizerTTL) {
		return cerrors.New(cerrors.InvalidArgument, msgAuthorizerTTL)
	}

	if az.Type == driver.AuthorizerRequest && az.IdentitySource == "" &&
		az.AuthorizerResultTTLInSeconds != nil && *az.AuthorizerResultTTLInSeconds != 0 {
		return cerrors.New(cerrors.InvalidArgument, msgAuthorizerIdentity)
	}

	applyAuthorizerDefaults(az)

	return nil
}

// validateAuthorizerKind checks the members each authorizer type requires.
func validateAuthorizerKind(az *driver.Authorizer) error {
	switch az.Type {
	case driver.AuthorizerToken, driver.AuthorizerRequest:
		if az.AuthorizerURI == "" {
			return cerrors.New(cerrors.InvalidArgument, msgAuthorizerURI)
		}
	case driver.AuthorizerCognito:
		if len(az.ProviderARNs) == 0 {
			return cerrors.New(cerrors.InvalidArgument, msgAuthorizerProvider)
		}
	default:
		return cerrors.New(cerrors.InvalidArgument, msgAuthorizerType)
	}

	return nil
}

// applyAuthorizerDefaults fills the identity source (TOKEN and Cognito) and the
// result TTL (Lambda authorizers) API Gateway defaults.
func applyAuthorizerDefaults(az *driver.Authorizer) {
	if az.Type != driver.AuthorizerRequest && az.IdentitySource == "" {
		az.IdentitySource = defaultTokenSource
	}

	if az.Type != driver.AuthorizerCognito && az.AuthorizerResultTTLInSeconds == nil {
		ttl := defaultAuthorizerTTL
		az.AuthorizerResultTTLInSeconds = &ttl
	}
}

// GetAuthorizer returns one authorizer.
func (m *Mock) GetAuthorizer(_ context.Context, restAPIID, id string) (*driver.Authorizer, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	az, ok := ad.authorizers[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgAuthorizerNotFound)
	}

	out := copyAuthorizer(az)

	return &out, nil
}

// GetAuthorizers lists a REST API's authorizers ordered by name then id.
func (m *Mock) GetAuthorizers(_ context.Context, restAPIID string, page driver.PageInput) (*driver.AuthorizerPage, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()

	all := make([]driver.Authorizer, 0, len(ad.authorizers))
	for _, az := range ad.authorizers {
		all = append(all, copyAuthorizer(az))
	}

	ad.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}

		return all[i].ID < all[j].ID
	})

	items, next, err := pageOf(all, page)
	if err != nil {
		return nil, err
	}

	return &driver.AuthorizerPage{Items: items, Position: next}, nil
}

// UpdateAuthorizer applies a patchOperations document and re-validates the
// result.
func (m *Mock) UpdateAuthorizer(
	_ context.Context, restAPIID, id string, ops []driver.PatchOperation,
) (*driver.Authorizer, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	az, ok := ad.authorizers[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgAuthorizerNotFound)
	}

	upd := copyAuthorizer(az)

	for _, op := range ops {
		if err := applyAuthorizerPatch(&upd, op); err != nil {
			return nil, err
		}
	}

	if err := normalizeAuthorizer(&upd); err != nil {
		return nil, err
	}

	for key := range ad.authCache {
		delete(ad.authCache, key)
	}

	*az = upd
	out := copyAuthorizer(az)

	return &out, nil
}

func applyAuthorizerPatch(az *driver.Authorizer, op driver.PatchOperation) error {
	if field := authorizerStringField(az, op.Path); field != nil {
		*field = op.Value

		return nil
	}

	switch op.Path {
	case "/authorizerResultTtlInSeconds":
		n, err := strconv.Atoi(op.Value)
		if err != nil {
			return cerrors.New(cerrors.InvalidArgument, msgAuthorizerTTL)
		}

		az.AuthorizerResultTTLInSeconds = &n
	case "/providerARNs":
		az.ProviderARNs = patchStringSlice(az.ProviderARNs, op.Op, op.Value)
	default:
		return invalidPatchPath(op, pathName, pathType, "/authType", "/authorizerUri", "/authorizerCredentials",
			"/identitySource", "/identityValidationExpression", "/authorizerResultTtlInSeconds", "/providerARNs")
	}

	return nil
}

// authorizerStringField returns the string member a patch path names, or nil.
func authorizerStringField(az *driver.Authorizer, path string) *string {
	switch path {
	case pathName:
		return &az.Name
	case pathType:
		return &az.Type
	case "/authType":
		return &az.AuthType
	case "/authorizerUri":
		return &az.AuthorizerURI
	case "/authorizerCredentials":
		return &az.AuthorizerCredentials
	case "/identitySource":
		return &az.IdentitySource
	case "/identityValidationExpression":
		return &az.IdentityValidationExpression
	default:
		return nil
	}
}

// DeleteAuthorizer removes an authorizer. As with real API Gateway the delete is
// refused (BadRequestException) while a method still uses it.
func (m *Mock) DeleteAuthorizer(_ context.Context, restAPIID, id string) error {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.authorizers[id]; !ok {
		return cerrors.New(cerrors.NotFound, msgAuthorizerNotFound)
	}

	if methodUsing(ad, func(mth *driver.Method) bool {
		// A method switched to NONE or AWS_IAM keeps a stale id that nothing uses.
		return mth.AuthorizerID == id && (mth.AuthorizationType == authTypeCustom || mth.AuthorizationType == authTypeCognito)
	}) {
		return cerrors.New(cerrors.InvalidArgument, "Authorizer is still in use by a method; remove it from the method first")
	}

	delete(ad.authorizers, id)

	return nil
}

func copyAuthorizer(az *driver.Authorizer) driver.Authorizer {
	out := *az
	out.ProviderARNs = copyStrSlice(az.ProviderARNs)
	out.AuthorizerResultTTLInSeconds = copyIntPointer(az.AuthorizerResultTTLInSeconds)

	return out
}
