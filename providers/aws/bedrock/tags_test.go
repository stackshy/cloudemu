package bedrock

import (
	"context"
	"testing"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

const tagResourceARN = "arn:aws:bedrock:us-east-1:123456789012:guardrail/gr-test"

func TestTagResourceAndList(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	err := m.TagResource(ctx, tagResourceARN, []bedrockdriver.Tag{
		{Key: "env", Value: "prod"},
		{Key: "team", Value: "ml"},
	})
	requireNoError(t, err)

	tags, err := m.ListTagsForResource(ctx, tagResourceARN)
	requireNoError(t, err)
	assertEqual(t, 2, len(tags))
	assertEqual(t, "env", tags[0].Key)
	assertEqual(t, "prod", tags[0].Value)
	assertEqual(t, "team", tags[1].Key)
	assertEqual(t, "ml", tags[1].Value)
}

func TestTagResourceOverwrite(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, m.TagResource(ctx, tagResourceARN, []bedrockdriver.Tag{{Key: "env", Value: "dev"}}))
	requireNoError(t, m.TagResource(ctx, tagResourceARN, []bedrockdriver.Tag{{Key: "env", Value: "prod"}}))

	tags, err := m.ListTagsForResource(ctx, tagResourceARN)
	requireNoError(t, err)
	assertEqual(t, 1, len(tags))
	assertEqual(t, "env", tags[0].Key)
	assertEqual(t, "prod", tags[0].Value)
}

func TestUntagResource(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, m.TagResource(ctx, tagResourceARN, []bedrockdriver.Tag{
		{Key: "env", Value: "prod"},
		{Key: "team", Value: "ml"},
		{Key: "cost", Value: "42"},
	}))

	requireNoError(t, m.UntagResource(ctx, tagResourceARN, []string{"team", "cost"}))

	tags, err := m.ListTagsForResource(ctx, tagResourceARN)
	requireNoError(t, err)
	assertEqual(t, 1, len(tags))
	assertEqual(t, "env", tags[0].Key)
}

func TestListTagsUnknownARN(t *testing.T) {
	m := newTestMock()

	tags, err := m.ListTagsForResource(context.Background(), "arn:aws:bedrock:us-east-1:123456789012:guardrail/nope")
	requireNoError(t, err)

	if tags == nil {
		t.Fatal("expected a non-nil empty slice for an unknown ARN")
	}

	assertEqual(t, 0, len(tags))
}

func TestCreateGuardrailPersistsTags(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	g, err := m.CreateGuardrail(ctx, bedrockdriver.GuardrailConfig{
		Name:                    "gr-tagged",
		BlockedInputMessaging:   "blocked in",
		BlockedOutputsMessaging: "blocked out",
		Tags:                    map[string]string{"env": "prod", "team": "ml"},
	})
	requireNoError(t, err)

	tags, err := m.ListTagsForResource(ctx, g.ARN)
	requireNoError(t, err)
	assertEqual(t, 2, len(tags))
	// tagsFromMap sorts by key, so ordering is deterministic.
	assertEqual(t, "env", tags[0].Key)
	assertEqual(t, "prod", tags[0].Value)
	assertEqual(t, "team", tags[1].Key)
	assertEqual(t, "ml", tags[1].Value)
}
