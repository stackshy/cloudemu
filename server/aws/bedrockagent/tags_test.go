package bedrockagent_test

import (
	"context"
	"errors"
	"maps"
	"regexp"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
)

// bedrockID is the shape AWS uses for every bedrock-agent resource id.
var bedrockID = regexp.MustCompile(`^[0-9A-Z]{10}$`)

func assertTags(t *testing.T, client *awsba.Client, arn string, want map[string]string) {
	t.Helper()

	out, err := client.ListTagsForResource(context.Background(), &awsba.ListTagsForResourceInput{
		ResourceArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("ListTagsForResource(%s): %v", arn, err)
	}

	if !maps.Equal(out.Tags, want) {
		t.Fatalf("tags on %s = %v, want %v", arn, out.Tags, want)
	}
}

func assertBedrockID(t *testing.T, kind, id string) {
	t.Helper()

	if !bedrockID.MatchString(id) {
		t.Fatalf("%s id %q is not 10 uppercase alphanumeric characters", kind, id)
	}
}

// TestSDKCreateTagsPersist covers every taggable resource: tags passed on
// create come back from ListTagsForResource keyed by the resource ARN.
func TestSDKCreateTagsPersist(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	tags := map[string]string{"env": "test", "team": "ml"}

	agent, err := client.CreateAgent(ctx, &awsba.CreateAgentInput{
		AgentName:            aws.String("tagged-agent"),
		AgentResourceRoleArn: aws.String(roleArn),
		FoundationModel:      aws.String(claudeModel),
		Tags:                 tags,
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	agentID := aws.ToString(agent.Agent.AgentId)
	assertBedrockID(t, "agent", agentID)
	assertTags(t, client, aws.ToString(agent.Agent.AgentArn), tags)

	alias, err := client.CreateAgentAlias(ctx, &awsba.CreateAgentAliasInput{
		AgentId:        aws.String(agentID),
		AgentAliasName: aws.String("prod"),
		Tags:           tags,
	})
	if err != nil {
		t.Fatalf("CreateAgentAlias: %v", err)
	}

	assertBedrockID(t, "agent alias", aws.ToString(alias.AgentAlias.AgentAliasId))
	assertTags(t, client, aws.ToString(alias.AgentAlias.AgentAliasArn), tags)

	kb, err := client.CreateKnowledgeBase(ctx, vectorKBInput("tagged-kb", tags))
	if err != nil {
		t.Fatalf("CreateKnowledgeBase: %v", err)
	}

	assertBedrockID(t, "knowledge base", aws.ToString(kb.KnowledgeBase.KnowledgeBaseId))
	assertTags(t, client, aws.ToString(kb.KnowledgeBase.KnowledgeBaseArn), tags)

	flow, err := client.CreateFlow(ctx, &awsba.CreateFlowInput{
		Name:             aws.String("tagged-flow"),
		ExecutionRoleArn: aws.String(roleArn),
		Tags:             tags,
	})
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}

	assertBedrockID(t, "flow", aws.ToString(flow.Id))
	assertTags(t, client, aws.ToString(flow.Arn), tags)

	prompt, err := client.CreatePrompt(ctx, &awsba.CreatePromptInput{
		Name: aws.String("tagged-prompt"),
		Tags: tags,
	})
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	assertBedrockID(t, "prompt", aws.ToString(prompt.Id))
	assertTags(t, client, aws.ToString(prompt.Arn), tags)
}

// TestSDKTagUntagRoundTrip covers TagResource merging and UntagResource
// removal, then that a deleted resource's ARN is no longer taggable.
func TestSDKTagUntagRoundTrip(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	agent, err := client.CreateAgent(ctx, &awsba.CreateAgentInput{
		AgentName: aws.String("plain-agent"),
		Tags:      map[string]string{"a": "1"},
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	arn := aws.ToString(agent.Agent.AgentArn)

	if _, err = client.TagResource(ctx, &awsba.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        map[string]string{"a": "2", "b": "3"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	assertTags(t, client, arn, map[string]string{"a": "2", "b": "3"})

	if _, err = client.UntagResource(ctx, &awsba.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"a", "missing"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	assertTags(t, client, arn, map[string]string{"b": "3"})

	if _, err = client.DeleteAgent(ctx, &awsba.DeleteAgentInput{AgentId: agent.Agent.AgentId}); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	_, err = client.ListTagsForResource(ctx, &awsba.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	assertNotFound(t, err)
}

// TestSDKTagsUnknownAndUntaggableARN covers the two failure shapes: a
// well-formed ARN naming nothing is ResourceNotFoundException, and an ARN of a
// type bedrock-agent does not tag is ValidationException.
func TestSDKTagsUnknownAndUntaggableARN(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	_, err := client.ListTagsForResource(ctx, &awsba.ListTagsForResourceInput{
		ResourceArn: aws.String("arn:aws:bedrock:us-east-1:123456789012:agent/ABCDE12345"),
	})
	assertNotFound(t, err)

	_, err = client.TagResource(ctx, &awsba.TagResourceInput{
		ResourceArn: aws.String("arn:aws:bedrock:us-east-1:123456789012:knowledge-base/ABCDE12345/data-source/X"),
		Tags:        map[string]string{"k": "v"},
	})

	var ve *batypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationException, got %T: %v", err, err)
	}
}

func vectorKBInput(name string, tags map[string]string) *awsba.CreateKnowledgeBaseInput {
	return &awsba.CreateKnowledgeBaseInput{
		Name:    aws.String(name),
		RoleArn: aws.String(roleArn),
		KnowledgeBaseConfiguration: &batypes.KnowledgeBaseConfiguration{
			Type: batypes.KnowledgeBaseTypeVector,
			VectorKnowledgeBaseConfiguration: &batypes.VectorKnowledgeBaseConfiguration{
				EmbeddingModelArn: aws.String(embedArn),
			},
		},
		StorageConfiguration: &batypes.StorageConfiguration{
			Type: batypes.KnowledgeBaseStorageTypeOpensearchServerless,
			OpensearchServerlessConfiguration: &batypes.OpenSearchServerlessConfiguration{
				CollectionArn:   aws.String("arn:aws:aoss:us-east-1:123456789012:collection/abc"),
				VectorIndexName: aws.String("idx"),
				FieldMapping: &batypes.OpenSearchServerlessFieldMapping{
					MetadataField: aws.String("meta"),
					TextField:     aws.String("text"),
					VectorField:   aws.String("vec"),
				},
			},
		},
		Tags: tags,
	}
}
