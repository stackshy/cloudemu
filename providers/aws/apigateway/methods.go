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

	method := normalizeMethod(httpMethod)
	if !validHTTPMethod(method) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgInvalidHTTPMethod)
	}

	if _, exists := res.Methods[method]; exists {
		return nil, cerrors.New(cerrors.AlreadyExists, msgMethodExists)
	}

	mth := &driver.Method{
		HTTPMethod:        method,
		AuthorizationType: orDefault(in.AuthorizationType, "NONE"),
		APIKeyRequired:    in.APIKeyRequired,
	}
	res.Methods[method] = mth

	out := *mth

	return &out, nil
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
		PassthroughBehavior:   orDefault(in.PassthroughBehavior, "WHEN_NO_MATCH"),
		TimeoutInMillis:       orDefaultInt(in.TimeoutInMillis, defaultIntegrationTimeoutMillis),
	}
	mth.Integration = ig

	out := *ig

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

	out := *mth

	if mth.Integration != nil {
		ig := *mth.Integration
		out.Integration = &ig
	}

	return &out, nil
}
