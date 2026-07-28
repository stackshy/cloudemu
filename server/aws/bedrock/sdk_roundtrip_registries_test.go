package bedrock_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
)

func TestSDKInferenceProfileLifecycle(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	src := "arn:aws:bedrock:us-east-1::foundation-model/" + claudeModel
	create, err := client.CreateInferenceProfile(ctx, &awsbedrock.CreateInferenceProfileInput{
		InferenceProfileName: aws.String("profile-1"),
		ModelSource:          &bedrocktypes.InferenceProfileModelSourceMemberCopyFrom{Value: src},
		Description:          aws.String("test profile"),
	})
	if err != nil {
		t.Fatalf("CreateInferenceProfile: %v", err)
	}

	arn := aws.ToString(create.InferenceProfileArn)
	if arn == "" {
		t.Fatal("expected an inference profile ARN")
	}

	if create.Status != bedrocktypes.InferenceProfileStatusActive {
		t.Fatalf("got status %q, want ACTIVE", create.Status)
	}

	got, err := client.GetInferenceProfile(ctx, &awsbedrock.GetInferenceProfileInput{
		InferenceProfileIdentifier: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("GetInferenceProfile: %v", err)
	}

	if got.Type != bedrocktypes.InferenceProfileTypeApplication {
		t.Fatalf("got type %q, want APPLICATION", got.Type)
	}

	if len(got.Models) != 1 || aws.ToString(got.Models[0].ModelArn) != src {
		t.Fatalf("unexpected models: %+v", got.Models)
	}

	list, err := client.ListInferenceProfiles(ctx, &awsbedrock.ListInferenceProfilesInput{})
	if err != nil {
		t.Fatalf("ListInferenceProfiles: %v", err)
	}

	if len(list.InferenceProfileSummaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(list.InferenceProfileSummaries))
	}

	if _, err = client.DeleteInferenceProfile(ctx, &awsbedrock.DeleteInferenceProfileInput{
		InferenceProfileIdentifier: aws.String(arn),
	}); err != nil {
		t.Fatalf("DeleteInferenceProfile: %v", err)
	}

	_, err = client.GetInferenceProfile(ctx, &awsbedrock.GetInferenceProfileInput{
		InferenceProfileIdentifier: aws.String(arn),
	})

	var nfe *bedrocktypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %T: %v", err, err)
	}
}

func TestSDKPromptRouterLifecycle(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	diff := 0.5
	create, err := client.CreatePromptRouter(ctx, &awsbedrock.CreatePromptRouterInput{
		PromptRouterName: aws.String("router-1"),
		Models: []bedrocktypes.PromptRouterTargetModel{
			{ModelArn: aws.String("arn:aws:bedrock:us-east-1::foundation-model/" + claudeModel)},
		},
		RoutingCriteria: &bedrocktypes.RoutingCriteria{ResponseQualityDifference: aws.Float64(diff)},
		FallbackModel:   &bedrocktypes.PromptRouterTargetModel{ModelArn: aws.String("arn:model/fallback")},
		Description:     aws.String("test router"),
	})
	if err != nil {
		t.Fatalf("CreatePromptRouter: %v", err)
	}

	arn := aws.ToString(create.PromptRouterArn)
	if arn == "" {
		t.Fatal("expected a prompt router ARN")
	}

	got, err := client.GetPromptRouter(ctx, &awsbedrock.GetPromptRouterInput{PromptRouterArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("GetPromptRouter: %v", err)
	}

	if got.Status != bedrocktypes.PromptRouterStatusAvailable {
		t.Fatalf("got status %q, want AVAILABLE", got.Status)
	}

	if got.Type != bedrocktypes.PromptRouterTypeCustom {
		t.Fatalf("got type %q, want custom", got.Type)
	}

	if got.RoutingCriteria == nil || aws.ToFloat64(got.RoutingCriteria.ResponseQualityDifference) != diff {
		t.Fatalf("unexpected routing criteria: %+v", got.RoutingCriteria)
	}

	if got.FallbackModel == nil || aws.ToString(got.FallbackModel.ModelArn) != "arn:model/fallback" {
		t.Fatalf("unexpected fallback model: %+v", got.FallbackModel)
	}

	list, err := client.ListPromptRouters(ctx, &awsbedrock.ListPromptRoutersInput{})
	if err != nil {
		t.Fatalf("ListPromptRouters: %v", err)
	}

	if len(list.PromptRouterSummaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(list.PromptRouterSummaries))
	}

	if _, err = client.DeletePromptRouter(ctx, &awsbedrock.DeletePromptRouterInput{
		PromptRouterArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DeletePromptRouter: %v", err)
	}

	_, err = client.GetPromptRouter(ctx, &awsbedrock.GetPromptRouterInput{PromptRouterArn: aws.String(arn)})

	var nfe *bedrocktypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %T: %v", err, err)
	}
}

