package bedrock_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
)

// guardrailWithPolicies drives CreateGuardrail with a topic, content, PII, and
// contextual-grounding policy through the real SDK and returns the created id.
func guardrailWithPolicies(ctx context.Context, t *testing.T, client *awsbedrock.Client) string {
	t.Helper()

	created, err := client.CreateGuardrail(ctx, &awsbedrock.CreateGuardrailInput{
		Name:                    aws.String("gr-policies"),
		Description:             aws.String("guardrail with policies"),
		BlockedInputMessaging:   aws.String("blocked input"),
		BlockedOutputsMessaging: aws.String("blocked output"),
		TopicPolicyConfig: &bedrocktypes.GuardrailTopicPolicyConfig{
			TopicsConfig: []bedrocktypes.GuardrailTopicConfig{{
				Name:       aws.String("fiduciary-advice"),
				Definition: aws.String("Providing personalized financial advice."),
				Examples:   []string{"Should I invest in this stock?"},
				Type:       bedrocktypes.GuardrailTopicTypeDeny,
			}},
		},
		ContentPolicyConfig: &bedrocktypes.GuardrailContentPolicyConfig{
			FiltersConfig: []bedrocktypes.GuardrailContentFilterConfig{{
				Type:           bedrocktypes.GuardrailContentFilterTypeHate,
				InputStrength:  bedrocktypes.GuardrailFilterStrengthHigh,
				OutputStrength: bedrocktypes.GuardrailFilterStrengthMedium,
			}},
		},
		SensitiveInformationPolicyConfig: &bedrocktypes.GuardrailSensitiveInformationPolicyConfig{
			PiiEntitiesConfig: []bedrocktypes.GuardrailPiiEntityConfig{{
				Type:   bedrocktypes.GuardrailPiiEntityTypeEmail,
				Action: bedrocktypes.GuardrailSensitiveInformationActionAnonymize,
			}},
		},
		ContextualGroundingPolicyConfig: &bedrocktypes.GuardrailContextualGroundingPolicyConfig{
			FiltersConfig: []bedrocktypes.GuardrailContextualGroundingFilterConfig{{
				Type:      bedrocktypes.GuardrailContextualGroundingFilterTypeGrounding,
				Threshold: aws.Float64(0.75),
				Action:    bedrocktypes.GuardrailContextualGroundingActionBlock,
			}},
		},
	})
	if err != nil {
		t.Fatalf("CreateGuardrail: %v", err)
	}

	return aws.ToString(created.GuardrailId)
}

func assertGuardrailPolicies(t *testing.T, got *awsbedrock.GetGuardrailOutput) {
	t.Helper()

	if got.TopicPolicy == nil || len(got.TopicPolicy.Topics) != 1 ||
		aws.ToString(got.TopicPolicy.Topics[0].Name) != "fiduciary-advice" {
		t.Fatalf("topic policy missing/wrong: %+v", got.TopicPolicy)
	}

	if got.ContentPolicy == nil || len(got.ContentPolicy.Filters) != 1 ||
		got.ContentPolicy.Filters[0].Type != bedrocktypes.GuardrailContentFilterTypeHate {
		t.Fatalf("content policy missing/wrong: %+v", got.ContentPolicy)
	}

	if got.SensitiveInformationPolicy == nil || len(got.SensitiveInformationPolicy.PiiEntities) != 1 ||
		got.SensitiveInformationPolicy.PiiEntities[0].Action != bedrocktypes.GuardrailSensitiveInformationActionAnonymize {
		t.Fatalf("sensitive-info policy missing/wrong: %+v", got.SensitiveInformationPolicy)
	}

	if got.ContextualGroundingPolicy == nil || len(got.ContextualGroundingPolicy.Filters) != 1 ||
		aws.ToFloat64(got.ContextualGroundingPolicy.Filters[0].Threshold) != 0.75 {
		t.Fatalf("contextual-grounding policy missing/wrong: %+v", got.ContextualGroundingPolicy)
	}
}

func TestSDKGuardrailPoliciesAndVersions(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	id := guardrailWithPolicies(ctx, t, client)

	// GetGuardrail (DRAFT) returns all policies with the response-shaped keys.
	got, err := client.GetGuardrail(ctx, &awsbedrock.GetGuardrailInput{GuardrailIdentifier: aws.String(id)})
	if err != nil {
		t.Fatalf("GetGuardrail: %v", err)
	}

	assertGuardrailPolicies(t, got)

	// Snapshot the DRAFT into an immutable numbered version.
	ver, err := client.CreateGuardrailVersion(ctx, &awsbedrock.CreateGuardrailVersionInput{
		GuardrailIdentifier: aws.String(id),
		Description:         aws.String("v1 snapshot"),
	})
	if err != nil {
		t.Fatalf("CreateGuardrailVersion: %v", err)
	}

	if aws.ToString(ver.GuardrailId) == "" || aws.ToString(ver.Version) != "1" {
		t.Fatalf("expected guardrail id + version 1, got %+v", ver)
	}

	// GetGuardrail at the numbered version returns the snapshot with policies.
	snap, err := client.GetGuardrail(ctx, &awsbedrock.GetGuardrailInput{
		GuardrailIdentifier: aws.String(id),
		GuardrailVersion:    aws.String("1"),
	})
	if err != nil {
		t.Fatalf("GetGuardrail(version): %v", err)
	}

	if aws.ToString(snap.Version) != "1" {
		t.Fatalf("got version %q, want 1", aws.ToString(snap.Version))
	}

	assertGuardrailPolicies(t, snap)

	// Scoped list shows DRAFT plus the numbered version.
	list, err := client.ListGuardrails(ctx, &awsbedrock.ListGuardrailsInput{GuardrailIdentifier: aws.String(id)})
	if err != nil {
		t.Fatalf("ListGuardrails: %v", err)
	}

	if len(list.Guardrails) != 2 {
		t.Fatalf("got %d guardrail summaries, want 2 (DRAFT + v1)", len(list.Guardrails))
	}

	// Deleting the specific version leaves the DRAFT resolvable.
	if _, err = client.DeleteGuardrail(ctx, &awsbedrock.DeleteGuardrailInput{
		GuardrailIdentifier: aws.String(id),
		GuardrailVersion:    aws.String("1"),
	}); err != nil {
		t.Fatalf("DeleteGuardrail(version): %v", err)
	}

	if _, err = client.GetGuardrail(ctx, &awsbedrock.GetGuardrailInput{
		GuardrailIdentifier: aws.String(id),
		GuardrailVersion:    aws.String("1"),
	}); err == nil {
		t.Fatal("expected error getting deleted version")
	}

	if _, err = client.GetGuardrail(ctx, &awsbedrock.GetGuardrailInput{GuardrailIdentifier: aws.String(id)}); err != nil {
		t.Fatalf("DRAFT should survive version delete: %v", err)
	}
}
