package sagemaker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/config"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/sagemaker/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
	)

	return New(opts)
}

func TestTrainingJobLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	job, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{
		JobName:        "train-1",
		RoleARN:        "arn:aws:iam::123456789012:role/svc",
		AlgorithmImage: "xgboost:latest",
		OutputS3URI:    "s3://bucket/out",
	})
	require.NoError(t, err)
	assert.Equal(t, driver.JobCompleted, job.Status)
	assert.Equal(t, driver.SecondaryCompleted, job.SecondaryStatus)
	assert.NotEmpty(t, job.SecondaryTransitions)
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:training-job/train-1", job.JobARN)
	assert.Equal(t, "s3://bucket/out/output/model.tar.gz", job.ModelArtifactS3URI)

	got, err := m.DescribeTrainingJob(ctx, "train-1")
	require.NoError(t, err)
	assert.Equal(t, "train-1", got.JobName)

	list, err := m.ListTrainingJobs(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	require.NoError(t, m.StopTrainingJob(ctx, "train-1"))
	got, _ = m.DescribeTrainingJob(ctx, "train-1")
	assert.Equal(t, driver.JobStopped, got.Status)
}

func TestTrainingJobDuplicateAndMissing(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "dup"})
	require.NoError(t, err)
	_, err = m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "dup"})
	require.Error(t, err)

	_, err = m.CreateTrainingJob(ctx, driver.TrainingJobConfig{})
	require.Error(t, err)

	_, err = m.DescribeTrainingJob(ctx, "nope")
	require.Error(t, err)
}

func TestAllJobKindsComplete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	pj, err := m.CreateProcessingJob(ctx, driver.ProcessingJobConfig{JobName: "p1"})
	require.NoError(t, err)
	assert.Equal(t, driver.JobCompleted, pj.Status)

	_, err = m.CreateModel(ctx, driver.ModelConfig{ModelName: "m1"})
	require.NoError(t, err)
	tj, err := m.CreateTransformJob(ctx, driver.TransformJobConfig{JobName: "t1", ModelName: "m1"})
	require.NoError(t, err)
	assert.Equal(t, driver.JobCompleted, tj.Status)

	hj, err := m.CreateHyperParameterTuningJob(ctx, driver.HyperParameterTuningJobConfig{JobName: "h1", MaxJobs: 4})
	require.NoError(t, err)
	assert.Equal(t, "h1-001", hj.BestTrainingJob)

	aj, err := m.CreateAutoMLJobV2(ctx, driver.AutoMLJobConfig{JobName: "a1"})
	require.NoError(t, err)
	assert.Equal(t, "a1-best", aj.BestCandidateName)

	lj, err := m.CreateLabelingJob(ctx, driver.LabelingJobConfig{JobName: "l1"})
	require.NoError(t, err)
	assert.Equal(t, driver.JobCompleted, lj.Status)

	cj, err := m.CreateCompilationJob(ctx, driver.CompilationJobConfig{JobName: "c1"})
	require.NoError(t, err)
	assert.Equal(t, driver.JobCompleted, cj.Status)
}

