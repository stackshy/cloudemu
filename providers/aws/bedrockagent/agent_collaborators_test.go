package bedrockagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func TestAgentCollaboratorRules(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	sup, err := m.CreateAgent(ctx, driver.AgentConfig{Name: "sup", AgentCollaboration: "SUPERVISOR"})
	require.NoError(t, err)

	workerID := mustAgent(t, m, "worker")

	alias, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: workerID, Name: "live"})
	require.NoError(t, err)

	base := driver.AgentCollaboratorConfig{
		AgentID: sup.ID, AgentVersion: driver.DraftVersion, AliasARN: alias.ARN, Name: "w", Instruction: "do it",
	}

	col, err := m.AssociateAgentCollaborator(ctx, base)
	require.NoError(t, err)

	_, err = m.AssociateAgentCollaborator(ctx, base)
	assert.True(t, cerrors.IsAlreadyExists(err), "names are unique per agent")

	testAlias := base
	testAlias.Name = "t"
	testAlias.AliasARN = "arn:aws:bedrock:us-east-1:123456789012:agent-alias/" + workerID + "/TSTALIASID"
	_, err = m.AssociateAgentCollaborator(ctx, testAlias)
	assert.True(t, cerrors.IsNotFound(err), "the test alias can't collaborate")

	badARN := base
	badARN.Name = "b"
	badARN.AliasARN = "arn:aws:bedrock:us-east-1:123456789012:agent/" + workerID
	_, err = m.AssociateAgentCollaborator(ctx, badARN)
	assert.True(t, cerrors.IsInvalidArgument(err))

	badRelay := base
	badRelay.Name = "r"
	badRelay.RelayConversationHistory = "ALWAYS"
	_, err = m.AssociateAgentCollaborator(ctx, badRelay)
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = m.UpdateAgentCollaborator(ctx, "MISSING000", base)
	assert.True(t, cerrors.IsNotFound(err))

	// A version cut copies collaborators.
	_, err = m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: sup.ID, Name: "prod"})
	require.NoError(t, err)

	v1, err := m.GetAgentCollaborator(ctx, sup.ID, "1", col.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", v1.AgentVersion)

	err = m.DisassociateAgentCollaborator(ctx, sup.ID, "1", col.ID)
	assert.True(t, cerrors.IsInvalidArgument(err), "versions are immutable")
}
