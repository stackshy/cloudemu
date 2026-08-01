package bedrock

import (
	"context"
	"testing"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

func TestMarketplaceEndpointLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := bedrockdriver.MarketplaceEndpointConfig{
		EndpointName:          "endpoint-1",
		ModelSourceIdentifier: "arn:aws:sagemaker:us-east-1:aws:hub-content/model/1",
		EndpointConfig:        []byte(`{"sageMaker":{"instanceType":"ml.m5.large"}}`),
		AcceptEula:            true,
		Tags:                  map[string]string{"team": "ml"},
	}

	endpoint, err := m.CreateMarketplaceModelEndpoint(ctx, cfg)
	requireNoError(t, err)
	assertNotEmpty(t, endpoint.EndpointARN)
	assertEqual(t, bedrockdriver.MarketplaceEndpointStatusRegistered, endpoint.Status)
	assertEqual(t, bedrockdriver.MarketplaceEndpointStatusInService, endpoint.EndpointStatus)

	got, err := m.GetMarketplaceModelEndpoint(ctx, endpoint.EndpointARN)
	requireNoError(t, err)
	assertEqual(t, cfg.ModelSourceIdentifier, got.ModelSourceIdentifier)

	tags, err := m.ListTagsForResource(ctx, endpoint.EndpointARN)
	requireNoError(t, err)
	assertEqual(t, 1, len(tags))

	list, err := m.ListMarketplaceModelEndpoints(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	updated, err := m.UpdateMarketplaceModelEndpoint(ctx, endpoint.EndpointARN, []byte(`{"sageMaker":{"instanceType":"ml.m5.xlarge"}}`))
	requireNoError(t, err)
	assertEqual(t, `{"sageMaker":{"instanceType":"ml.m5.xlarge"}}`, string(updated.EndpointConfig))

	reg, err := m.RegisterMarketplaceModelEndpoint(ctx, endpoint.EndpointARN, "arn:model/source-2")
	requireNoError(t, err)
	assertEqual(t, bedrockdriver.MarketplaceEndpointStatusRegistered, reg.Status)
	assertEqual(t, "arn:model/source-2", reg.ModelSourceIdentifier)

	// Deregister removes the Bedrock registration: a subsequent Get is NotFound.
	requireNoError(t, m.DeregisterMarketplaceModelEndpoint(ctx, endpoint.EndpointARN))

	_, err = m.GetMarketplaceModelEndpoint(ctx, endpoint.EndpointARN)
	assertError(t, err, true)

	// Delete also removes an endpoint (exercised on a fresh one).
	ep2, err := m.CreateMarketplaceModelEndpoint(ctx, bedrockdriver.MarketplaceEndpointConfig{
		EndpointName:          "endpoint-2",
		ModelSourceIdentifier: "arn:aws:sagemaker:us-east-1:aws:hub-content/model/2",
		EndpointConfig:        []byte(`{"sageMaker":{"instanceType":"ml.m5.large"}}`),
	})
	requireNoError(t, err)
	requireNoError(t, m.DeleteMarketplaceModelEndpoint(ctx, ep2.EndpointARN))

	_, err = m.GetMarketplaceModelEndpoint(ctx, ep2.EndpointARN)
	assertError(t, err, true)
}

func TestMarketplaceEndpointValidationAndErrors(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateMarketplaceModelEndpoint(ctx, bedrockdriver.MarketplaceEndpointConfig{
		ModelSourceIdentifier: "arn:model/s", EndpointConfig: []byte(`{}`),
	})
	assertError(t, err, true)

	_, err = m.CreateMarketplaceModelEndpoint(ctx, bedrockdriver.MarketplaceEndpointConfig{
		EndpointName: "e", EndpointConfig: []byte(`{}`),
	})
	assertError(t, err, true)

	_, err = m.CreateMarketplaceModelEndpoint(ctx, bedrockdriver.MarketplaceEndpointConfig{
		EndpointName: "e", ModelSourceIdentifier: "arn:model/s",
	})
	assertError(t, err, true)

	_, err = m.GetMarketplaceModelEndpoint(ctx, "arn:missing")
	assertError(t, err, true)

	_, err = m.UpdateMarketplaceModelEndpoint(ctx, "arn:missing", []byte(`{}`))
	assertError(t, err, true)

	// Register requires a model source identifier.
	_, err = m.RegisterMarketplaceModelEndpoint(ctx, "arn:some-endpoint", "")
	assertError(t, err, true)

	assertError(t, m.DeregisterMarketplaceModelEndpoint(ctx, "arn:missing"), true)
	assertError(t, m.DeleteMarketplaceModelEndpoint(ctx, "arn:missing"), true)
}

// TestRegisterMarketplaceEndpointUpsert verifies that registering a brand-new
// (untracked) endpoint identifier creates a REGISTERED record retrievable by Get.
func TestRegisterMarketplaceEndpointUpsert(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	const arn = "arn:aws:sagemaker:us-east-1:123456789012:endpoint/external-endpoint"

	reg, err := m.RegisterMarketplaceModelEndpoint(ctx, arn, "arn:model/source-1")
	requireNoError(t, err)
	assertEqual(t, arn, reg.EndpointARN)
	assertEqual(t, "arn:model/source-1", reg.ModelSourceIdentifier)
	assertEqual(t, bedrockdriver.MarketplaceEndpointStatusRegistered, reg.Status)
	assertEqual(t, bedrockdriver.MarketplaceEndpointStatusInService, reg.EndpointStatus)

	got, err := m.GetMarketplaceModelEndpoint(ctx, arn)
	requireNoError(t, err)
	assertEqual(t, arn, got.EndpointARN)
	assertEqual(t, "arn:model/source-1", got.ModelSourceIdentifier)
	assertEqual(t, bedrockdriver.MarketplaceEndpointStatusRegistered, got.Status)

	// Re-registering the now-tracked endpoint updates its model source.
	reg2, err := m.RegisterMarketplaceModelEndpoint(ctx, arn, "arn:model/source-2")
	requireNoError(t, err)
	assertEqual(t, "arn:model/source-2", reg2.ModelSourceIdentifier)
}

// TestMarketplaceEndpointCopyOut verifies that mutating the EndpointConfig
// bytes of a returned endpoint does not affect the stored value.
func TestMarketplaceEndpointCopyOut(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	created, err := m.CreateMarketplaceModelEndpoint(ctx, bedrockdriver.MarketplaceEndpointConfig{
		EndpointName:          "endpoint-copyout",
		ModelSourceIdentifier: "arn:aws:sagemaker:us-east-1:aws:hub-content/model/1",
		EndpointConfig:        []byte(`{"sageMaker":{"instanceType":"ml.m5.large"}}`),
	})
	requireNoError(t, err)

	got, err := m.GetMarketplaceModelEndpoint(ctx, created.EndpointARN)
	requireNoError(t, err)

	// Mutate the returned bytes; the store must be unaffected.
	for i := range got.EndpointConfig {
		got.EndpointConfig[i] = 'X'
	}

	again, err := m.GetMarketplaceModelEndpoint(ctx, created.EndpointARN)
	requireNoError(t, err)
	assertEqual(t, `{"sageMaker":{"instanceType":"ml.m5.large"}}`, string(again.EndpointConfig))
}

func TestFoundationModelAgreementLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	avail, err := m.GetFoundationModelAvailability(ctx, titanModel)
	requireNoError(t, err)
	assertEqual(t, bedrockdriver.AuthorizationStatusNotAuthorized, avail.AuthorizationStatus)
	assertEqual(t, bedrockdriver.AgreementStatusPending, avail.AgreementStatus)
	assertEqual(t, bedrockdriver.AvailabilityAvailable, avail.EntitlementAvailability)
	assertEqual(t, bedrockdriver.AvailabilityAvailable, avail.RegionAvailability)

	offers, err := m.ListFoundationModelAgreementOffers(ctx, titanModel, "ALL")
	requireNoError(t, err)
	assertEqual(t, 1, len(offers))
	assertNotEmpty(t, offers[0].OfferToken)
	assertNotEmpty(t, offers[0].OfferID)

	modelID, err := m.CreateFoundationModelAgreement(ctx, titanModel, offers[0].OfferToken)
	requireNoError(t, err)
	assertEqual(t, titanModel, modelID)

	avail, err = m.GetFoundationModelAvailability(ctx, titanModel)
	requireNoError(t, err)
	assertEqual(t, bedrockdriver.AuthorizationStatusAuthorized, avail.AuthorizationStatus)
	assertEqual(t, bedrockdriver.AgreementStatusAvailable, avail.AgreementStatus)

	requireNoError(t, m.DeleteFoundationModelAgreement(ctx, titanModel))

	avail, err = m.GetFoundationModelAvailability(ctx, titanModel)
	requireNoError(t, err)
	assertEqual(t, bedrockdriver.AuthorizationStatusNotAuthorized, avail.AuthorizationStatus)
}

func TestFoundationModelAgreementValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateFoundationModelAgreement(ctx, "", "token")
	assertError(t, err, true)

	_, err = m.CreateFoundationModelAgreement(ctx, titanModel, "")
	assertError(t, err, true)

	// A model that is not in the catalog is rejected.
	_, err = m.CreateFoundationModelAgreement(ctx, "bogus.nonexistent-model-v1", "token")
	assertError(t, err, true)

	// A real catalog model (fetched from ListFoundationModels) succeeds.
	models, err := m.ListFoundationModels(ctx)
	requireNoError(t, err)

	if len(models) == 0 {
		t.Fatal("expected a non-empty foundation model catalog")
	}

	modelID, err := m.CreateFoundationModelAgreement(ctx, models[0].ModelID, "token")
	requireNoError(t, err)
	assertEqual(t, models[0].ModelID, modelID)

	_, err = m.ListFoundationModelAgreementOffers(ctx, "", "ALL")
	assertError(t, err, true)

	_, err = m.GetFoundationModelAvailability(ctx, "")
	assertError(t, err, true)

	assertError(t, m.DeleteFoundationModelAgreement(ctx, ""), true)
}