func TestInferenceFlowAndInvoke(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "model-a", RoleARN: "role"})
	require.NoError(t, err)

	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{
		ConfigName: "cfg-a",
		ProductionVariants: []driver.ProductionVariant{
			{VariantName: "v1", ModelName: "model-a", InstanceType: "ml.m5.large", InitialInstanceCount: 1, InitialVariantWeight: 1},
		},
	})
	require.NoError(t, err)

	ep, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep-a", ConfigName: "cfg-a"})
	require.NoError(t, err)
	assert.Equal(t, driver.EndpointInService, ep.Status)
	assert.Len(t, ep.Variants, 1)

	out, err := m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{
		EndpointName: "ep-a", ContentType: "application/json", Body: []byte(`{"x":1}`),
	})
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"x":1}`), out.Body)
	assert.Equal(t, "v1", out.InvokedVariant)

	_, err = m.UpdateEndpointWeightsAndCapacities(ctx, "ep-a", []driver.VariantWeight{
		{VariantName: "v1", DesiredWeight: 0.5, DesiredInstanceCount: 3},
	})
	require.NoError(t, err)
	got, _ := m.DescribeEndpoint(ctx, "ep-a")
	assert.InDelta(t, 0.5, got.Variants[0].InitialVariantWeight, 0.001)
	assert.Equal(t, 3, got.Variants[0].InitialInstanceCount)
}

func TestInvokeEndpointErrors(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "missing"})
	require.Error(t, err)
}

func TestEndpointRequiresExistingConfig(t *testing.T) {
	m := newTestMock()
	_, err := m.CreateEndpoint(context.Background(), driver.EndpointSpec{EndpointName: "e", ConfigName: "nope"})
	require.Error(t, err)
}

func TestModelRegistryVersioning(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateModelPackageGroup(ctx, driver.ModelPackageGroupSpec{GroupName: "grp"})
	require.NoError(t, err)

	p1, err := m.CreateModelPackage(ctx, driver.ModelPackageSpec{GroupName: "grp"})
	require.NoError(t, err)
	assert.Equal(t, 1, p1.Version)
	assert.Equal(t, driver.ApprovalPendingManual, p1.ApprovalStatus)

	p2, err := m.CreateModelPackage(ctx, driver.ModelPackageSpec{GroupName: "grp"})
	require.NoError(t, err)
	assert.Equal(t, 2, p2.Version)

	upd, err := m.UpdateModelPackage(ctx, p2.PackageARN, driver.ApprovalApproved)
	require.NoError(t, err)
	assert.Equal(t, driver.ApprovalApproved, upd.ApprovalStatus)

	pkgs, err := m.ListModelPackages(ctx, "grp")
	require.NoError(t, err)
	assert.Len(t, pkgs, 2)
}

func TestStudioDomainAndChildren(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	d, err := m.CreateDomain(ctx, driver.DomainSpec{DomainName: "team", AuthMode: "IAM"})
	require.NoError(t, err)
	assert.Contains(t, d.DomainID, "d-")
	assert.Equal(t, driver.StudioInService, d.Status)

	_, err = m.CreateUserProfile(ctx, driver.UserProfileSpec{DomainID: d.DomainID, UserProfileName: "alice"})
	require.NoError(t, err)
	_, err = m.CreateUserProfile(ctx, driver.UserProfileSpec{DomainID: "d-missing", UserProfileName: "bob"})
	require.Error(t, err)

	app, err := m.CreateApp(ctx, driver.AppSpec{
		DomainID: d.DomainID, UserProfileName: "alice", AppType: "JupyterServer", AppName: "default",
	})
	require.NoError(t, err)
	assert.Equal(t, driver.AppInService, app.Status)

	apps, err := m.ListApps(ctx, d.DomainID)
	require.NoError(t, err)
	assert.Len(t, apps, 1)
}

func TestNotebookInstanceStartStop(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	nb, err := m.CreateNotebookInstance(ctx, driver.NotebookInstanceSpec{Name: "nb", InstanceType: "ml.t3.medium"})
	require.NoError(t, err)
	assert.Equal(t, driver.NotebookInService, nb.Status)

	require.NoError(t, m.StopNotebookInstance(ctx, "nb"))
	got, _ := m.DescribeNotebookInstance(ctx, "nb")
	assert.Equal(t, driver.NotebookStopped, got.Status)

	require.NoError(t, m.StartNotebookInstance(ctx, "nb"))
	got, _ = m.DescribeNotebookInstance(ctx, "nb")
	assert.Equal(t, driver.NotebookInService, got.Status)
}

func TestHyperPodClusterNodes(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	c, err := m.CreateCluster(ctx, driver.ClusterSpec{
		ClusterName: "cl",
		InstanceGroups: []driver.ClusterInstanceGroupSpec{
			{GroupName: "workers", InstanceType: "ml.p4d.24xlarge", InstanceCount: 3},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, driver.ClusterInService, c.Status)

	nodes, err := m.ListClusterNodes(ctx, "cl")
	require.NoError(t, err)
	assert.Len(t, nodes, 3)

	node, err := m.DescribeClusterNode(ctx, "cl", "workers-0")
	require.NoError(t, err)
	assert.Equal(t, "Running", node.Status)
}

func TestFeatureStoreRuntime(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateFeatureGroup(ctx, driver.FeatureGroupSpec{
		GroupName:            "fg",
		RecordIdentifierName: "id",
		EventTimeFeatureName: "ts",
		OnlineStoreEnabled:   true,
		Features:             []driver.FeatureDefinition{{Name: "id", Type: "String"}, {Name: "score", Type: "Fractional"}},
	})
	require.NoError(t, err)

	rec := []driver.FeatureValue{{Name: "id", Value: "u1"}, {Name: "score", Value: "0.9"}}
	require.NoError(t, m.PutRecord(ctx, "fg", rec))

	got, err := m.GetRecord(ctx, "fg", "u1")
	require.NoError(t, err)
	assert.Len(t, got, 2)

	require.NoError(t, m.DeleteRecord(ctx, "fg", "u1"))
	_, err = m.GetRecord(ctx, "fg", "u1")
	require.Error(t, err)

	// Missing identifier feature is rejected.
	require.Error(t, m.PutRecord(ctx, "fg", []driver.FeatureValue{{Name: "score", Value: "1"}}))
}

func TestPipelineExecutionAndExperiments(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreatePipeline(ctx, driver.PipelineSpec{PipelineName: "pl", Definition: "{}"})
	require.NoError(t, err)

	ex, err := m.StartPipelineExecution(ctx, "pl")
	require.NoError(t, err)
	assert.Equal(t, driver.ExecutionSucceeded, ex.Status)

	require.NoError(t, m.StopPipelineExecution(ctx, ex.ExecutionARN))
	got, _ := m.DescribePipelineExecution(ctx, ex.ExecutionARN)
	assert.Equal(t, driver.ExecutionStopped, got.Status)

	_, err = m.CreateExperiment(ctx, driver.ExperimentSpec{ExperimentName: "exp"})
	require.NoError(t, err)
	_, err = m.CreateTrial(ctx, driver.TrialSpec{TrialName: "tr", ExperimentName: "exp"})
	require.NoError(t, err)
	trials, err := m.ListTrials(ctx, "exp")
	require.NoError(t, err)
	assert.Len(t, trials, 1)
}

func TestTagsLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	job, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{
		JobName: "tagged",
		Tags:    []driver.Tag{{Key: "team", Value: "ml"}},
	})
	require.NoError(t, err)

	tags, err := m.ListTags(ctx, job.JobARN)
	require.NoError(t, err)
	assert.Len(t, tags, 1)

	_, err = m.AddTags(ctx, job.JobARN, []driver.Tag{{Key: "env", Value: "test"}, {Key: "team", Value: "ai"}})
	require.NoError(t, err)
	tags, _ = m.ListTags(ctx, job.JobARN)
	assert.Len(t, tags, 2)

	require.NoError(t, m.DeleteTags(ctx, job.JobARN, []string{"env"}))
	tags, _ = m.ListTags(ctx, job.JobARN)
	assert.Len(t, tags, 1)
	assert.Equal(t, "ai", tags[0].Value)
}

func TestAutoMetricsEmission(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	m := New(opts)
	ctx := context.Background()

	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "metric-job"})
	require.NoError(t, err)

	result, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace:  "AWS/SageMaker",
		MetricName: "JobsCreated",
		Dimensions: map[string]string{"JobType": "TrainingJob"},
		StartTime:  fc.Now().Add(-time.Hour),
		EndTime:    fc.Now().Add(time.Hour),
		Period:     60,
		Stat:       "Sum",
	})
	require.NoError(t, err)
	assert.True(t, len(result.Values) > 0)
}
