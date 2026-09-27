package apigatewayv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// CreateIntegration creates an Integration on an API.
func (m *Mock) CreateIntegration(
	_ context.Context, apiID string, in *driver.CreateIntegrationInput,
) (*driver.Integration, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	ig := &driver.Integration{
		IntegrationID: genID(), IntegrationType: in.IntegrationType,
		IntegrationURI:       in.IntegrationURI,
		IntegrationMethod:    in.IntegrationMethod,
		ConnectionType:       orDefault(in.ConnectionType, defaultConnectionType),
		PayloadFormatVersion: orDefault(in.PayloadFormatVersion, defaultPayloadFormat),
		TimeoutInMillis:      integrationTimeout(ad, in.TimeoutInMillis),
		Description:          in.Description,
		RequestParameters:    copyStrMap(in.RequestParameters),
		CredentialsArn:       in.CredentialsArn,

		RequestTemplates:            copyStrMap(in.RequestTemplates),
		TemplateSelectionExpression: in.TemplateSelectionExpression,
		PassthroughBehavior:         in.PassthroughBehavior,
	}

	if err := validateIntegration(ad.api.ProtocolType, ig); err != nil {
		return nil, err
	}

	ad.integrations[ig.IntegrationID] = ig
	m.autoDeploy(ad)

	out := copyIntegration(ig)

	return &out, nil
}

// integrationTimeout returns the requested timeout, or the protocol default
// (30s HTTP, 29s WebSocket) when unset. ad must be held.
func integrationTimeout(ad *apiData, requested int) int {
	if requested != 0 {
		return requested
	}

	if ad.api.ProtocolType == driver.ProtocolWebSocket {
		return defaultWebSocketTimeoutMillis
	}

	return defaultHTTPTimeoutMillis
}

// GetIntegration returns a single Integration.
func (m *Mock) GetIntegration(_ context.Context, apiID, integrationID string) (*driver.Integration, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	ig, ok := ad.integrations[integrationID]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid integration identifier specified %s", integrationID)
	}

	out := copyIntegration(ig)

	return &out, nil
}

// GetIntegrations lists one page of an API's Integrations, ordered by id.
func (m *Mock) GetIntegrations(
	_ context.Context, apiID string, page *driver.PageInput,
) ([]driver.Integration, string, error) {
	return listPage(m, apiID, func(ad *apiData) map[string]*driver.Integration { return ad.integrations }, copyIntegration,
		func(a, b driver.Integration) bool { return a.IntegrationID < b.IntegrationID }, page)
}

// UpdateIntegration applies the non-nil fields of in to a stored Integration.
func (m *Mock) UpdateIntegration(
	_ context.Context, apiID, integrationID string, in *driver.UpdateIntegrationInput,
) (*driver.Integration, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	ig, ok := ad.integrations[integrationID]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid integration identifier specified %s", integrationID)
	}

	next := copyIntegration(ig)
	setString(&next.IntegrationType, in.IntegrationType)
	setString(&next.IntegrationURI, in.IntegrationURI)
	setString(&next.IntegrationMethod, in.IntegrationMethod)
	setString(&next.ConnectionType, in.ConnectionType)
	setString(&next.PayloadFormatVersion, in.PayloadFormatVersion)
	setString(&next.Description, in.Description)
	setString(&next.CredentialsArn, in.CredentialsArn)
	setString(&next.TemplateSelectionExpression, in.TemplateSelectionExpression)
	setString(&next.PassthroughBehavior, in.PassthroughBehavior)

	if in.RequestTemplates != nil {
		next.RequestTemplates = copyStrMap(in.RequestTemplates)
	}

	if in.TimeoutInMillis != nil {
		next.TimeoutInMillis = *in.TimeoutInMillis
	}

	if in.RequestParameters != nil {
		next.RequestParameters = copyStrMap(in.RequestParameters)
	}

	if err := validateIntegration(ad.api.ProtocolType, &next); err != nil {
		return nil, err
	}

	*ig = next

	m.autoDeploy(ad)

	out := copyIntegration(ig)

	return &out, nil
}

// DeleteIntegration removes an Integration.
func (m *Mock) DeleteIntegration(_ context.Context, apiID, integrationID string) error {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	ig, ok := ad.integrations[integrationID]
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "Invalid integration identifier specified %s", integrationID)
	}

	if ig.APIGatewayManaged {
		return badRequest("Cannot delete an integration managed by API Gateway")
	}

	delete(ad.integrations, integrationID)
	m.autoDeploy(ad)

	return nil
}

// copyIntegration returns a deep copy of an Integration.
func copyIntegration(i *driver.Integration) driver.Integration {
	out := *i
	out.RequestParameters = copyStrMap(i.RequestParameters)
	out.RequestTemplates = copyStrMap(i.RequestTemplates)

	return out
}
