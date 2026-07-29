package bedrockagentruntime_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	agentruntime "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	agentruntimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"

	"github.com/stackshy/cloudemu/v2/config"
	providerbar "github.com/stackshy/cloudemu/v2/providers/aws/bedrockagentruntime"
	serverbar "github.com/stackshy/cloudemu/v2/server/aws/bedrockagentruntime"
	svcbar "github.com/stackshy/cloudemu/v2/services/bedrockagentruntime"
)

func newClient(t *testing.T) *agentruntime.Client {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	// Exercise the full stack: provider mock -> portable service -> handler.
	svc := svcbar.NewBedrockAgentRuntime(providerbar.New(opts))
	ts := httptest.NewServer(serverbar.New(svc))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return agentruntime.NewFromConfig(cfg, func(o *agentruntime.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKInvokeAgent(t *testing.T) {
	client := newClient(t)

	out, err := client.InvokeAgent(context.Background(), &agentruntime.InvokeAgentInput{
		AgentId:      aws.String("AGENT123"),
		AgentAliasId: aws.String("ALIAS123"),
		SessionId:    aws.String("session-abc"),
		InputText:    aws.String("Tell me about Bedrock agents"),
	})
	if err != nil {
		t.Fatalf("InvokeAgent: %v", err)
	}

	if aws.ToString(out.SessionId) != "session-abc" {
		t.Fatalf("got session id %q, want session-abc", aws.ToString(out.SessionId))
	}

	stream := out.GetStream()
	defer stream.Close()

	var completion strings.Builder

	for event := range stream.Events() {
		if c, ok := event.(*agentruntimetypes.ResponseStreamMemberChunk); ok {
			completion.Write(c.Value.Bytes)
		}
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}

	got := completion.String()
	if got == "" {
		t.Fatal("expected a non-empty completion")
	}

	if !strings.Contains(got, "Tell me about Bedrock agents") {
		t.Fatalf("completion %q does not echo the prompt", got)
	}
}

func TestSDKRetrieve(t *testing.T) {
	client := newClient(t)

	out, err := client.Retrieve(context.Background(), &agentruntime.RetrieveInput{
		KnowledgeBaseId: aws.String("KB123"),
		RetrievalQuery: &agentruntimetypes.KnowledgeBaseQuery{
			Text: aws.String("what is a knowledge base"),
		},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	if len(out.RetrievalResults) == 0 {
		t.Fatal("expected non-empty retrievalResults")
	}

	for _, r := range out.RetrievalResults {
		if r.Content == nil || aws.ToString(r.Content.Text) == "" {
			t.Fatal("expected a result with content.text")
		}

		if !strings.Contains(aws.ToString(r.Content.Text), "what is a knowledge base") {
			t.Fatalf("result %q does not echo the query", aws.ToString(r.Content.Text))
		}
	}
}

func TestSDKRetrieveAndGenerate(t *testing.T) {
	client := newClient(t)

	out, err := client.RetrieveAndGenerate(context.Background(), &agentruntime.RetrieveAndGenerateInput{
		Input: &agentruntimetypes.RetrieveAndGenerateInput{
			Text: aws.String("Summarize the docs"),
		},
	})
	if err != nil {
		t.Fatalf("RetrieveAndGenerate: %v", err)
	}

	if out.Output == nil || aws.ToString(out.Output.Text) == "" {
		t.Fatal("expected a non-empty output.text")
	}

	if aws.ToString(out.SessionId) == "" {
		t.Fatal("expected a session id")
	}

	if !strings.Contains(aws.ToString(out.Output.Text), "Summarize the docs") {
		t.Fatalf("output %q does not echo the input", aws.ToString(out.Output.Text))
	}
}
