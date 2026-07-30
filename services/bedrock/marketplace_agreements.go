package bedrock

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// --- Marketplace model endpoints ---

// CreateMarketplaceModelEndpoint deploys a marketplace model endpoint.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *Bedrock) CreateMarketplaceModelEndpoint(
	ctx context.Context, cfg driver.MarketplaceEndpointConfig,
) (*driver.MarketplaceEndpoint, error) {
	out, err := b.do(ctx, "CreateMarketplaceModelEndpoint", cfg.EndpointName, func() (any, error) {
		return b.driver.CreateMarketplaceModelEndpoint(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.MarketplaceEndpoint), nil
}

// GetMarketplaceModelEndpoint retrieves a marketplace model endpoint by ARN.
func (b *Bedrock) GetMarketplaceModelEndpoint(ctx context.Context, endpointARN string) (*driver.MarketplaceEndpoint, error) {
	out, err := b.do(ctx, "GetMarketplaceModelEndpoint", endpointARN, func() (any, error) {
		return b.driver.GetMarketplaceModelEndpoint(ctx, endpointARN)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.MarketplaceEndpoint), nil
}

// ListMarketplaceModelEndpoints lists all marketplace model endpoints.
func (b *Bedrock) ListMarketplaceModelEndpoints(ctx context.Context) ([]driver.MarketplaceEndpoint, error) {
	out, err := b.do(ctx, "ListMarketplaceModelEndpoints", nil, func() (any, error) {
		return b.driver.ListMarketplaceModelEndpoints(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.MarketplaceEndpoint), nil
}

// UpdateMarketplaceModelEndpoint replaces an endpoint's configuration.
func (b *Bedrock) UpdateMarketplaceModelEndpoint(
	ctx context.Context, endpointARN string, endpointConfig []byte,
) (*driver.MarketplaceEndpoint, error) {
	out, err := b.do(ctx, "UpdateMarketplaceModelEndpoint", endpointARN, func() (any, error) {
		return b.driver.UpdateMarketplaceModelEndpoint(ctx, endpointARN, endpointConfig)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.MarketplaceEndpoint), nil
}

// DeleteMarketplaceModelEndpoint deletes a marketplace model endpoint by ARN.
func (b *Bedrock) DeleteMarketplaceModelEndpoint(ctx context.Context, endpointARN string) error {
	_, err := b.do(ctx, "DeleteMarketplaceModelEndpoint", endpointARN, func() (any, error) {
		return nil, b.driver.DeleteMarketplaceModelEndpoint(ctx, endpointARN)
	})

	return err
}

// RegisterMarketplaceModelEndpoint registers an existing endpoint.
func (b *Bedrock) RegisterMarketplaceModelEndpoint(
	ctx context.Context, endpointIdentifier, modelSourceIdentifier string,
) (*driver.MarketplaceEndpoint, error) {
	out, err := b.do(ctx, "RegisterMarketplaceModelEndpoint", endpointIdentifier, func() (any, error) {
		return b.driver.RegisterMarketplaceModelEndpoint(ctx, endpointIdentifier, modelSourceIdentifier)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.MarketplaceEndpoint), nil
}

// DeregisterMarketplaceModelEndpoint deregisters an existing endpoint.
func (b *Bedrock) DeregisterMarketplaceModelEndpoint(ctx context.Context, endpointARN string) error {
	_, err := b.do(ctx, "DeregisterMarketplaceModelEndpoint", endpointARN, func() (any, error) {
		return nil, b.driver.DeregisterMarketplaceModelEndpoint(ctx, endpointARN)
	})

	return err
}

// --- Foundation model agreements ---

// CreateFoundationModelAgreement records an accepted agreement for a model.
func (b *Bedrock) CreateFoundationModelAgreement(ctx context.Context, modelID, offerToken string) (string, error) {
	out, err := b.do(ctx, "CreateFoundationModelAgreement", modelID, func() (any, error) {
		return b.driver.CreateFoundationModelAgreement(ctx, modelID, offerToken)
	})
	if err != nil {
		return "", err
	}

	return out.(string), nil
}

// DeleteFoundationModelAgreement removes an accepted agreement for a model.
func (b *Bedrock) DeleteFoundationModelAgreement(ctx context.Context, modelID string) error {
	_, err := b.do(ctx, "DeleteFoundationModelAgreement", modelID, func() (any, error) {
		return nil, b.driver.DeleteFoundationModelAgreement(ctx, modelID)
	})

	return err
}

// ListFoundationModelAgreementOffers lists agreement offers for a model.
func (b *Bedrock) ListFoundationModelAgreementOffers(
	ctx context.Context, modelID, offerType string,
) ([]driver.FoundationModelOffer, error) {
	out, err := b.do(ctx, "ListFoundationModelAgreementOffers", modelID, func() (any, error) {
		return b.driver.ListFoundationModelAgreementOffers(ctx, modelID, offerType)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.FoundationModelOffer), nil
}

// GetFoundationModelAvailability reports availability for a model.
func (b *Bedrock) GetFoundationModelAvailability(
	ctx context.Context, modelID string,
) (*driver.FoundationModelAvailability, error) {
	out, err := b.do(ctx, "GetFoundationModelAvailability", modelID, func() (any, error) {
		return b.driver.GetFoundationModelAvailability(ctx, modelID)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.FoundationModelAvailability), nil
}
