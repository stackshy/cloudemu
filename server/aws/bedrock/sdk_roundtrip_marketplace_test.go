package bedrock_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
)

func TestSDKMarketplaceModelEndpointLifecycle(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	source := "arn:aws:sagemaker:us-east-1:aws:hub-content/SageMakerPublicHub/Model/model-1"
	cfg := &bedrocktypes.EndpointConfigMemberSageMaker{Value: bedrocktypes.SageMakerEndpoint{
		ExecutionRole:        aws.String("arn:aws:iam::123456789012:role/sagemaker"),
		InitialInstanceCount: aws.Int32(1),
		InstanceType:         aws.String("ml.m5.large"),
	}}

	create, err := client.CreateMarketplaceModelEndpoint(ctx, &awsbedrock.CreateMarketplaceModelEndpointInput{
		EndpointConfig:        cfg,
		EndpointName:          aws.String("endpoint-1"),
		ModelSourceIdentifier: aws.String(source),
		AcceptEula:            true,
	})
	if err != nil {
		t.Fatalf("CreateMarketplaceModelEndpoint: %v", err)
	}

	endpoint := create.MarketplaceModelEndpoint
	arn := aws.ToString(endpoint.EndpointArn)
	if arn == "" {
		t.Fatal("expected an endpoint ARN")
	}

	if endpoint.Status != bedrocktypes.StatusRegistered {
		t.Fatalf("got status %q, want REGISTERED", endpoint.Status)
	}

	if aws.ToString(endpoint.EndpointStatus) != "InService" {
		t.Fatalf("got endpointStatus %q, want InService", aws.ToString(endpoint.EndpointStatus))
	}

	got, err := client.GetMarketplaceModelEndpoint(ctx, &awsbedrock.GetMarketplaceModelEndpointInput{
		EndpointArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("GetMarketplaceModelEndpoint: %v", err)
	}

	sm, ok := got.MarketplaceModelEndpoint.EndpointConfig.(*bedrocktypes.EndpointConfigMemberSageMaker)
	if !ok || aws.ToString(sm.Value.InstanceType) != "ml.m5.large" {
		t.Fatalf("unexpected endpoint config: %+v", got.MarketplaceModelEndpoint.EndpointConfig)
	}

	list, err := client.ListMarketplaceModelEndpoints(ctx, &awsbedrock.ListMarketplaceModelEndpointsInput{})
	if err != nil {
		t.Fatalf("ListMarketplaceModelEndpoints: %v", err)
	}

	if len(list.MarketplaceModelEndpoints) != 1 || aws.ToString(list.MarketplaceModelEndpoints[0].EndpointArn) != arn {
		t.Fatalf("unexpected list result: %+v", list.MarketplaceModelEndpoints)
	}

	upd := &bedrocktypes.EndpointConfigMemberSageMaker{Value: bedrocktypes.SageMakerEndpoint{
		ExecutionRole:        aws.String("arn:aws:iam::123456789012:role/sagemaker"),
		InitialInstanceCount: aws.Int32(2),
		InstanceType:         aws.String("ml.m5.xlarge"),
	}}

	updated, err := client.UpdateMarketplaceModelEndpoint(ctx, &awsbedrock.UpdateMarketplaceModelEndpointInput{
		EndpointArn:    aws.String(arn),
		EndpointConfig: upd,
	})
	if err != nil {
		t.Fatalf("UpdateMarketplaceModelEndpoint: %v", err)
	}

	usm, ok := updated.MarketplaceModelEndpoint.EndpointConfig.(*bedrocktypes.EndpointConfigMemberSageMaker)
	if !ok || aws.ToString(usm.Value.InstanceType) != "ml.m5.xlarge" {
		t.Fatalf("unexpected updated config: %+v", updated.MarketplaceModelEndpoint.EndpointConfig)
	}

	reg, err := client.RegisterMarketplaceModelEndpoint(ctx, &awsbedrock.RegisterMarketplaceModelEndpointInput{
		EndpointIdentifier:    aws.String(arn),
		ModelSourceIdentifier: aws.String(source),
	})
	if err != nil {
		t.Fatalf("RegisterMarketplaceModelEndpoint: %v", err)
	}

	if reg.MarketplaceModelEndpoint.Status != bedrocktypes.StatusRegistered {
		t.Fatalf("got status %q after register, want REGISTERED", reg.MarketplaceModelEndpoint.Status)
	}

	if _, err = client.DeregisterMarketplaceModelEndpoint(ctx, &awsbedrock.DeregisterMarketplaceModelEndpointInput{
		EndpointArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DeregisterMarketplaceModelEndpoint: %v", err)
	}

	if _, err = client.DeleteMarketplaceModelEndpoint(ctx, &awsbedrock.DeleteMarketplaceModelEndpointInput{
		EndpointArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DeleteMarketplaceModelEndpoint: %v", err)
	}

	_, err = client.GetMarketplaceModelEndpoint(ctx, &awsbedrock.GetMarketplaceModelEndpointInput{
		EndpointArn: aws.String(arn),
	})

	var nfe *bedrocktypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %T: %v", err, err)
	}
}

func TestSDKFoundationModelAgreementLifecycle(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	offers, err := client.ListFoundationModelAgreementOffers(ctx, &awsbedrock.ListFoundationModelAgreementOffersInput{
		ModelId:   aws.String(claudeModel),
		OfferType: bedrocktypes.OfferTypeAll,
	})
	if err != nil {
		t.Fatalf("ListFoundationModelAgreementOffers: %v", err)
	}

	if len(offers.Offers) != 1 || aws.ToString(offers.Offers[0].OfferToken) == "" {
		t.Fatalf("unexpected offers: %+v", offers.Offers)
	}

	create, err := client.CreateFoundationModelAgreement(ctx, &awsbedrock.CreateFoundationModelAgreementInput{
		ModelId:    aws.String(claudeModel),
		OfferToken: offers.Offers[0].OfferToken,
	})
	if err != nil {
		t.Fatalf("CreateFoundationModelAgreement: %v", err)
	}

	if aws.ToString(create.ModelId) != claudeModel {
		t.Fatalf("got modelId %q, want %q", aws.ToString(create.ModelId), claudeModel)
	}

	avail, err := client.GetFoundationModelAvailability(ctx, &awsbedrock.GetFoundationModelAvailabilityInput{
		ModelId: aws.String(claudeModel),
	})
	if err != nil {
		t.Fatalf("GetFoundationModelAvailability: %v", err)
	}

	if avail.AuthorizationStatus != bedrocktypes.AuthorizationStatusAuthorized {
		t.Fatalf("got authorization %q, want AUTHORIZED", avail.AuthorizationStatus)
	}

	if avail.AgreementAvailability == nil || avail.AgreementAvailability.Status != bedrocktypes.AgreementStatusAvailable {
		t.Fatalf("unexpected agreement availability: %+v", avail.AgreementAvailability)
	}

	if avail.EntitlementAvailability != bedrocktypes.EntitlementAvailabilityAvailable {
		t.Fatalf("got entitlement %q, want AVAILABLE", avail.EntitlementAvailability)
	}

	if avail.RegionAvailability != bedrocktypes.RegionAvailabilityAvailable {
		t.Fatalf("got region %q, want AVAILABLE", avail.RegionAvailability)
	}

	if _, err = client.DeleteFoundationModelAgreement(ctx, &awsbedrock.DeleteFoundationModelAgreementInput{
		ModelId: aws.String(claudeModel),
	}); err != nil {
		t.Fatalf("DeleteFoundationModelAgreement: %v", err)
	}

	after, err := client.GetFoundationModelAvailability(ctx, &awsbedrock.GetFoundationModelAvailabilityInput{
		ModelId: aws.String(claudeModel),
	})
	if err != nil {
		t.Fatalf("GetFoundationModelAvailability after delete: %v", err)
	}

	if after.AuthorizationStatus != bedrocktypes.AuthorizationStatusNotAuthorized {
		t.Fatalf("got authorization %q, want NOT_AUTHORIZED", after.AuthorizationStatus)
	}
}
