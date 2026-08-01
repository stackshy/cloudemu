package bedrock

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// --- Marketplace model endpoints ---

// CreateMarketplaceModelEndpoint deploys a marketplace model endpoint. The
// endpoint ARN is a SageMaker endpoint ARN and the endpoint is REGISTERED and
// InService immediately.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateMarketplaceModelEndpoint(
	_ context.Context, cfg driver.MarketplaceEndpointConfig,
) (*driver.MarketplaceEndpoint, error) {
	switch {
	case cfg.EndpointName == "":
		return nil, errors.New(errors.InvalidArgument, "endpointName is required")
	case cfg.ModelSourceIdentifier == "":
		return nil, errors.New(errors.InvalidArgument, "modelSourceIdentifier is required")
	case len(cfg.EndpointConfig) == 0:
		return nil, errors.New(errors.InvalidArgument, "endpointConfig is required")
	}

	now := m.now()
	arn := idgen.AWSARN("sagemaker", m.opts.Region, m.opts.AccountID, "endpoint/"+cfg.EndpointName)

	if m.marketplaceEndpoints.Has(arn) {
		return nil, errors.Newf(errors.AlreadyExists, "marketplace model endpoint %q already exists", cfg.EndpointName)
	}

	endpoint := &driver.MarketplaceEndpoint{
		EndpointARN:           arn,
		ModelSourceIdentifier: cfg.ModelSourceIdentifier,
		EndpointConfig:        copyBytes(cfg.EndpointConfig),
		EndpointStatus:        driver.MarketplaceEndpointStatusInService,
		Status:                driver.MarketplaceEndpointStatusRegistered,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	m.marketplaceEndpoints.Set(arn, endpoint)
	m.setTags(arn, m.tagsFromMap(cfg.Tags))

	result := cloneMarketplaceEndpoint(endpoint)

	return &result, nil
}

// GetMarketplaceModelEndpoint returns a marketplace model endpoint by ARN.
func (m *Mock) GetMarketplaceModelEndpoint(_ context.Context, endpointARN string) (*driver.MarketplaceEndpoint, error) {
	endpoint, ok := m.marketplaceEndpoints.Get(endpointARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "marketplace model endpoint %q not found", endpointARN)
	}

	result := cloneMarketplaceEndpoint(endpoint)

	return &result, nil
}

// ListMarketplaceModelEndpoints lists all marketplace model endpoints.
func (m *Mock) ListMarketplaceModelEndpoints(_ context.Context) ([]driver.MarketplaceEndpoint, error) {
	all := m.marketplaceEndpoints.SortedValues()
	out := make([]driver.MarketplaceEndpoint, 0, len(all))

	for _, endpoint := range all {
		out = append(out, cloneMarketplaceEndpoint(endpoint))
	}

	return out, nil
}

// UpdateMarketplaceModelEndpoint replaces an endpoint's configuration.
func (m *Mock) UpdateMarketplaceModelEndpoint(
	_ context.Context, endpointARN string, endpointConfig []byte,
) (*driver.MarketplaceEndpoint, error) {
	stored, ok := m.marketplaceEndpoints.Get(endpointARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "marketplace model endpoint %q not found", endpointARN)
	}

	if len(endpointConfig) == 0 {
		return nil, errors.New(errors.InvalidArgument, "endpointConfig is required")
	}

	// Copy-on-write: mutate a copy so concurrent readers never race the write.
	updated := *stored
	updated.EndpointConfig = copyBytes(endpointConfig)
	updated.UpdatedAt = m.now()
	m.marketplaceEndpoints.Set(endpointARN, &updated)

	result := cloneMarketplaceEndpoint(&updated)

	return &result, nil
}

// DeleteMarketplaceModelEndpoint deletes a marketplace model endpoint by ARN.
func (m *Mock) DeleteMarketplaceModelEndpoint(_ context.Context, endpointARN string) error {
	if !m.marketplaceEndpoints.Has(endpointARN) {
		return errors.Newf(errors.NotFound, "marketplace model endpoint %q not found", endpointARN)
	}

	m.marketplaceEndpoints.Delete(endpointARN)

	return nil
}

