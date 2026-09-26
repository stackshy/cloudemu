package bedrock_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	awsruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	runtimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

const (
	profileOnlyModel = "anthropic.claude-3-7-sonnet-20250219-v1:0"
	usProfileID      = "us." + profileOnlyModel
)

// newSharedClients returns control-plane and runtime clients on one server.
func newSharedClients(t *testing.T) (endpoint string, ctl *awsbedrock.Client, rt *awsruntime.Client) {
	t.Helper()

	endpoint = newServer(t)
	cfg := testConfig(t)
	ctl = awsbedrock.NewFromConfig(cfg, func(o *awsbedrock.Options) { o.BaseEndpoint = aws.String(endpoint) })
	rt = awsruntime.NewFromConfig(cfg, func(o *awsruntime.Options) { o.BaseEndpoint = aws.String(endpoint) })

	return endpoint, ctl, rt
}

func TestSDKListFoundationModelsFilters(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	byProvider, err := client.ListFoundationModels(ctx, &awsbedrock.ListFoundationModelsInput{ByProvider: aws.String("anthropic")})
	if err != nil {
		t.Fatalf("ListFoundationModels: %v", err)
	}

	if len(byProvider.ModelSummaries) == 0 {
		t.Fatal("expected Anthropic models")
	}

	for _, s := range byProvider.ModelSummaries {
		if aws.ToString(s.ProviderName) != "Anthropic" {
			t.Fatalf("byProvider=anthropic returned %s from %s", aws.ToString(s.ModelId), aws.ToString(s.ProviderName))
		}
	}

	embed, err := client.ListFoundationModels(ctx, &awsbedrock.ListFoundationModelsInput{
		ByProvider: aws.String("Amazon"), ByOutputModality: bedrocktypes.ModelModalityEmbedding,
	})
	if err != nil {
		t.Fatalf("ListFoundationModels embedding: %v", err)
	}

	if len(embed.ModelSummaries) != 2 {
		t.Fatalf("got %d Amazon embedding models, want 2", len(embed.ModelSummaries))
	}

	_, err = client.ListFoundationModels(ctx, &awsbedrock.ListFoundationModelsInput{ByOutputModality: "text"})

	var ve *bedrocktypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("invalid enum: want ValidationException, got %v", err)
	}
}

func TestSDKListPagination(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	for _, name := range []string{"a", "b", "c"} {
		if _, err := client.CreateModelCustomizationJob(ctx, &awsbedrock.CreateModelCustomizationJobInput{
			JobName: aws.String("job-" + name), CustomModelName: aws.String("model-" + name),
			RoleArn: aws.String("arn:aws:iam::123456789012:role/r"), BaseModelIdentifier: aws.String("amazon.titan-text-express-v1"),
			TrainingDataConfig: &bedrocktypes.TrainingDataConfig{S3Uri: aws.String("s3://b/train")},
			OutputDataConfig:   &bedrocktypes.OutputDataConfig{S3Uri: aws.String("s3://b/out")},
		}); err != nil {
			t.Fatalf("CreateModelCustomizationJob: %v", err)
		}
	}

	pages, total := 0, 0

	p := awsbedrock.NewListCustomModelsPaginator(client, &awsbedrock.ListCustomModelsInput{MaxResults: aws.Int32(1)})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListCustomModels page: %v", err)
		}

		pages++
		total += len(out.ModelSummaries)
	}

	if pages != 3 || total != 3 {
		t.Fatalf("custom models: got %d pages / %d items, want 3 / 3", pages, total)
	}

	jobs, err := client.ListModelCustomizationJobs(ctx, &awsbedrock.ListModelCustomizationJobsInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListModelCustomizationJobs: %v", err)
	}

	if len(jobs.ModelCustomizationJobSummaries) != 2 || aws.ToString(jobs.NextToken) == "" {
		t.Fatalf("jobs: got %d items, token %q", len(jobs.ModelCustomizationJobSummaries), aws.ToString(jobs.NextToken))
	}

	profiles, err := client.ListInferenceProfiles(ctx, &awsbedrock.ListInferenceProfilesInput{MaxResults: aws.Int32(1)})
	if err != nil {
		t.Fatalf("ListInferenceProfiles: %v", err)
	}

	if len(profiles.InferenceProfileSummaries) != 1 || profiles.InferenceProfileSummaries[0].Type != bedrocktypes.InferenceProfileTypeSystemDefined {
		t.Fatalf("profiles: got %+v", profiles.InferenceProfileSummaries)
	}

	_, err = client.ListCustomModels(ctx, &awsbedrock.ListCustomModelsInput{NextToken: aws.String("not-a-token!")})

	var ve *bedrocktypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("bad token: want ValidationException, got %v", err)
	}
}

