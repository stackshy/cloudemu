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

	if in.IntegrationType == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "IntegrationType is required")
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
	}
	ad.integrations[ig.IntegrationID] = ig

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

// GetIntegrations lists an API's Integrations.
func (m *Mock) GetIntegrations(_ context.Context, apiID string) ([]driver.Integration, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := make([]driver.Integration, 0, len(ad.integrations))
	for _, ig := range ad.integrations {
		out = append(out, copyIntegration(ig))
	}

	return out, nil
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

	setString(&ig.IntegrationType, in.IntegrationType)
	setString(&ig.IntegrationURI, in.IntegrationURI)
	setString(&ig.IntegrationMethod, in.IntegrationMethod)
	setString(&ig.ConnectionType, in.ConnectionType)
	setString(&ig.PayloadFormatVersion, in.PayloadFormatVersion)
	setString(&ig.Description, in.Description)

	if in.TimeoutInMillis != nil {
		ig.TimeoutInMillis = *in.TimeoutInMillis
	}

	if in.RequestParameters != nil {
		ig.RequestParameters = copyStrMap(in.RequestParameters)
	}

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

	if _, ok := ad.integrations[integrationID]; !ok {
		return cerrors.Newf(cerrors.NotFound, "Invalid integration identifier specified %s", integrationID)
	}

	delete(ad.integrations, integrationID)

	return nil
}

// copyIntegration returns a deep copy of an Integration.
func copyIntegration(i *driver.Integration) driver.Integration {
	out := *i
	out.RequestParameters = copyStrMap(i.RequestParameters)

	return out
}
