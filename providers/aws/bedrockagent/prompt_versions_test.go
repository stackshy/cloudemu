package bedrockagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func TestSplitPromptIdentifier(t *testing.T) {
	cases := map[string][2]string{
		"ABCDEFGHIJ": {"ABCDEFGHIJ", ""},
		"arn:aws:bedrock:us-east-1:123456789012:prompt/ABCDEFGHIJ":   {"ABCDEFGHIJ", ""},
		"arn:aws:bedrock:us-east-1:123456789012:prompt/ABCDEFGHIJ:7": {"ABCDEFGHIJ", "7"},
	}

	for in, want := range cases {
		id, version := splitPromptIdentifier(in)
		assert.Equal(t, want, [2]string{id, version}, in)
	}
}

func TestPromptVersionErrorsAndCopies(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	p, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p", Variants: []byte(`[{"name":"v"}]`)})
	require.NoError(t, err)

	_, err = m.CreatePromptVersion(ctx, p.ID, "", map[string]string{"bad key!": "x"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	v1, err := m.CreatePromptVersion(ctx, p.ID, "", nil)
	require.NoError(t, err)

	v1.Variants[0] = 'X'

	got, err := m.GetPromptVersion(ctx, p.ID, "1")
	require.NoError(t, err)
	assert.JSONEq(t, `[{"name":"v"}]`, string(got.Variants))

	err = m.DeletePromptVersion(ctx, p.ID, driver.DraftVersion)
	assert.True(t, cerrors.IsInvalidArgument(err), "DRAFT is deleted with DeletePrompt")

	err = m.DeletePromptVersion(ctx, p.ID, "9")
	assert.True(t, cerrors.IsNotFound(err))

	_, _, err = m.ListPromptVersions(ctx, "MISSING000", driver.Page{})
	assert.True(t, cerrors.IsNotFound(err))
}
