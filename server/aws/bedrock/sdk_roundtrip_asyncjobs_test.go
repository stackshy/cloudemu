package bedrock_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	awsruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	runtimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

func TestSDKAsyncInvokeLifecycle(t *testing.T) {
	client := newRuntimeClient(t)
	ctx := context.Background()

	start, err := client.StartAsyncInvoke(ctx, &awsruntime.StartAsyncInvokeInput{
		ModelId:    aws.String(claudeModel),
		ModelInput: document.NewLazyDocument(map[string]any{"inputText": "hello"}),
		OutputDataConfig: &runtimetypes.AsyncInvokeOutputDataConfigMemberS3OutputDataConfig{
			Value: runtimetypes.AsyncInvokeS3OutputDataConfig{S3Uri: aws.String("s3://bucket/out/")},
		},
	})
	if err != nil {
		t.Fatalf("StartAsyncInvoke: %v", err)
	}

	arn := aws.ToString(start.InvocationArn)
	if arn == "" {
		t.Fatal("expected an invocation ARN")
	}

	got, err := client.GetAsyncInvoke(ctx, &awsruntime.GetAsyncInvokeInput{InvocationArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("GetAsyncInvoke: %v", err)
	}

	if got.Status != runtimetypes.AsyncInvokeStatusCompleted {
		t.Fatalf("got status %q, want Completed", got.Status)
	}

	if aws.ToString(got.ModelArn) == "" || got.SubmitTime == nil {
		t.Fatalf("expected modelArn + submitTime, got %+v", got)
	}

	s3, ok := got.OutputDataConfig.(*runtimetypes.AsyncInvokeOutputDataConfigMemberS3OutputDataConfig)
	if !ok || aws.ToString(s3.Value.S3Uri) != "s3://bucket/out/" {
		t.Fatalf("unexpected output data config: %+v", got.OutputDataConfig)
	}

	list, err := client.ListAsyncInvokes(ctx, &awsruntime.ListAsyncInvokesInput{})
	if err != nil {
		t.Fatalf("ListAsyncInvokes: %v", err)
	}

	if len(list.AsyncInvokeSummaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(list.AsyncInvokeSummaries))
	}
}

func TestSDKGetAsyncInvokeNotFound(t *testing.T) {
	client := newRuntimeClient(t)

	_, err := client.GetAsyncInvoke(context.Background(), &awsruntime.GetAsyncInvokeInput{
		InvocationArn: aws.String("arn:aws:bedrock:us-east-1:123456789012:async-invoke/missing"),
	})

	var ae smithy.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("expected API error, got %T: %v", err, err)
	}

	if ae.ErrorCode() != "ResourceNotFoundException" {
		t.Fatalf("got error code %q, want ResourceNotFoundException", ae.ErrorCode())
	}
}

func TestSDKModelImportJobLifecycle(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	create, err := client.CreateModelImportJob(ctx, &awsbedrock.CreateModelImportJobInput{
		JobName:           aws.String("import-1"),
		ImportedModelName: aws.String("my-imported"),
		RoleArn:           aws.String("arn:aws:iam::123456789012:role/bedrock"),
		ModelDataSource: &bedrocktypes.ModelDataSourceMemberS3DataSource{
			Value: bedrocktypes.S3DataSource{S3Uri: aws.String("s3://bucket/model/")},
		},
	})
	if err != nil {
		t.Fatalf("CreateModelImportJob: %v", err)
	}

	if aws.ToString(create.JobArn) == "" {
		t.Fatal("expected a job ARN")
	}

	got, err := client.GetModelImportJob(ctx, &awsbedrock.GetModelImportJobInput{
		JobIdentifier: aws.String("import-1"),
	})
	if err != nil {
		t.Fatalf("GetModelImportJob: %v", err)
	}

	if got.Status != bedrocktypes.ModelImportJobStatusCompleted {
		t.Fatalf("got status %q, want Completed", got.Status)
	}

	if aws.ToString(got.ImportedModelArn) == "" {
		t.Fatalf("expected an imported model ARN, got %+v", got)
	}

	if got.ModelDataSource == nil {
		t.Fatal("expected modelDataSource to round-trip")
	}

	list, err := client.ListModelImportJobs(ctx, &awsbedrock.ListModelImportJobsInput{})
	if err != nil {
		t.Fatalf("ListModelImportJobs: %v", err)
	}

	if len(list.ModelImportJobSummaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(list.ModelImportJobSummaries))
	}

	_, err = client.GetModelImportJob(ctx, &awsbedrock.GetModelImportJobInput{JobIdentifier: aws.String("missing")})

	var nfe *bedrocktypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %T: %v", err, err)
	}
}

