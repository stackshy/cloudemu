package bedrock

import (
	"context"
	"testing"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// TestGuardrailPoliciesStoreDeepCopy verifies that CreateGuardrail deep-copies
// the caller's policy config: mutating the ORIGINAL cfg slices after create
// must not affect the stored draft.
func TestGuardrailPoliciesStoreDeepCopy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topics := []bedrockdriver.GuardrailTopic{
		{Name: "fiduciary", Definition: "financial advice", Examples: []string{"invest?"}, Type: "DENY"},
	}
	filters := []bedrockdriver.GuardrailContentFilter{
		{Type: "HATE", InputStrength: "HIGH", OutputStrength: "MEDIUM"},
	}

	_, err := m.CreateGuardrail(ctx, bedrockdriver.GuardrailConfig{
		Name:                    "gr-immut",
		BlockedInputMessaging:   "in",
		BlockedOutputsMessaging: "out",
		GuardrailPolicies: bedrockdriver.GuardrailPolicies{
			TopicPolicy:   &bedrockdriver.GuardrailTopicPolicy{Topics: topics},
			ContentPolicy: &bedrockdriver.GuardrailContentPolicy{Filters: filters},
		},
	})
	requireNoError(t, err)

	// Mutate the ORIGINAL caller-owned slices after the create call.
	topics[0].Name = "MUTATED"
	topics[0].Examples[0] = "MUTATED"
	filters[0].InputStrength = "MUTATED"

	got, err := m.GetGuardrail(ctx, "gr-immut", "")
	requireNoError(t, err)

	if got.TopicPolicy == nil || got.TopicPolicy.Topics[0].Name != "fiduciary" {
		t.Fatalf("stored topic name aliased caller slice: %+v", got.TopicPolicy)
	}

	if got.TopicPolicy.Topics[0].Examples[0] != "invest?" {
		t.Fatalf("stored topic examples aliased caller slice: %+v", got.TopicPolicy.Topics[0].Examples)
	}

	if got.ContentPolicy == nil || got.ContentPolicy.Filters[0].InputStrength != "HIGH" {
		t.Fatalf("stored content filter aliased caller slice: %+v", got.ContentPolicy)
	}
}

// TestGuardrailVersionImmutableAgainstDraftEdits verifies that a numbered
// version snapshot is immutable against later edits to the DRAFT working copy.
func TestGuardrailVersionImmutableAgainstDraftEdits(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateGuardrail(ctx, bedrockdriver.GuardrailConfig{
		Name:                    "gr-ver-immut",
		BlockedInputMessaging:   "in",
		BlockedOutputsMessaging: "out",
		GuardrailPolicies: bedrockdriver.GuardrailPolicies{
			TopicPolicy: &bedrockdriver.GuardrailTopicPolicy{Topics: []bedrockdriver.GuardrailTopic{
				{Name: "orig", Definition: "d", Examples: []string{"e"}, Type: "DENY"},
			}},
		},
	})
	requireNoError(t, err)

	_, ver, err := m.CreateGuardrailVersion(ctx, "gr-ver-immut", "snapshot")
	requireNoError(t, err)
	assertEqual(t, "1", ver)

	// Update the DRAFT with entirely new policies.
	_, err = m.UpdateGuardrail(ctx, "gr-ver-immut", bedrockdriver.GuardrailConfig{
		Name:                    "gr-ver-immut",
		BlockedInputMessaging:   "in",
		BlockedOutputsMessaging: "out",
		GuardrailPolicies: bedrockdriver.GuardrailPolicies{
			TopicPolicy: &bedrockdriver.GuardrailTopicPolicy{Topics: []bedrockdriver.GuardrailTopic{
				{Name: "changed", Definition: "d2", Examples: []string{"e2"}, Type: "DENY"},
			}},
		},
	})
	requireNoError(t, err)

	// The version snapshot must still carry the original policy values.
	snap, err := m.GetGuardrail(ctx, "gr-ver-immut", ver)
	requireNoError(t, err)

	if snap.TopicPolicy == nil || snap.TopicPolicy.Topics[0].Name != "orig" {
		t.Fatalf("version snapshot mutated by draft edit: %+v", snap.TopicPolicy)
	}

	// And the DRAFT reflects the update, confirming they are distinct graphs.
	draft, err := m.GetGuardrail(ctx, "gr-ver-immut", "DRAFT")
	requireNoError(t, err)

	if draft.TopicPolicy.Topics[0].Name != "changed" {
		t.Fatalf("draft did not pick up the update: %+v", draft.TopicPolicy)
	}
}
