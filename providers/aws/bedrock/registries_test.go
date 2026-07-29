package bedrock

import (
	"context"
	"testing"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

func TestInferenceProfileLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	src := "arn:aws:bedrock:us-east-1:123456789012:foundation-model/" + titanModel
	profile, err := m.CreateInferenceProfile(ctx, bedrockdriver.InferenceProfileConfig{
		Name:                "profile-1",
		ModelSourceCopyFrom: src,
		Description:         "test profile",
		Tags:                map[string]string{"team": "ml"},
	})
	requireNoError(t, err)
	assertNotEmpty(t, profile.ARN)
	assertNotEmpty(t, profile.ID)
	assertEqual(t, bedrockdriver.InferenceProfileStatusActive, profile.Status)
	assertEqual(t, bedrockdriver.InferenceProfileTypeApplication, profile.Type)
	assertEqual(t, 1, len(profile.Models))
	assertEqual(t, src, profile.Models[0])

	byID, err := m.GetInferenceProfile(ctx, profile.ID)
	requireNoError(t, err)
	assertEqual(t, profile.ARN, byID.ARN)

	byARN, err := m.GetInferenceProfile(ctx, profile.ARN)
	requireNoError(t, err)
	assertEqual(t, "profile-1", byARN.Name)

	list, err := m.ListInferenceProfiles(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	tags, err := m.ListTagsForResource(ctx, profile.ARN)
	requireNoError(t, err)
	assertEqual(t, 1, len(tags))

	requireNoError(t, m.DeleteInferenceProfile(ctx, profile.ID))

	_, err = m.GetInferenceProfile(ctx, profile.ID)
	assertError(t, err, true)
}

func TestInferenceProfileValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInferenceProfile(ctx, bedrockdriver.InferenceProfileConfig{ModelSourceCopyFrom: "arn:model"})
	assertError(t, err, true)

	_, err = m.CreateInferenceProfile(ctx, bedrockdriver.InferenceProfileConfig{Name: "p"})
	assertError(t, err, true)

	assertError(t, m.DeleteInferenceProfile(ctx, "missing"), true)
}

// TestInferenceProfileDuplicateName verifies a second create with the same name
// returns AlreadyExists.
func TestInferenceProfileDuplicateName(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := bedrockdriver.InferenceProfileConfig{
		Name:                "dup-profile",
		ModelSourceCopyFrom: "arn:aws:bedrock:us-east-1:123456789012:foundation-model/" + titanModel,
	}

	_, err := m.CreateInferenceProfile(ctx, cfg)
	requireNoError(t, err)

	_, err = m.CreateInferenceProfile(ctx, cfg)
	assertError(t, err, true)
}

func TestPromptRouterLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	diff := 0.5
	router, err := m.CreatePromptRouter(ctx, bedrockdriver.PromptRouterConfig{
		Name:                      "router-1",
		Models:                    []string{"arn:aws:bedrock:us-east-1::foundation-model/a", "arn:model/b"},
		ResponseQualityDifference: &diff,
		FallbackModelARN:          "arn:model/fallback",
		Description:               "test router",
		Tags:                      map[string]string{"team": "ml"},
	})
	requireNoError(t, err)
	assertNotEmpty(t, router.ARN)
	assertEqual(t, bedrockdriver.PromptRouterStatusAvailable, router.Status)
	assertEqual(t, bedrockdriver.PromptRouterTypeCustom, router.Type)
	assertEqual(t, 2, len(router.Models))

	got, err := m.GetPromptRouter(ctx, router.ARN)
	requireNoError(t, err)
	assertEqual(t, "router-1", got.Name)
	assertEqual(t, "arn:model/fallback", got.FallbackModelARN)

	if got.ResponseQualityDifference == nil || *got.ResponseQualityDifference != diff {
		t.Fatalf("unexpected routing criteria: %+v", got.ResponseQualityDifference)
	}

	list, err := m.ListPromptRouters(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	requireNoError(t, m.DeletePromptRouter(ctx, router.ARN))

	_, err = m.GetPromptRouter(ctx, router.ARN)
	assertError(t, err, true)
}

func TestPromptRouterValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	diff := 0.25
	_, err := m.CreatePromptRouter(ctx, bedrockdriver.PromptRouterConfig{
		Models:                    []string{"arn:model/a"},
		ResponseQualityDifference: &diff,
		FallbackModelARN:          "arn:model/f",
	})
	assertError(t, err, true)

	_, err = m.CreatePromptRouter(ctx, bedrockdriver.PromptRouterConfig{Name: "r", FallbackModelARN: "arn:model/f", ResponseQualityDifference: &diff})
	assertError(t, err, true)

	_, err = m.CreatePromptRouter(ctx, bedrockdriver.PromptRouterConfig{Name: "r", Models: []string{"arn:model/a"}, FallbackModelARN: "arn:model/f"})
	assertError(t, err, true)

	assertError(t, m.DeletePromptRouter(ctx, "arn:missing"), true)
}

func TestAutomatedReasoningPolicyLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	policy, err := m.CreateAutomatedReasoningPolicy(ctx, bedrockdriver.AutomatedReasoningPolicyConfig{
		Name:             "policy-1",
		Description:      "test policy",
		KMSKeyID:         "arn:aws:kms:us-east-1:123456789012:key/abc",
		PolicyDefinition: []byte(`{"rules":[]}`),
		Tags:             map[string]string{"team": "ml"},
	})
	requireNoError(t, err)
	assertNotEmpty(t, policy.ARN)
	assertNotEmpty(t, policy.ID)
	assertNotEmpty(t, policy.DefinitionHash)
	assertEqual(t, bedrockdriver.AutomatedReasoningPolicyVersionDraft, policy.Version)
	assertEqual(t, "arn:aws:kms:us-east-1:123456789012:key/abc", policy.KMSKeyARN)

	got, err := m.GetAutomatedReasoningPolicy(ctx, policy.ARN)
	requireNoError(t, err)
	assertEqual(t, "policy-1", got.Name)

	list, err := m.ListAutomatedReasoningPolicies(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	updated, err := m.UpdateAutomatedReasoningPolicy(ctx, policy.ARN, bedrockdriver.AutomatedReasoningPolicyUpdate{
		Name:             "policy-1-renamed",
		Description:      "updated",
		PolicyDefinition: []byte(`{"rules":[{"id":"r1"}]}`),
	})
	requireNoError(t, err)
	assertEqual(t, "policy-1-renamed", updated.Name)

	if updated.DefinitionHash == policy.DefinitionHash {
		t.Fatal("expected definition hash to change after update")
	}

	requireNoError(t, m.DeleteAutomatedReasoningPolicy(ctx, policy.ARN))

	_, err = m.GetAutomatedReasoningPolicy(ctx, policy.ARN)
	assertError(t, err, true)
}

func TestAutomatedReasoningPolicyValidationAndErrors(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateAutomatedReasoningPolicy(ctx, bedrockdriver.AutomatedReasoningPolicyConfig{Description: "no name"})
	assertError(t, err, true)

	_, err = m.GetAutomatedReasoningPolicy(ctx, "arn:missing")
	assertError(t, err, true)

	_, err = m.UpdateAutomatedReasoningPolicy(ctx, "arn:missing", bedrockdriver.AutomatedReasoningPolicyUpdate{Name: "x"})
	assertError(t, err, true)

	assertError(t, m.DeleteAutomatedReasoningPolicy(ctx, "arn:missing"), true)
}

// TestAutomatedReasoningPolicyDuplicateName verifies a second create with the
// same name returns AlreadyExists.
func TestAutomatedReasoningPolicyDuplicateName(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := bedrockdriver.AutomatedReasoningPolicyConfig{
		Name:             "dup-policy",
		PolicyDefinition: []byte(`{"rules":[]}`),
	}

	_, err := m.CreateAutomatedReasoningPolicy(ctx, cfg)
	requireNoError(t, err)

	_, err = m.CreateAutomatedReasoningPolicy(ctx, cfg)
	assertError(t, err, true)
}
