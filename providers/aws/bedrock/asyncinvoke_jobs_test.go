package bedrock

import (
	"context"
	"testing"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

func TestStartAsyncInvokeLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	inv, err := m.StartAsyncInvoke(ctx, bedrockdriver.StartAsyncInvokeConfig{
		ModelID:    titanModel,
		ModelInput: []byte(`{"inputText":"hello"}`),
		Output:     bedrockdriver.AsyncInvokeOutputConfig{S3URI: "s3://bucket/out/"},
		Tags:       map[string]string{"team": "ml"},
	})
	requireNoError(t, err)
	assertNotEmpty(t, inv.InvocationARN)
	assertNotEmpty(t, inv.ModelARN)
	assertEqual(t, bedrockdriver.AsyncCompleted, inv.Status)
	assertEqual(t, "s3://bucket/out/", inv.Output.S3URI)

	got, err := m.GetAsyncInvoke(ctx, inv.InvocationARN)
	requireNoError(t, err)
	assertEqual(t, inv.InvocationARN, got.InvocationARN)
	assertEqual(t, bedrockdriver.AsyncCompleted, got.Status)

	list, err := m.ListAsyncInvokes(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	tags, err := m.ListTagsForResource(ctx, inv.InvocationARN)
	requireNoError(t, err)
	assertEqual(t, 1, len(tags))
}

func TestStartAsyncInvokeValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.StartAsyncInvoke(ctx, bedrockdriver.StartAsyncInvokeConfig{
		ModelInput: []byte(`{}`),
		Output:     bedrockdriver.AsyncInvokeOutputConfig{S3URI: "s3://b/o"},
	})
	assertError(t, err, true)

	_, err = m.StartAsyncInvoke(ctx, bedrockdriver.StartAsyncInvokeConfig{
		ModelID:    "nope.unknown-v1",
		ModelInput: []byte(`{}`),
		Output:     bedrockdriver.AsyncInvokeOutputConfig{S3URI: "s3://b/o"},
	})
	assertError(t, err, true)
}

func TestGetAsyncInvokeNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.GetAsyncInvoke(context.Background(), "arn:aws:bedrock:us-east-1:123456789012:async-invoke/missing")
	assertError(t, err, true)
}

func TestModelImportJobLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	job, err := m.CreateModelImportJob(ctx, bedrockdriver.ModelImportJobConfig{
		JobName:              "import-1",
		ImportedModelName:    "my-imported",
		RoleARN:              "arn:aws:iam::123456789012:role/bedrock",
		ModelDataSourceS3URI: "s3://bucket/model/",
	})
	requireNoError(t, err)
	assertNotEmpty(t, job.JobARN)
	assertEqual(t, bedrockdriver.JobCompleted, job.Status)
	assertNotEmpty(t, job.ImportedModelARN)

	byName, err := m.GetModelImportJob(ctx, "import-1")
	requireNoError(t, err)
	assertEqual(t, job.JobARN, byName.JobARN)

	byARN, err := m.GetModelImportJob(ctx, job.JobARN)
	requireNoError(t, err)
	assertEqual(t, "import-1", byARN.JobName)

	list, err := m.ListModelImportJobs(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	_, err = m.GetModelImportJob(ctx, "missing")
	assertError(t, err, true)

	_, err = m.CreateModelImportJob(ctx, bedrockdriver.ModelImportJobConfig{ImportedModelName: "x"})
	assertError(t, err, true)
}

func TestModelCopyJobLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	src := "arn:aws:bedrock:us-east-1:123456789012:custom-model/src"
	job, err := m.CreateModelCopyJob(ctx, bedrockdriver.ModelCopyJobConfig{
		SourceModelARN:  src,
		TargetModelName: "copy-target",
	})
	requireNoError(t, err)
	assertNotEmpty(t, job.JobARN)
	assertEqual(t, bedrockdriver.JobCompleted, job.Status)
	assertEqual(t, "123456789012", job.SourceAccountID)
	assertNotEmpty(t, job.TargetModelARN)

	got, err := m.GetModelCopyJob(ctx, job.JobARN)
	requireNoError(t, err)
	assertEqual(t, src, got.SourceModelARN)

	list, err := m.ListModelCopyJobs(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	_, err = m.GetModelCopyJob(ctx, "arn:aws:bedrock:us-east-1:123456789012:model-copy-job/missing")
	assertError(t, err, true)

	_, err = m.CreateModelCopyJob(ctx, bedrockdriver.ModelCopyJobConfig{TargetModelName: "x"})
	assertError(t, err, true)
}

func TestEvaluationJobLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	job, err := m.CreateEvaluationJob(ctx, bedrockdriver.EvaluationJobConfig{
		JobName:          "eval-1",
		RoleARN:          "arn:aws:iam::123456789012:role/bedrock",
		EvaluationConfig: []byte(`{"automated":{}}`),
		InferenceConfig:  []byte(`{"models":[]}`),
		OutputDataS3URI:  "s3://bucket/eval/",
	})
	requireNoError(t, err)
	assertNotEmpty(t, job.JobARN)
	assertEqual(t, bedrockdriver.JobInProgress, job.Status)
	assertEqual(t, bedrockdriver.EvaluationTypeAutomated, job.JobType)

	got, err := m.GetEvaluationJob(ctx, "eval-1")
	requireNoError(t, err)
	assertEqual(t, job.JobARN, got.JobARN)

	byARN, err := m.GetEvaluationJob(ctx, job.JobARN)
	requireNoError(t, err)
	assertEqual(t, "eval-1", byARN.JobName)

	list, err := m.ListEvaluationJobs(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	requireNoError(t, m.StopEvaluationJob(ctx, "eval-1"))

	stopped, err := m.GetEvaluationJob(ctx, "eval-1")
	requireNoError(t, err)
	assertEqual(t, bedrockdriver.JobStopped, stopped.Status)

	// Stopping a job that is no longer in progress is rejected.
	assertError(t, m.StopEvaluationJob(ctx, "eval-1"), true)

	assertError(t, m.StopEvaluationJob(ctx, "missing"), true)
}

func TestEvaluationJobTypeHuman(t *testing.T) {
	m := newTestMock()

	job, err := m.CreateEvaluationJob(context.Background(), bedrockdriver.EvaluationJobConfig{
		JobName:          "eval-human",
		RoleARN:          "arn:aws:iam::123456789012:role/bedrock",
		EvaluationConfig: []byte(`{"human":{"humanWorkflowConfig":{}}}`),
		InferenceConfig:  []byte(`{"models":[]}`),
		OutputDataS3URI:  "s3://bucket/eval/",
	})
	requireNoError(t, err)
	assertEqual(t, bedrockdriver.EvaluationTypeHuman, job.JobType)
}
