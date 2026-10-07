package apigateway

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// PutMethod configures an HTTP method (or "ANY") on a resource.
func (m *Mock) PutMethod(
	_ context.Context, restAPIID, resourceID, httpMethod string, in driver.PutMethodInput,
) (*driver.Method, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	res, ok := ad.resources[resourceID]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgResourceNotFound)
	}

	if err := validateMethodRequestParams(in.RequestParameters); err != nil {
		return nil, err
	}

	method := normalizeMethod(httpMethod)
	if !validHTTPMethod(method) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgInvalidHTTPMethod)
	}

	if _, exists := res.Methods[method]; exists {
		return nil, cerrors.New(cerrors.AlreadyExists, msgMethodExists)
	}

	authType := orDefault(in.AuthorizationType, "NONE")
	if !validAuthorizationType(authType) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgAuthorizationType)
	}

	if err := checkMethodRefs(ad, authType, &in); err != nil {
		return nil, err
	}

	mth := &driver.Method{
		HTTPMethod:          method,
		AuthorizationType:   authType,
		APIKeyRequired:      in.APIKeyRequired,
		OperationName:       in.OperationName,
		RequestParameters:   copyBoolMap(in.RequestParameters),
		RequestModels:       copyStrMap(in.RequestModels),
		AuthorizerID:        in.AuthorizerID,
		RequestValidatorID:  in.RequestValidatorID,
		AuthorizationScopes: copyStrSlice(in.AuthorizationScopes),
	}
	res.Methods[method] = mth

	out := copyMethod(mth)

	return &out, nil
}

// checkMethodRefs validates the authorizer, request validator and models a
// method names: they must exist in the API, and a CUSTOM or COGNITO_USER_POOLS
// method needs an authorizer of the matching kind. The caller holds ad.mu.
func checkMethodRefs(ad *apiData, authType string, in *driver.PutMethodInput) error {
	if authType == authTypeCustom || authType == authTypeCognito {
		az, ok := ad.authorizers[in.AuthorizerID]
		if !ok || (authType == authTypeCognito) != (az.Type == driver.AuthorizerCognito) {
			return cerrors.New(cerrors.InvalidArgument, msgMethodAuthorizer)
		}
	}

	if _, ok := ad.validators[in.RequestValidatorID]; in.RequestValidatorID != "" && !ok {
		return cerrors.New(cerrors.NotFound, msgValidatorNotFound)
	}

	return checkModelRefs(ad, in.RequestModels)
}

// checkModelRefs requires every model a method names to exist in the API.
func checkModelRefs(ad *apiData, models map[string]string) error {
	for _, ct := range sortedKeys(models) {
		if _, ok := ad.models[models[ct]]; !ok {
			return cerrors.Newf(cerrors.InvalidArgument,
				"Invalid model specified: Validation Result: warnings: [], errors: [Invalid model name specified: %s]", models[ct])
		}
	}

	return nil
}

// checkConnection validates an integration's connection: INTERNET (the default)
// or a VPC_LINK that exists. Lock order: ad.mu is held by the caller, so the VPC
// link lookup takes regionMu without a nested API lock.
func (m *Mock) checkConnection(ig *driver.Integration) error {
	switch ig.ConnectionType {
	case connectionInternet:
		return nil
	case connectionVpcLink:
		m.regionMu.RLock()
		_, ok := m.vpcLinks[ig.ConnectionID]
		m.regionMu.RUnlock()

		if !ok {
			return cerrors.New(cerrors.InvalidArgument, "Invalid VPC link identifier specified for the integration")
		}

		return nil
	default:
		return cerrors.New(cerrors.InvalidArgument, "Invalid connection type: must be INTERNET or VPC_LINK")
	}
}

// GetMethod returns a resource's configured method.
func (m *Mock) GetMethod(_ context.Context, restAPIID, resourceID, httpMethod string) (*driver.Method, error) {
	mth, err := m.lookupMethod(restAPIID, resourceID, httpMethod)
	if err != nil {
		return nil, err
	}

	return mth, nil
}

