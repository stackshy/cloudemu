package bedrockagent_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func flowNode(name string, typ batypes.FlowNodeType) batypes.FlowNode {
	return batypes.FlowNode{Name: aws.String(name), Type: typ}
}

func flowConn(name, source, target string) batypes.FlowConnection {
	return batypes.FlowConnection{
		Name: aws.String(name), Source: aws.String(source), Target: aws.String(target), Type: batypes.FlowConnectionTypeData,
	}
}

func TestSDKValidateFlowDefinition(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	valid, err := client.ValidateFlowDefinition(ctx, &awsba.ValidateFlowDefinitionInput{Definition: &batypes.FlowDefinition{
		Nodes:       []batypes.FlowNode{flowNode("in", batypes.FlowNodeTypeInput), flowNode("out", batypes.FlowNodeTypeOutput)},
		Connections: []batypes.FlowConnection{flowConn("c1", "in", "out")},
	}})
	require.NoError(t, err)
	assert.Empty(t, valid.Validations)

	broken, err := client.ValidateFlowDefinition(ctx, &awsba.ValidateFlowDefinitionInput{Definition: &batypes.FlowDefinition{
		Nodes:       []batypes.FlowNode{flowNode("in", batypes.FlowNodeTypeInput), flowNode("orphan", batypes.FlowNodeTypePrompt)},
		Connections: []batypes.FlowConnection{flowConn("c1", "in", "ghost")},
	}})
	require.NoError(t, err)

	types := make([]batypes.FlowValidationType, 0, len(broken.Validations))
	for _, v := range broken.Validations {
		assert.Equal(t, batypes.FlowValidationSeverityError, v.Severity)
		types = append(types, v.Type)
	}

	assert.ElementsMatch(t, []batypes.FlowValidationType{
		batypes.FlowValidationTypeMissingEndingNodes,
		batypes.FlowValidationTypeUnknownConnectionTarget,
		batypes.FlowValidationTypeUnreachableNode,
	}, types)
}
