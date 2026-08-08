package bedrock_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

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

	if err := stream.Err(); err != nil && !benignStreamTeardown(err) {
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

// TestSDKConverseStreamMultibyteRuneBoundary streams a completion built from a
// prompt dense with multi-byte UTF-8 runes and long enough that the delta split
// point falls in the middle of a multi-byte rune. It asserts the reassembled
// streamed text is valid UTF-8, carries no U+FFFD replacement characters, and
// contains the multi-byte prompt substring byte-for-byte. It also compares the
// streamed text against the non-streaming Converse text for the same prompt.
func TestSDKConverseStreamMultibyteRuneBoundary(t *testing.T) {
	client := newRuntimeClient(t)

	// Repeated five times so the byte midpoint of the echoed completion lands
	// mid-rune, exercising the rune-boundary split (a raw byte split here would
	// corrupt both halves into U+FFFD).
	prompt := strings.Repeat("café ☕ 日本語 🎉 naïve résumé ", 5)

	msg := []runtimetypes.Message{
		{
			Role:    runtimetypes.ConversationRoleUser,
			Content: []runtimetypes.ContentBlock{&runtimetypes.ContentBlockMemberText{Value: prompt}},
		},
	}

	streamOut, err := client.ConverseStream(context.Background(), &awsruntime.ConverseStreamInput{
		ModelId:  aws.String(claudeModel),
		Messages: msg,
	})
	if err != nil {
		t.Fatalf("ConverseStream: %v", err)
	}

	stream := streamOut.GetStream()
	defer stream.Close()

	var streamed strings.Builder

	for ev := range stream.Events() {
		if d, ok := ev.(*runtimetypes.ConverseStreamOutputMemberContentBlockDelta); ok {
			if td, ok := d.Value.Delta.(*runtimetypes.ContentBlockDeltaMemberText); ok {
				streamed.WriteString(td.Value)
			}
		}
	}

	if err := stream.Err(); err != nil && !benignStreamTeardown(err) {
		t.Fatalf("stream error: %v", err)
	}

	got := streamed.String()

	if !utf8.ValidString(got) {
		t.Fatalf("streamed text is not valid UTF-8: %q", got)
	}

	if strings.ContainsRune(got, '�') {
		t.Fatalf("streamed text contains U+FFFD replacement character: %q", got)
	}

	if !strings.Contains(got, prompt) {
		t.Fatalf("streamed text does not contain the multi-byte prompt substring\n got: %q\nwant substring: %q", got, prompt)
	}

	// The non-streaming Converse of the same prompt must yield identical text.
	convOut, err := client.Converse(context.Background(), &awsruntime.ConverseInput{
		ModelId:  aws.String(claudeModel),
		Messages: msg,
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}

	var nonStreamed strings.Builder

	if out, ok := convOut.Output.(*runtimetypes.ConverseOutputMemberMessage); ok {
		for _, block := range out.Value.Content {
			if tb, ok := block.(*runtimetypes.ContentBlockMemberText); ok {
				nonStreamed.WriteString(tb.Value)
			}
		}
	}

	if got != nonStreamed.String() {
		t.Fatalf("streamed text != non-streamed text\nstreamed:    %q\nnon-streamed: %q", got, nonStreamed.String())
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

	if err := stream.Err(); err != nil && !benignStreamTeardown(err) {
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