// DeleteMethod removes a resource's configured method (and its integration,
// if any).
func (m *Mock) DeleteMethod(_ context.Context, restAPIID, resourceID, httpMethod string) error {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	res, ok := ad.resources[resourceID]
	if !ok {
		return cerrors.New(cerrors.NotFound, msgResourceNotFound)
	}

	method := normalizeMethod(httpMethod)
	if _, ok := res.Methods[method]; !ok {
		return cerrors.New(cerrors.NotFound, msgMethodNotFound)
	}

	delete(res.Methods, method)

	return nil
}

// PutIntegration wires a method to a backend integration (AWS_PROXY/AWS to a
// Lambda invocation ARN, or another supported type).
func (m *Mock) PutIntegration(
	_ context.Context, restAPIID, resourceID, httpMethod string, in driver.PutIntegrationInput,
) (*driver.Integration, error) {
	if err := validateIntegration(&in); err != nil {
		return nil, err
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	res, ok := ad.resources[resourceID]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgResourceNotFound)
	}

	method := normalizeMethod(httpMethod)

	mth, ok := res.Methods[method]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgMethodNotFound)
	}

	ig := &driver.Integration{
		Type:                  in.Type,
		IntegrationHTTPMethod: in.IntegrationHTTPMethod,
		URI:                   in.URI,
		PassthroughBehavior:   orDefault(in.PassthroughBehavior, driver.PassthroughWhenNoMatch),
		TimeoutInMillis:       orDefaultInt(in.TimeoutInMillis, defaultIntegrationTimeoutMillis),
		Credentials:           in.Credentials,
		RequestParameters:     copyStrMap(in.RequestParameters),
		RequestTemplates:      copyStrMap(in.RequestTemplates),
		ContentHandling:       in.ContentHandling,
		CacheNamespace:        orDefault(in.CacheNamespace, resourceID),
		CacheKeyParameters:    append([]string(nil), in.CacheKeyParameters...),
		ConnectionType:        orDefault(in.ConnectionType, connectionInternet),
		ConnectionID:          in.ConnectionID,
	}

	if err := m.checkConnection(ig); err != nil {
		return nil, err
	}

	if err := validateIntegrationSettings(ig, mth.RequestParameters); err != nil {
		return nil, err
	}

	mth.Integration = ig

	out := copyIntegration(ig)

	return &out, nil
}

// GetIntegration returns a method's integration.
func (m *Mock) GetIntegration(_ context.Context, restAPIID, resourceID, httpMethod string) (*driver.Integration, error) {
	mth, err := m.lookupMethod(restAPIID, resourceID, httpMethod)
	if err != nil {
		return nil, err
	}

	if mth.Integration == nil {
		return nil, cerrors.New(cerrors.NotFound, msgIntegrationNotFound)
	}

	return mth.Integration, nil
}

// DeleteIntegration removes a method's integration.
func (m *Mock) DeleteIntegration(_ context.Context, restAPIID, resourceID, httpMethod string) error {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	res, ok := ad.resources[resourceID]
	if !ok {
		return cerrors.New(cerrors.NotFound, msgResourceNotFound)
	}

	method := normalizeMethod(httpMethod)

	mth, ok := res.Methods[method]
	if !ok {
		return cerrors.New(cerrors.NotFound, msgMethodNotFound)
	}

	if mth.Integration == nil {
		return cerrors.New(cerrors.NotFound, msgIntegrationNotFound)
	}

	mth.Integration = nil

	return nil
}

// lookupMethod resolves a method and returns a deep copy safe to use after the
// lock is released.
func (m *Mock) lookupMethod(restAPIID, resourceID, httpMethod string) (*driver.Method, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	res, ok := ad.resources[resourceID]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgResourceNotFound)
	}

	mth, ok := res.Methods[normalizeMethod(httpMethod)]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgMethodNotFound)
	}

	out := copyMethod(mth)

	return &out, nil
}