func TestSDKModelCopyJobLifecycle(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	src := "arn:aws:bedrock:us-east-1:123456789012:custom-model/src"
	create, err := client.CreateModelCopyJob(ctx, &awsbedrock.CreateModelCopyJobInput{
		SourceModelArn:  aws.String(src),
		TargetModelName: aws.String("copy-target"),
	})
	if err != nil {
		t.Fatalf("CreateModelCopyJob: %v", err)
	}

	arn := aws.ToString(create.JobArn)
	if arn == "" {
		t.Fatal("expected a job ARN")
	}

	got, err := client.GetModelCopyJob(ctx, &awsbedrock.GetModelCopyJobInput{JobArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("GetModelCopyJob: %v", err)
	}

	if got.Status != bedrocktypes.ModelCopyJobStatusCompleted {
		t.Fatalf("got status %q, want Completed", got.Status)
	}

	if aws.ToString(got.SourceModelArn) != src || aws.ToString(got.TargetModelArn) == "" {
		t.Fatalf("unexpected copy job: %+v", got)
	}

	if aws.ToString(got.SourceAccountId) != "123456789012" {
		t.Fatalf("got source account %q", aws.ToString(got.SourceAccountId))
	}

	list, err := client.ListModelCopyJobs(ctx, &awsbedrock.ListModelCopyJobsInput{})
	if err != nil {
		t.Fatalf("ListModelCopyJobs: %v", err)
	}

	if len(list.ModelCopyJobSummaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(list.ModelCopyJobSummaries))
	}

	_, err = client.GetModelCopyJob(ctx, &awsbedrock.GetModelCopyJobInput{
		JobArn: aws.String("arn:aws:bedrock:us-east-1:123456789012:model-copy-job/missing"),
	})

	var nfe *bedrocktypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %T: %v", err, err)
	}
}

func evaluationConfig() bedrocktypes.EvaluationConfig {
	return &bedrocktypes.EvaluationConfigMemberAutomated{
		Value: bedrocktypes.AutomatedEvaluationConfig{
			DatasetMetricConfigs: []bedrocktypes.EvaluationDatasetMetricConfig{
				{
					TaskType:    bedrocktypes.EvaluationTaskTypeGeneration,
					Dataset:     &bedrocktypes.EvaluationDataset{Name: aws.String("ds")},
					MetricNames: []string{"Builtin.Accuracy"},
				},
			},
		},
	}
}

func inferenceConfig() bedrocktypes.EvaluationInferenceConfig {
	return &bedrocktypes.EvaluationInferenceConfigMemberModels{
		Value: []bedrocktypes.EvaluationModelConfig{
			&bedrocktypes.EvaluationModelConfigMemberBedrockModel{
				Value: bedrocktypes.EvaluationBedrockModel{ModelIdentifier: aws.String(claudeModel)},
			},
		},
	}
}

func TestSDKEvaluationJobLifecycle(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	create, err := client.CreateEvaluationJob(ctx, &awsbedrock.CreateEvaluationJobInput{
		JobName:          aws.String("eval-1"),
		RoleArn:          aws.String("arn:aws:iam::123456789012:role/bedrock"),
		EvaluationConfig: evaluationConfig(),
		InferenceConfig:  inferenceConfig(),
		OutputDataConfig: &bedrocktypes.EvaluationOutputDataConfig{S3Uri: aws.String("s3://bucket/eval/")},
	})
	if err != nil {
		t.Fatalf("CreateEvaluationJob: %v", err)
	}

	if aws.ToString(create.JobArn) == "" {
		t.Fatal("expected a job ARN")
	}

	got, err := client.GetEvaluationJob(ctx, &awsbedrock.GetEvaluationJobInput{JobIdentifier: aws.String("eval-1")})
	if err != nil {
		t.Fatalf("GetEvaluationJob: %v", err)
	}

	if got.Status != bedrocktypes.EvaluationJobStatusInProgress {
		t.Fatalf("got status %q, want InProgress", got.Status)
	}

	if got.JobType != bedrocktypes.EvaluationJobTypeAutomated {
		t.Fatalf("got job type %q, want Automated", got.JobType)
	}

	if _, ok := got.EvaluationConfig.(*bedrocktypes.EvaluationConfigMemberAutomated); !ok {
		t.Fatalf("expected automated evaluation config to round-trip, got %T", got.EvaluationConfig)
	}

	list, err := client.ListEvaluationJobs(ctx, &awsbedrock.ListEvaluationJobsInput{})
	if err != nil {
		t.Fatalf("ListEvaluationJobs: %v", err)
	}

	if len(list.JobSummaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(list.JobSummaries))
	}

	if _, err = client.StopEvaluationJob(ctx, &awsbedrock.StopEvaluationJobInput{
		JobIdentifier: aws.String("eval-1"),
	}); err != nil {
		t.Fatalf("StopEvaluationJob: %v", err)
	}

	stopped, err := client.GetEvaluationJob(ctx, &awsbedrock.GetEvaluationJobInput{JobIdentifier: aws.String("eval-1")})
	if err != nil {
		t.Fatalf("GetEvaluationJob after stop: %v", err)
	}

	if stopped.Status != bedrocktypes.EvaluationJobStatusStopped {
		t.Fatalf("got status %q, want Stopped", stopped.Status)
	}
}
