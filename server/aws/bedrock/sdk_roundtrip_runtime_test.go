package bedrock_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	awsruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	runtimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestSDKCountTokensConverse(t *testing.T) {
	client := newRuntimeClient(t)

	out, err := client.CountTokens(context.Background(), &awsruntime.CountTokensInput{
		ModelId: aws.String(claudeModel),
		Input: &runtimetypes.CountTokensInputMemberConverse{
			Value: runtimetypes.ConverseTokensRequest{
				System: []runtimetypes.SystemContentBlock{
					&runtimetypes.SystemContentBlockMemberText{Value: "Be concise."},
				},
				Messages: []runtimetypes.Message{
					{
						Role:    runtimetypes.ConversationRoleUser,
						Content: []runtimetypes.ContentBlock{&runtimetypes.ContentBlockMemberText{Value: "What is Bedrock?"}},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CountTokens: %v", err)
	}

	if aws.ToInt32(out.InputTokens) <= 0 {
		t.Fatalf("expected a positive input token count, got %d", aws.ToInt32(out.InputTokens))
	}
}

// TestSDKCountTokensInvokeModel exercises the invokeModel union member, whose
// body the SDK serializes as a base64 blob — the server must decode it back to
// the model-native payload before counting tokens.
func TestSDKCountTokensInvokeModel(t *testing.T) {
	client := newRuntimeClient(t)

	body, _ := json.Marshal(map[string]any{
		"anthropic_version": "bedrock-2023-05-31",
		"messages":          []map[string]any{{"role": "user", "content": "how many tokens is this prompt"}},
	})

	out, err := client.CountTokens(context.Background(), &awsruntime.CountTokensInput{
		ModelId: aws.String(claudeModel),
		Input:   &runtimetypes.CountTokensInputMemberInvokeModel{Value: runtimetypes.InvokeModelTokensRequest{Body: body}},
	})
	if err != nil {
		t.Fatalf("CountTokens (invokeModel): %v", err)
	}

	if aws.ToInt32(out.InputTokens) <= 0 {
		t.Fatalf("expected a positive input token count on the invokeModel path, got %d", aws.ToInt32(out.InputTokens))
	}
}

func TestSDKApplyGuardrail(t *testing.T) {
	endpoint := newServer(t)
	cfg := testConfig(t)

	control := awsbedrock.NewFromConfig(cfg, func(o *awsbedrock.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
	runtime := awsruntime.NewFromConfig(cfg, func(o *awsruntime.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	ctx := context.Background()

	created, err := control.CreateGuardrail(ctx, &awsbedrock.CreateGuardrailInput{
		Name:                    aws.String("gr-apply"),
		Description:             aws.String("apply guardrail test"),
		BlockedInputMessaging:   aws.String("blocked input"),
		BlockedOutputsMessaging: aws.String("blocked output"),
	})
	if err != nil {
		t.Fatalf("CreateGuardrail: %v", err)
	}

	out, err := runtime.ApplyGuardrail(ctx, &awsruntime.ApplyGuardrailInput{
		GuardrailIdentifier: created.GuardrailId,
		GuardrailVersion:    aws.String("DRAFT"),
		Source:              runtimetypes.GuardrailContentSourceInput,
		Content: []runtimetypes.GuardrailContentBlock{
			&runtimetypes.GuardrailContentBlockMemberText{
				Value: runtimetypes.GuardrailTextBlock{Text: aws.String("hello guardrail")},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyGuardrail: %v", err)
	}

	if out.Action != runtimetypes.GuardrailActionNone {
		t.Fatalf("got action %q, want NONE", out.Action)
	}
}
