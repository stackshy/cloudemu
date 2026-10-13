package bedrockagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func TestResourcePolicyRules(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	kb := mustKB(t, m, "kb")

	_, err := m.PutResourcePolicy(ctx, "arn:aws:bedrock:us-east-1:123456789012:agent/ABCDEFGHIJ", `{}`, "")
	assert.True(t, cerrors.IsInvalidArgument(err), "only knowledge bases take resource policies")

	_, err = m.PutResourcePolicy(ctx, kb.ARN, "", "")
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = m.DeleteResourcePolicy(ctx, kb.ARN, "")
	assert.True(t, cerrors.IsNotFound(err))

	first, err := m.PutResourcePolicy(ctx, kb.ARN, `{"a":1}`, "")
	require.NoError(t, err)

	second, err := m.PutResourcePolicy(ctx, kb.ARN, `{"a":2}`, first.RevisionID)
	require.NoError(t, err)
	assert.NotEqual(t, first.RevisionID, second.RevisionID)

	_, err = m.DeleteResourcePolicy(ctx, kb.ARN, first.RevisionID)
	assert.True(t, cerrors.IsFailedPrecondition(err), "a stale revision is a conflict")

	_, err = m.DeleteKnowledgeBase(ctx, kb.ID)
	require.NoError(t, err)
	assert.Empty(t, m.policies.All(), "the policy goes with its knowledge base")
}
