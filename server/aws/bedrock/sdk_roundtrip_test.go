package bedrock_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	awsruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	runtimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const claudeModel = "anthropic.claude-3-sonnet-20240229-v1:0"

func newServer(t *testing.T) string {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		Bedrock: cloud.Bedrock,
		// S3 included to exercise routing precedence: Bedrock must claim its
		// REST paths before the catch-all S3 handler sees them.
		S3: cloud.S3,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts.URL
}

func newControlClient(t *testing.T) *awsbedrock.Client {
	t.Helper()

	cfg := testConfig(t)

	return awsbedrock.NewFromConfig(cfg, func(o *awsbedrock.Options) {
		o.BaseEndpoint = aws.String(newServer(t))
	})
}

func testConfig(t *testing.T) aws.Config {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return cfg
}

func TestSDKListAndGetFoundationModels(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	list, err := client.ListFoundationModels(ctx, &awsbedrock.ListFoundationModelsInput{})
	if err != nil {
		t.Fatalf("ListFoundationModels: %v", err)
	}

	if len(list.ModelSummaries) == 0 {
		t.Fatal("expected a non-empty foundation-model catalog")
	}

	got, err := client.GetFoundationModel(ctx, &awsbedrock.GetFoundationModelInput{
		ModelIdentifier: aws.String(claudeModel),
	})
	if err != nil {
		t.Fatalf("GetFoundationModel: %v", err)
	}

	if aws.ToString(got.ModelDetails.ModelId) != claudeModel {
		t.Fatalf("got model %q, want %q", aws.ToString(got.ModelDetails.ModelId), claudeModel)
	}

	if got.ModelDetails.ModelLifecycle == nil || got.ModelDetails.ModelLifecycle.Status != bedrocktypes.FoundationModelLifecycleStatusActive {
		t.Fatalf("expected ACTIVE lifecycle, got %+v", got.ModelDetails.ModelLifecycle)
	}
}

func TestSDKGetFoundationModelNotFound(t *testing.T) {
	client := newControlClient(t)

	_, err := client.GetFoundationModel(context.Background(), &awsbedrock.GetFoundationModelInput{
		ModelIdentifier: aws.String("does.not-exist-v1"),
	})
	if err == nil {
		t.Fatal("expected error for unknown model")
	}

	var nfe *bedrocktypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %T: %v", err, err)
	}
}

func TestSDKCustomizationLifecycle(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	create, err := client.CreateModelCustomizationJob(ctx, &awsbedrock.CreateModelCustomizationJobInput{
		JobName:             aws.String("tune-1"),
		CustomModelName:     aws.String("my-custom-model"),
		RoleArn:             aws.String("arn:aws:iam::123456789012:role/bedrock"),
		BaseModelIdentifier: aws.String("amazon.titan-text-express-v1"),
		TrainingDataConfig:  &bedrocktypes.TrainingDataConfig{S3Uri: aws.String("s3://bucket/train.jsonl")},
		OutputDataConfig:    &bedrocktypes.OutputDataConfig{S3Uri: aws.String("s3://bucket/out/")},
		HyperParameters:     map[string]string{"epochs": "3"},
	})
	if err != nil {
		t.Fatalf("CreateModelCustomizationJob: %v", err)
	}

	if aws.ToString(create.JobArn) == "" {
		t.Fatal("expected a job ARN")
	}

	job, err := client.GetModelCustomizationJob(ctx, &awsbedrock.GetModelCustomizationJobInput{
		JobIdentifier: aws.String("tune-1"),
	})
	if err != nil {
		t.Fatalf("GetModelCustomizationJob: %v", err)
	}

	if job.Status != bedrocktypes.ModelCustomizationJobStatusCompleted {
		t.Fatalf("got status %q, want Completed", job.Status)
	}

	if aws.ToString(job.OutputModelName) != "my-custom-model" {
		t.Fatalf("got output model %q", aws.ToString(job.OutputModelName))
	}

	jobs, err := client.ListModelCustomizationJobs(ctx, &awsbedrock.ListModelCustomizationJobsInput{})
	if err != nil {
		t.Fatalf("ListModelCustomizationJobs: %v", err)
	}

	if len(jobs.ModelCustomizationJobSummaries) != 1 {
		t.Fatalf("got %d job summaries, want 1", len(jobs.ModelCustomizationJobSummaries))
	}

	models, err := client.ListCustomModels(ctx, &awsbedrock.ListCustomModelsInput{})
	if err != nil {
		t.Fatalf("ListCustomModels: %v", err)
	}

	if len(models.ModelSummaries) != 1 {
		t.Fatalf("got %d custom models, want 1", len(models.ModelSummaries))
	}

	cm, err := client.GetCustomModel(ctx, &awsbedrock.GetCustomModelInput{
		ModelIdentifier: aws.String("my-custom-model"),
	})
	if err != nil {
		t.Fatalf("GetCustomModel: %v", err)
	}

	if cm.ModelStatus != bedrocktypes.ModelStatusActive {
		t.Fatalf("got model status %q, want Active", cm.ModelStatus)
	}

	if _, err = client.DeleteCustomModel(ctx, &awsbedrock.DeleteCustomModelInput{
		ModelIdentifier: aws.String("my-custom-model"),
	}); err != nil {
		t.Fatalf("DeleteCustomModel: %v", err)
	}

	_, err = client.GetCustomModel(ctx, &awsbedrock.GetCustomModelInput{
		ModelIdentifier: aws.String("my-custom-model"),
	})
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestSDKCreateJobUnknownBaseModel(t *testing.T) {
	client := newControlClient(t)

	_, err := client.CreateModelCustomizationJob(context.Background(), &awsbedrock.CreateModelCustomizationJobInput{
		JobName:             aws.String("bad"),
		CustomModelName:     aws.String("m"),
		RoleArn:             aws.String("arn:aws:iam::123456789012:role/bedrock"),
		BaseModelIdentifier: aws.String("nope.unknown-v1"),
		TrainingDataConfig:  &bedrocktypes.TrainingDataConfig{S3Uri: aws.String("s3://b/t")},
		OutputDataConfig:    &bedrocktypes.OutputDataConfig{S3Uri: aws.String("s3://b/o")},
	})

	var ve *bedrocktypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationException, got %T: %v", err, err)
	}
}

