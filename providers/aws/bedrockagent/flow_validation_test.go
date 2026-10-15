package bedrockagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func TestValidateFlowDefinitionChecks(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.ValidateFlowDefinition(ctx, nil)
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = m.ValidateFlowDefinition(ctx, []byte(`[1]`))
	assert.True(t, cerrors.IsInvalidArgument(err))

	empty, err := m.ValidateFlowDefinition(ctx, []byte(`{}`))
	require.NoError(t, err)
	assert.Len(t, empty, 2, "no Input and no Output node")

	dup, err := m.ValidateFlowDefinition(ctx, []byte(`{
		"nodes":[{"name":"in","type":"Input"},{"name":"out","type":"Output"}],
		"connections":[
			{"name":"c1","source":"in","target":"out","type":"Data"},
			{"name":"c2","source":"in","target":"out","type":"Data"},
			{"name":"c3","source":"ghost","target":"out","type":"Data"}]}`))
	require.NoError(t, err)

	types := []string{}
	for _, v := range dup {
		types = append(types, v.Type)
	}

	assert.ElementsMatch(t, []string{"DuplicateConnections", "UnknownConnectionSource"}, types)
}
