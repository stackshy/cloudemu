package bedrock_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	runtimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestSDKConverseStream(t *testing.T) {
	client := newRuntimeClient(t)

	out, err := client.ConverseStream(context.Background(), &awsruntime.ConverseStreamInput{
		ModelId: aws.String(claudeModel),
		Messages: []runtimetypes.Message{
			{
				Role:    runtimetypes.ConversationRoleUser,
				Content: []runtimetypes.ContentBlock{&runtimetypes.ContentBlockMemberText{Value: "Stream me a reply."}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ConverseStream: %v", err)
	}

	stream := out.GetStream()
	defer stream.Close()

	var (
		text        strings.Builder
		sawStart    bool
		sawStop     bool
		sawMetadata bool
		inTokens    int32
	)

	for ev := range stream.Events() {
		switch e := ev.(type) {
		case *runtimetypes.ConverseStreamOutputMemberMessageStart:
			sawStart = true
		case *runtimetypes.ConverseStreamOutputMemberContentBlockDelta:
			if d, ok := e.Value.Delta.(*runtimetypes.ContentBlockDeltaMemberText); ok {
				text.WriteString(d.Value)
			}
		case *runtimetypes.ConverseStreamOutputMemberMessageStop:
			sawStop = true
		case *runtimetypes.ConverseStreamOutputMemberMetadata:
			sawMetadata = true
			if e.Value.Usage != nil {
				inTokens = aws.ToInt32(e.Value.Usage.InputTokens)
			}
		}
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}

	if !sawStart || !sawStop || !sawMetadata {
		t.Fatalf("missing lifecycle events: start=%v stop=%v metadata=%v", sawStart, sawStop, sawMetadata)
	}

	if text.Len() == 0 {
		t.Fatal("expected non-empty streamed assistant text")
	}

	if inTokens <= 0 {
		t.Fatalf("expected positive input token usage, got %d", inTokens)
	}
}

func TestSDKInvokeModelWithResponseStream(t *testing.T) {
	client := newRuntimeClient(t)

	body, _ := json.Marshal(map[string]any{
		"anthropic_version": "bedrock-2023-05-31",
		"max_tokens":        32,
		"messages":          []map[string]any{{"role": "user", "content": "Hi there"}},
	})

	out, err := client.InvokeModelWithResponseStream(context.Background(), &awsruntime.InvokeModelWithResponseStreamInput{
		ModelId:     aws.String(claudeModel),
		ContentType: aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		t.Fatalf("InvokeModelWithResponseStream: %v", err)
	}

	stream := out.GetStream()
	defer stream.Close()

	var payload []byte

	for ev := range stream.Events() {
		if c, ok := ev.(*runtimetypes.ResponseStreamMemberChunk); ok {
			payload = append(payload, c.Value.Bytes...)
		}
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}

	if len(payload) == 0 {
		t.Fatal("expected a non-empty chunk payload")
	}

	// The chunk carries model-native JSON; confirm it parses.
	var probe map[string]any
	if err := json.Unmarshal(payload, &probe); err != nil {
		t.Fatalf("chunk bytes are not valid JSON: %v (%s)", err, string(payload))
	}
}