func newRuntimeClient(t *testing.T) *awsruntime.Client {
	t.Helper()

	cfg := testConfig(t)

	return awsruntime.NewFromConfig(cfg, func(o *awsruntime.Options) {
		o.BaseEndpoint = aws.String(newServer(t))
	})
}

func TestSDKInvokeModelAnthropic(t *testing.T) {
	client := newRuntimeClient(t)

	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello there"}]}]}`)

	out, err := client.InvokeModel(context.Background(), &awsruntime.InvokeModelInput{
		ModelId:     aws.String(claudeModel),
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		t.Fatalf("InvokeModel: %v", err)
	}

	var resp struct {
		Type    string `json:"type"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}

	if err = json.Unmarshal(out.Body, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Type != "message" || len(resp.Content) == 0 || resp.Content[0].Text == "" {
		t.Fatalf("unexpected anthropic response shape: %s", out.Body)
	}
}

func TestSDKInvokeModelUnknownModel(t *testing.T) {
	client := newRuntimeClient(t)

	_, err := client.InvokeModel(context.Background(), &awsruntime.InvokeModelInput{
		ModelId:     aws.String("nope.unknown-v1"),
		ContentType: aws.String("application/json"),
		Body:        []byte(`{"inputText":"hi"}`),
	})

	var ae smithy.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("expected API error, got %T: %v", err, err)
	}

	if ae.ErrorCode() != "ValidationException" {
		t.Fatalf("got error code %q, want ValidationException", ae.ErrorCode())
	}
}

func TestSDKConverse(t *testing.T) {
	client := newRuntimeClient(t)

	out, err := client.Converse(context.Background(), &awsruntime.ConverseInput{
		ModelId: aws.String(claudeModel),
		System:  []runtimetypes.SystemContentBlock{&runtimetypes.SystemContentBlockMemberText{Value: "Be concise."}},
		Messages: []runtimetypes.Message{
			{
				Role:    runtimetypes.ConversationRoleUser,
				Content: []runtimetypes.ContentBlock{&runtimetypes.ContentBlockMemberText{Value: "What is Bedrock?"}},
			},
		},
		InferenceConfig: &runtimetypes.InferenceConfiguration{MaxTokens: aws.Int32(256)},
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}

	if out.StopReason != runtimetypes.StopReasonEndTurn {
		t.Fatalf("got stop reason %q, want end_turn", out.StopReason)
	}

	msg, ok := out.Output.(*runtimetypes.ConverseOutputMemberMessage)
	if !ok {
		t.Fatalf("unexpected output union type %T", out.Output)
	}

	block, ok := msg.Value.Content[0].(*runtimetypes.ContentBlockMemberText)
	if !ok || block.Value == "" {
		t.Fatalf("expected non-empty text content block, got %T", msg.Value.Content[0])
	}

	if out.Usage == nil || aws.ToInt32(out.Usage.TotalTokens) == 0 {
		t.Fatalf("expected non-zero token usage, got %+v", out.Usage)
	}
}

func TestSDKGuardrailLifecycle(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	created, err := client.CreateGuardrail(ctx, &awsbedrock.CreateGuardrailInput{
		Name:                    aws.String("gr-1"),
		Description:             aws.String("test guardrail"),
		BlockedInputMessaging:   aws.String("blocked input"),
		BlockedOutputsMessaging: aws.String("blocked output"),
	})
	if err != nil {
		t.Fatalf("CreateGuardrail: %v", err)
	}

	if aws.ToString(created.GuardrailId) == "" || aws.ToString(created.GuardrailArn) == "" {
		t.Fatalf("expected guardrail id + arn, got %+v", created)
	}

	got, err := client.GetGuardrail(ctx, &awsbedrock.GetGuardrailInput{
		GuardrailIdentifier: created.GuardrailId,
	})
	if err != nil {
		t.Fatalf("GetGuardrail: %v", err)
	}

	if got.Status != bedrocktypes.GuardrailStatusReady {
		t.Fatalf("got status %q, want READY", got.Status)
	}

	list, err := client.ListGuardrails(ctx, &awsbedrock.ListGuardrailsInput{})
	if err != nil {
		t.Fatalf("ListGuardrails: %v", err)
	}

	if len(list.Guardrails) != 1 {
		t.Fatalf("got %d guardrails, want 1", len(list.Guardrails))
	}

	if _, err = client.UpdateGuardrail(ctx, &awsbedrock.UpdateGuardrailInput{
		GuardrailIdentifier:     created.GuardrailId,
		Name:                    aws.String("gr-1"),
		BlockedInputMessaging:   aws.String("updated input"),
		BlockedOutputsMessaging: aws.String("updated output"),
	}); err != nil {
		t.Fatalf("UpdateGuardrail: %v", err)
	}

	if _, err = client.DeleteGuardrail(ctx, &awsbedrock.DeleteGuardrailInput{
		GuardrailIdentifier: created.GuardrailId,
	}); err != nil {
		t.Fatalf("DeleteGuardrail: %v", err)
	}

	_, err = client.GetGuardrail(ctx, &awsbedrock.GetGuardrailInput{GuardrailIdentifier: created.GuardrailId})
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestSDKProvisionedThroughputLifecycle(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	created, err := client.CreateProvisionedModelThroughput(ctx, &awsbedrock.CreateProvisionedModelThroughputInput{
		ProvisionedModelName: aws.String("pt-1"),
		ModelId:              aws.String(claudeModel),
		ModelUnits:           aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("CreateProvisionedModelThroughput: %v", err)
	}

	got, err := client.GetProvisionedModelThroughput(ctx, &awsbedrock.GetProvisionedModelThroughputInput{
		ProvisionedModelId: aws.String("pt-1"),
	})
	if err != nil {
		t.Fatalf("GetProvisionedModelThroughput: %v", err)
	}

	if got.Status != bedrocktypes.ProvisionedModelStatusInService {
		t.Fatalf("got status %q, want InService", got.Status)
	}

	list, err := client.ListProvisionedModelThroughputs(ctx, &awsbedrock.ListProvisionedModelThroughputsInput{})
	if err != nil {
		t.Fatalf("ListProvisionedModelThroughputs: %v", err)
	}

	if len(list.ProvisionedModelSummaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(list.ProvisionedModelSummaries))
	}

	if _, err = client.DeleteProvisionedModelThroughput(ctx, &awsbedrock.DeleteProvisionedModelThroughputInput{
		ProvisionedModelId: aws.String(aws.ToString(created.ProvisionedModelArn)),
	}); err != nil {
		t.Fatalf("DeleteProvisionedModelThroughput: %v", err)
	}
}

func TestSDKModelInvocationLogging(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	_, err := client.PutModelInvocationLoggingConfiguration(ctx, &awsbedrock.PutModelInvocationLoggingConfigurationInput{
		LoggingConfig: &bedrocktypes.LoggingConfig{
			TextDataDeliveryEnabled: aws.Bool(true),
			S3Config: &bedrocktypes.S3Config{
				BucketName: aws.String("my-logs"),
				KeyPrefix:  aws.String("bedrock/"),
			},
		},
	})
	if err != nil {
		t.Fatalf("PutModelInvocationLoggingConfiguration: %v", err)
	}

	got, err := client.GetModelInvocationLoggingConfiguration(ctx, &awsbedrock.GetModelInvocationLoggingConfigurationInput{})
	if err != nil {
		t.Fatalf("GetModelInvocationLoggingConfiguration: %v", err)
	}

	if got.LoggingConfig == nil || got.LoggingConfig.S3Config == nil ||
		aws.ToString(got.LoggingConfig.S3Config.BucketName) != "my-logs" {
		t.Fatalf("unexpected logging config: %+v", got.LoggingConfig)
	}

	if _, err = client.DeleteModelInvocationLoggingConfiguration(ctx,
		&awsbedrock.DeleteModelInvocationLoggingConfigurationInput{}); err != nil {
		t.Fatalf("DeleteModelInvocationLoggingConfiguration: %v", err)
	}

	after, err := client.GetModelInvocationLoggingConfiguration(ctx,
		&awsbedrock.GetModelInvocationLoggingConfigurationInput{})
	if err != nil {
		t.Fatalf("GetModelInvocationLoggingConfiguration after delete: %v", err)
	}

	if after.LoggingConfig != nil {
		t.Fatalf("expected nil logging config after delete, got %+v", after.LoggingConfig)
	}
}