// RegisterMarketplaceModelEndpoint registers an externally-created SageMaker
// endpoint with Bedrock, marking it REGISTERED. This is an upsert: if the
// endpoint is not already tracked it is created as a new REGISTERED record keyed
// by endpointIdentifier; if it exists, its model source and status are updated.
func (m *Mock) RegisterMarketplaceModelEndpoint(
	_ context.Context, endpointIdentifier, modelSourceIdentifier string,
) (*driver.MarketplaceEndpoint, error) {
	if modelSourceIdentifier == "" {
		return nil, errors.New(errors.InvalidArgument, "modelSourceIdentifier is required")
	}

	now := m.now()

	stored, ok := m.marketplaceEndpoints.Get(endpointIdentifier)
	if !ok {
		// Not tracked yet: register the externally-created endpoint as a new
		// REGISTERED record keyed by its identifier.
		endpoint := &driver.MarketplaceEndpoint{
			EndpointARN:           endpointIdentifier,
			ModelSourceIdentifier: modelSourceIdentifier,
			EndpointStatus:        driver.MarketplaceEndpointStatusInService,
			Status:                driver.MarketplaceEndpointStatusRegistered,
			CreatedAt:             now,
			UpdatedAt:             now,
		}
		m.marketplaceEndpoints.Set(endpointIdentifier, endpoint)

		result := cloneMarketplaceEndpoint(endpoint)

		return &result, nil
	}

	// Copy-on-write: mutate a copy so concurrent readers never race the write.
	updated := *stored
	updated.ModelSourceIdentifier = modelSourceIdentifier
	updated.Status = driver.MarketplaceEndpointStatusRegistered
	updated.UpdatedAt = now
	m.marketplaceEndpoints.Set(endpointIdentifier, &updated)

	result := cloneMarketplaceEndpoint(&updated)

	return &result, nil
}

// DeregisterMarketplaceModelEndpoint removes an endpoint's Bedrock registration.
// The endpoint is no longer tracked as a marketplace model endpoint, so a
// subsequent Get returns NotFound (the underlying, unmodeled SageMaker endpoint
// is unaffected), matching real AWS.
func (m *Mock) DeregisterMarketplaceModelEndpoint(_ context.Context, endpointARN string) error {
	if !m.marketplaceEndpoints.Has(endpointARN) {
		return errors.Newf(errors.NotFound, "marketplace model endpoint %q not found", endpointARN)
	}

	m.marketplaceEndpoints.Delete(endpointARN)

	return nil
}

// --- Foundation model agreements ---

// CreateFoundationModelAgreement records an accepted agreement for a model.
func (m *Mock) CreateFoundationModelAgreement(_ context.Context, modelID, offerToken string) (string, error) {
	switch {
	case modelID == "":
		return "", errors.New(errors.InvalidArgument, "modelId is required")
	case offerToken == "":
		return "", errors.New(errors.InvalidArgument, "offerToken is required")
	}

	if m.findFoundation(modelID) == nil {
		return "", errors.Newf(errors.InvalidArgument, "foundation model %q not found", modelID)
	}

	m.fmAgreements.Set(modelID, true)

	return modelID, nil
}

// DeleteFoundationModelAgreement removes an accepted agreement for a model.
func (m *Mock) DeleteFoundationModelAgreement(_ context.Context, modelID string) error {
	if modelID == "" {
		return errors.New(errors.InvalidArgument, "modelId is required")
	}

	m.fmAgreements.Delete(modelID)

	return nil
}

// ListFoundationModelAgreementOffers returns a single synthetic agreement offer.
func (*Mock) ListFoundationModelAgreementOffers(
	_ context.Context, modelID, _ string,
) ([]driver.FoundationModelOffer, error) {
	if modelID == "" {
		return nil, errors.New(errors.InvalidArgument, "modelId is required")
	}

	return []driver.FoundationModelOffer{
		{OfferToken: idgen.GenerateID(""), OfferID: idgen.GenerateID("")},
	}, nil
}

// GetFoundationModelAvailability reports availability for a model. An accepted
// agreement makes it AUTHORIZED/AVAILABLE; otherwise NOT_AUTHORIZED/PENDING.
func (m *Mock) GetFoundationModelAvailability(
	_ context.Context, modelID string,
) (*driver.FoundationModelAvailability, error) {
	if modelID == "" {
		return nil, errors.New(errors.InvalidArgument, "modelId is required")
	}

	out := &driver.FoundationModelAvailability{
		AgreementStatus:         driver.AgreementStatusPending,
		AuthorizationStatus:     driver.AuthorizationStatusNotAuthorized,
		EntitlementAvailability: driver.AvailabilityAvailable,
		RegionAvailability:      driver.AvailabilityAvailable,
	}
	if m.fmAgreements.Has(modelID) {
		out.AgreementStatus = driver.AgreementStatusAvailable
		out.AuthorizationStatus = driver.AuthorizationStatusAuthorized
	}

	return out, nil
}

// cloneMarketplaceEndpoint returns a value copy whose EndpointConfig does not
// alias the stored endpoint, so callers can't mutate internal state via the
// result.
func cloneMarketplaceEndpoint(e *driver.MarketplaceEndpoint) driver.MarketplaceEndpoint {
	out := *e
	out.EndpointConfig = copyBytes(e.EndpointConfig)

	return out
}

// copyBytes returns a copy of b so stored payloads never alias caller memory.
func copyBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}

	out := make([]byte, len(b))
	copy(out, b)

	return out
}