func TestSDKAutomatedReasoningPolicyLifecycle(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	create, err := client.CreateAutomatedReasoningPolicy(ctx, &awsbedrock.CreateAutomatedReasoningPolicyInput{
		Name:             aws.String("policy-1"),
		Description:      aws.String("test policy"),
		PolicyDefinition: &bedrocktypes.AutomatedReasoningPolicyDefinition{Version: aws.String("1.0")},
	})
	if err != nil {
		t.Fatalf("CreateAutomatedReasoningPolicy: %v", err)
	}

	arn := aws.ToString(create.PolicyArn)
	if arn == "" || aws.ToString(create.DefinitionHash) == "" {
		t.Fatalf("expected policy ARN + definition hash, got %+v", create)
	}

	if aws.ToString(create.Version) != "DRAFT" {
		t.Fatalf("got version %q, want DRAFT", aws.ToString(create.Version))
	}

	got, err := client.GetAutomatedReasoningPolicy(ctx, &awsbedrock.GetAutomatedReasoningPolicyInput{
		PolicyArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("GetAutomatedReasoningPolicy: %v", err)
	}

	if aws.ToString(got.Name) != "policy-1" || aws.ToString(got.PolicyId) == "" {
		t.Fatalf("unexpected policy: %+v", got)
	}

	list, err := client.ListAutomatedReasoningPolicies(ctx, &awsbedrock.ListAutomatedReasoningPoliciesInput{})
	if err != nil {
		t.Fatalf("ListAutomatedReasoningPolicies: %v", err)
	}

	if len(list.AutomatedReasoningPolicySummaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(list.AutomatedReasoningPolicySummaries))
	}

	updated, err := client.UpdateAutomatedReasoningPolicy(ctx, &awsbedrock.UpdateAutomatedReasoningPolicyInput{
		PolicyArn:        aws.String(arn),
		Name:             aws.String("policy-1-renamed"),
		Description:      aws.String("updated"),
		PolicyDefinition: &bedrocktypes.AutomatedReasoningPolicyDefinition{Version: aws.String("2.0")},
	})
	if err != nil {
		t.Fatalf("UpdateAutomatedReasoningPolicy: %v", err)
	}

	if aws.ToString(updated.Name) != "policy-1-renamed" || aws.ToString(updated.DefinitionHash) == "" {
		t.Fatalf("unexpected update result: %+v", updated)
	}

	if _, err = client.DeleteAutomatedReasoningPolicy(ctx, &awsbedrock.DeleteAutomatedReasoningPolicyInput{
		PolicyArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DeleteAutomatedReasoningPolicy: %v", err)
	}

	_, err = client.GetAutomatedReasoningPolicy(ctx, &awsbedrock.GetAutomatedReasoningPolicyInput{
		PolicyArn: aws.String(arn),
	})

	var nfe *bedrocktypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %T: %v", err, err)
	}
}