// TestListOpsRejectBadMaxResults hits every paginated List route with an
// out-of-range maxResults.
func TestListOpsRejectBadMaxResults(t *testing.T) {
	endpoint := newServer(t)

	paths := []string{
		"/custom-models", "/model-customization-jobs", "/provisioned-model-throughputs", "/async-invoke",
		"/model-import-jobs", "/model-copy-jobs", "/evaluation-jobs", "/inference-profiles", "/prompt-routers",
		"/automated-reasoning-policies", "/marketplace-model/endpoints", "/guardrails",
	}

	for _, p := range paths {
		for _, v := range []string{"0", "1001"} {
			resp, err := http.Get(endpoint + p + "?maxResults=" + v)
			if err != nil {
				t.Fatalf("GET %s: %v", p, err)
			}

			_ = resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("X-Amzn-Errortype") != "ValidationException" {
				t.Fatalf("GET %s?maxResults=%s: got %d %q", p, v, resp.StatusCode, resp.Header.Get("X-Amzn-Errortype"))
			}
		}
	}
}

func TestSDKRuntimeModelResolution(t *testing.T) {
	_, ctl, rt := newSharedClients(t)
	ctx := context.Background()

	msgs := []runtimetypes.Message{{
		Role:    runtimetypes.ConversationRoleUser,
		Content: []runtimetypes.ContentBlock{&runtimetypes.ContentBlockMemberText{Value: "hello"}},
	}}

	_, err := rt.Converse(ctx, &awsruntime.ConverseInput{ModelId: aws.String(profileOnlyModel), Messages: msgs})

	want := "Invocation of model ID " + profileOnlyModel + " with on-demand throughput isn’t supported." +
		" Retry your request with the ID or ARN of an inference profile that contains this model."

	var ve *runtimetypes.ValidationException
	if !errors.As(err, &ve) || aws.ToString(ve.Message) != want {
		t.Fatalf("profile-only Converse: got %v", err)
	}

	for _, id := range []string{usProfileID, "global.anthropic.claude-sonnet-4-20250514-v1:0"} {
		if _, err = rt.Converse(ctx, &awsruntime.ConverseInput{ModelId: aws.String(id), Messages: msgs}); err != nil {
			t.Fatalf("Converse via %s: %v", id, err)
		}
	}

	sys, err := ctl.GetInferenceProfile(ctx, &awsbedrock.GetInferenceProfileInput{InferenceProfileIdentifier: aws.String(usProfileID)})
	if err != nil {
		t.Fatalf("GetInferenceProfile system: %v", err)
	}

	if sys.Type != bedrocktypes.InferenceProfileTypeSystemDefined || len(sys.Models) != 3 {
		t.Fatalf("system profile: type %q, %d models", sys.Type, len(sys.Models))
	}

	body := []byte(`{"anthropic_version":"bedrock-2023-05-31","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)

	out, err := rt.InvokeModel(ctx, &awsruntime.InvokeModelInput{ModelId: sys.InferenceProfileArn, Body: body})
	if err != nil {
		t.Fatalf("InvokeModel via profile ARN: %v", err)
	}

	assertAnthropicBody(t, out.Body)
}

// TestSDKInvokeCustomModelUsesBaseFamily covers BDR-04: a custom model built
// on Claude answers in the Anthropic envelope.
func TestSDKInvokeCustomModelUsesBaseFamily(t *testing.T) {
	_, ctl, rt := newSharedClients(t)
	ctx := context.Background()

	if _, err := ctl.CreateModelCustomizationJob(ctx, &awsbedrock.CreateModelCustomizationJobInput{
		JobName: aws.String("job"), CustomModelName: aws.String("tuned-haiku"),
		RoleArn:             aws.String("arn:aws:iam::123456789012:role/r"),
		BaseModelIdentifier: aws.String("anthropic.claude-3-haiku-20240307-v1:0"),
		TrainingDataConfig:  &bedrocktypes.TrainingDataConfig{S3Uri: aws.String("s3://b/train")},
		OutputDataConfig:    &bedrocktypes.OutputDataConfig{S3Uri: aws.String("s3://b/out")},
	}); err != nil {
		t.Fatalf("CreateModelCustomizationJob: %v", err)
	}

	cm, err := ctl.GetCustomModel(ctx, &awsbedrock.GetCustomModelInput{ModelIdentifier: aws.String("tuned-haiku")})
	if err != nil {
		t.Fatalf("GetCustomModel: %v", err)
	}

	out, err := rt.InvokeModel(ctx, &awsruntime.InvokeModelInput{
		ModelId: cm.ModelArn,
		Body:    []byte(`{"anthropic_version":"bedrock-2023-05-31","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`),
	})
	if err != nil {
		t.Fatalf("InvokeModel custom: %v", err)
	}

	assertAnthropicBody(t, out.Body)
}

func assertAnthropicBody(t *testing.T, raw []byte) {
	t.Helper()

	var body struct {
		Type    string `json:"type"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Type != "message" || len(body.Content) == 0 || !strings.Contains(body.Content[0].Text, "simulated") {
		t.Fatalf("not an Anthropic message body: %s", raw)
	}
}
