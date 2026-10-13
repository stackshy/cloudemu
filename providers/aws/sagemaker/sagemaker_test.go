package sagemaker

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
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

	list, err := m.ListTrainingJobs(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, list.Items, 1)

	// A job that already Completed cannot be stopped: Stop succeeds and the
	// job stays Completed (see TestStopTrainingJob_* for the in-progress cases).
	require.NoError(t, m.StopTrainingJob(ctx, "train-1"))
	got, _ = m.DescribeTrainingJob(ctx, "train-1")
	assert.Equal(t, driver.JobCompleted, got.Status)
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
	assert.Equal(t, driver.CompilationCompleted, cj.Status)
}

func TestInferenceFlowAndInvoke(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "model-a", RoleARN: "role"})
	require.NoError(t, err)

	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{
		ConfigName: "cfg-a",
		ProductionVariants: []driver.ProductionVariant{
			{VariantName: "v1", ModelName: "model-a", InstanceType: "ml.m5.large", InitialInstanceCount: 1, InitialVariantWeight: ptr(1.0)},
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
		{VariantName: "v1", DesiredWeight: ptr(0.5), DesiredInstanceCount: ptr(3)},
	})
	require.NoError(t, err)
	got, _ := m.DescribeEndpoint(ctx, "ep-a")
	require.Len(t, got.VariantSummaries, 1)
	assert.InDelta(t, 0.5, got.VariantSummaries[0].DesiredWeight, 0.001)
	assert.Equal(t, 3, got.VariantSummaries[0].DesiredInstanceCount)
	require.NotNil(t, got.Variants[0].InitialVariantWeight)
	assert.InDelta(t, 1.0, *got.Variants[0].InitialVariantWeight, 0.001, "the endpoint config's initial values are not overwritten")
	assert.Equal(t, 1, got.Variants[0].InitialInstanceCount)
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

	pkgs, err := m.ListModelPackages(ctx, "grp", driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, pkgs.Items, 2)
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

	apps, err := m.ListApps(ctx, d.DomainID, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, apps.Items, 1)
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

	nodes := listNodes(t, m, "cl")
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

	// Every emulated run has already finished, so Stop leaves it as it is.
	require.NoError(t, m.StopPipelineExecution(ctx, ex.ExecutionARN))
	got, _ := m.DescribePipelineExecution(ctx, ex.ExecutionARN)
	assert.Equal(t, driver.ExecutionSucceeded, got.Status)

	_, err = m.CreateExperiment(ctx, driver.ExperimentSpec{ExperimentName: "exp"})
	require.NoError(t, err)
	_, err = m.CreateTrial(ctx, driver.TrialSpec{TrialName: "tr", ExperimentName: "exp"})
	require.NoError(t, err)
	trials, err := m.ListTrials(ctx, "exp", driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, trials.Items, 1)
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

// metricsRig is a Mock wired to a CloudWatch mock over a fake clock.
type metricsRig struct {
	m   *Mock
	cw  *cloudwatch.Mock
	fc  *config.FakeClock
	ctx context.Context
}

func newMetricsRig() *metricsRig {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	m := New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	return &metricsRig{m: m, cw: cw, fc: fc, ctx: context.Background()}
}

// sum returns the total of a metric's datapoints for the exact dimensions.
func (r *metricsRig) sum(t *testing.T, ns, name string, dims map[string]string) float64 {
	t.Helper()

	res, err := r.cw.GetMetricData(r.ctx, mondriver.GetMetricInput{
		Namespace: ns, MetricName: name, Dimensions: dims,
		StartTime: r.fc.Now().Add(-time.Hour), EndTime: r.fc.Now().Add(time.Hour), Period: 60, Stat: "Sum",
	})
	require.NoError(t, err)

	total := 0.0
	for _, v := range res.Values {
		total += v
	}

	return total
}

func (r *metricsRig) endpoint(t *testing.T, name string) {
	t.Helper()

	_, err := r.m.CreateModel(r.ctx, driver.ModelConfig{ModelName: "mdl"})
	require.NoError(t, err)
	_, err = r.m.CreateEndpointConfig(r.ctx, driver.EndpointConfigSpec{
		ConfigName: "cfg",
		ProductionVariants: []driver.ProductionVariant{
			{VariantName: "AllTraffic", ModelName: "mdl", InstanceType: "ml.m5.large", InitialInstanceCount: 2, InitialVariantWeight: ptr(1.0)},
		},
	})
	require.NoError(t, err)
	_, err = r.m.CreateEndpoint(r.ctx, driver.EndpointSpec{EndpointName: name, ConfigName: "cfg"})
	require.NoError(t, err)
}

func TestNoFabricatedMetrics(t *testing.T) { // T130
	r := newMetricsRig()
	r.endpoint(t, "ep")

	_, err := r.m.CreateTrainingJob(r.ctx, driver.TrainingJobConfig{JobName: "tj"})
	require.NoError(t, err)
	_, err = r.m.CreatePipeline(r.ctx, driver.PipelineSpec{PipelineName: "pl"})
	require.NoError(t, err)
	_, err = r.m.CreateNotebookInstance(r.ctx, driver.NotebookInstanceSpec{Name: "nb", InstanceType: "ml.t3.medium"})
	require.NoError(t, err)

	for _, ns := range []string{"AWS/SageMaker", "/aws/sagemaker/TrainingJobs"} {
		names, err := r.cw.ListMetrics(r.ctx, ns)
		require.NoError(t, err)
		assert.NotContains(t, names, "JobsCreated", ns)
		assert.NotContains(t, names, "ResourcesCreated", ns)
	}
}

func TestInvokeMetrics_DimsAndUnits(t *testing.T) { // T131
	r := newMetricsRig()
	r.endpoint(t, "ep")

	_, err := r.m.InvokeEndpoint(r.ctx, driver.InvokeEndpointInput{EndpointName: "ep", Body: []byte("x")})
	require.NoError(t, err)

	dims := map[string]string{"EndpointName": "ep", "VariantName": "AllTraffic"}
	assert.InDelta(t, 1, r.sum(t, "AWS/SageMaker", "Invocations", dims), 0)
	// AWS emits 1/numberOfInstances per request: this variant runs 2 instances.
	assert.InDelta(t, 0.5, r.sum(t, "AWS/SageMaker", "InvocationsPerInstance", dims), 0.0001)
	assert.Positive(t, r.sum(t, "AWS/SageMaker", "ModelLatency", dims))
	assert.Positive(t, r.sum(t, "AWS/SageMaker", "OverheadLatency", dims))
	// The fabricated EndpointName-only series no longer exists.
	assert.Zero(t, r.sum(t, "AWS/SageMaker", "Invocations", map[string]string{"EndpointName": "ep"}))

	units := r.cw.MetricUnits(r.ctx, &mondriver.GetMetricInput{
		Namespace: "AWS/SageMaker", MetricName: "ModelLatency", Dimensions: dims,
		StartTime: r.fc.Now().Add(-time.Hour), EndTime: r.fc.Now().Add(time.Hour),
	})
	assert.Equal(t, []string{"Microseconds"}, units)

	// A missing endpoint publishes nothing.
	_, err = r.m.InvokeEndpoint(r.ctx, driver.InvokeEndpointInput{EndpointName: "ghost"})
	require.Error(t, err)
	assert.Zero(t, r.sum(t, "AWS/SageMaker", "Invocations", map[string]string{"EndpointName": "ghost", "VariantName": "AllTraffic"}))
}

func TestInvokeMetrics_Errors(t *testing.T) { // T132
	r := newMetricsRig()
	r.endpoint(t, "ep")

	dims := map[string]string{"EndpointName": "ep", "VariantName": "AllTraffic"}

	// 4XX: the caller named a variant the endpoint does not have.
	_, err := r.m.InvokeEndpoint(r.ctx, driver.InvokeEndpointInput{EndpointName: "ep", TargetVariant: "nope"})
	require.Error(t, err)
	assert.InDelta(t, 1, r.sum(t, "AWS/SageMaker", "Invocation4XXErrors", map[string]string{"EndpointName": "ep", "VariantName": "nope"}), 0)

	// 5XX from the model container: ModelError (424) plus the 5XX and model-error counters.
	r.m.SetInvocationFailure("ep", 500)

	_, err = r.m.InvokeEndpoint(r.ctx, driver.InvokeEndpointInput{EndpointName: "ep"})

	var me *driver.ModelError
	require.ErrorAs(t, err, &me)
	assert.Equal(t, 500, me.OriginalStatusCode)
	assert.InDelta(t, 1, r.sum(t, "AWS/SageMaker", "Invocation5XXErrors", dims), 0)
	assert.InDelta(t, 1, r.sum(t, "AWS/SageMaker", "InvocationModelErrors", dims), 0)
	assert.Zero(t, r.sum(t, "AWS/SageMaker", "Invocations", dims))

	// A container 4XX counts as 4XX, and clearing the seed restores success.
	r.m.SetInvocationFailure("ep", 422)

	_, err = r.m.InvokeEndpoint(r.ctx, driver.InvokeEndpointInput{EndpointName: "ep"})
	require.Error(t, err)
	assert.InDelta(t, 1, r.sum(t, "AWS/SageMaker", "Invocation4XXErrors", dims), 0)

	r.m.SetInvocationFailure("ep", 0)

	_, err = r.m.InvokeEndpoint(r.ctx, driver.InvokeEndpointInput{EndpointName: "ep"})
	require.NoError(t, err)
}

func TestJobUtilizationMetrics(t *testing.T) { // T133
	r := newMetricsRig()

	_, err := r.m.CreateTrainingJob(r.ctx, driver.TrainingJobConfig{
		JobName: "tj", Resources: driver.ResourceConfig{InstanceType: "ml.p3.2xlarge", InstanceCount: 2},
	})
	require.NoError(t, err)

	ns := "/aws/sagemaker/TrainingJobs"

	for _, host := range []string{"tj/algo-1", "tj/algo-2"} {
		d := map[string]string{"Host": host}
		for _, name := range []string{"CPUUtilization", "MemoryUtilization", "DiskUtilization", "GPUUtilization", "GPUMemoryUtilization"} {
			assert.Positive(t, r.sum(t, ns, name, d), host+" "+name)
		}
	}

	// CPU instance: no GPU series.
	_, err = r.m.CreateProcessingJob(r.ctx, driver.ProcessingJobConfig{
		JobName: "pj", Resources: driver.ResourceConfig{InstanceType: "ml.m5.xlarge", InstanceCount: 1},
	})
	require.NoError(t, err)

	pd := map[string]string{"Host": "pj/algo-1"}
	assert.Positive(t, r.sum(t, "/aws/sagemaker/ProcessingJobs", "CPUUtilization", pd))
	assert.Zero(t, r.sum(t, "/aws/sagemaker/ProcessingJobs", "GPUUtilization", pd))

	// Transform: host is an instance id and there is no DiskUtilization.
	_, err = r.m.CreateTransformJob(r.ctx, driver.TransformJobConfig{JobName: "xf", ModelName: "mdl", InstanceType: "ml.m5.large", InstanceCount: 1})
	require.NoError(t, err)

	names, err := r.cw.ListMetrics(r.ctx, "/aws/sagemaker/TransformJobs")
	require.NoError(t, err)
	assert.Contains(t, names, "CPUUtilization")
	assert.NotContains(t, names, "DiskUtilization")
}

func TestPipelineMetrics(t *testing.T) { // T134
	r := newMetricsRig()

	_, err := r.m.CreatePipeline(r.ctx, driver.PipelineSpec{PipelineName: "pl"})
	require.NoError(t, err)
	_, err = r.m.StartPipelineExecution(r.ctx, "pl")
	require.NoError(t, err)

	d := map[string]string{"PipelineName": "pl"}
	assert.InDelta(t, 1, r.sum(t, "AWS/Sagemaker/ModelBuildingPipeline", "ExecutionStarted", d), 0)
	assert.InDelta(t, 1, r.sum(t, "AWS/Sagemaker/ModelBuildingPipeline", "ExecutionSucceeded", d), 0)
}

func TestMetrics_NilMonitoringIsSafe(t *testing.T) { // T135
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "mdl"})
	require.NoError(t, err)
	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{
		ConfigName:         "cfg",
		ProductionVariants: []driver.ProductionVariant{{VariantName: "v", ModelName: "mdl", InstanceType: "ml.m5.large", InitialInstanceCount: 1, InitialVariantWeight: ptr(1.0)}},
	})
	require.NoError(t, err)
	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: "cfg"})
	require.NoError(t, err)
	_, err = m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep"})
	require.NoError(t, err)
	_, err = m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "tj"})
	require.NoError(t, err)
}

func newTestMockClock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
	)

	return New(opts), fc
}

func createJobs(t *testing.T, m *Mock, fc *config.FakeClock, names ...string) {
	t.Helper()

	for _, n := range names {
		_, err := m.CreateTrainingJob(context.Background(), driver.TrainingJobConfig{JobName: n, OutputS3URI: "s3://b"})
		require.NoError(t, err)
		fc.Advance(time.Minute)
	}
}

func TestList_Paging_SizeOneWalksAll(t *testing.T) { // T1
	m, fc := newTestMockClock()
	ctx := context.Background()
	names := make([]string, 0, 25)

	for i := 0; i < 25; i++ {
		names = append(names, "job-"+string(rune('a'+i)))
	}

	createJobs(t, m, fc, names...)

	seen := map[string]int{}
	token := ""

	for pages := 0; pages < 100; pages++ {
		page, err := m.ListTrainingJobs(ctx, driver.ListOptions{MaxResults: 1, NextToken: token})
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		seen[page.Items[0].JobName]++

		if page.NextToken == "" {
			break
		}

		token = page.NextToken
	}

	assert.Len(t, seen, 25)

	for n, c := range seen {
		assert.Equal(t, 1, c, n)
	}
}

func TestList_BadToken(t *testing.T) { // T2
	m := newTestMock()
	_, err := m.ListTrainingJobs(context.Background(), driver.ListOptions{NextToken: "not-a-token", MaxResults: 5})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestList_MaxResultsBounds(t *testing.T) { // T3
	m, fc := newTestMockClock()
	createJobs(t, m, fc, "a", "b", "c")
	ctx := context.Background()

	all, err := m.ListTrainingJobs(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all.Items, 3, "zero MaxResults means all at driver level")

	for _, bad := range []int{101, -1} {
		_, err = m.ListTrainingJobs(ctx, driver.ListOptions{MaxResults: bad})
		require.Error(t, err, bad)
		assert.True(t, errors.IsInvalidArgument(err), bad)
	}

	for _, ok := range []int{1, 100} {
		_, err = m.ListTrainingJobs(ctx, driver.ListOptions{MaxResults: ok})
		require.NoError(t, err, ok)
	}
}

func TestList_FiltersAndSort(t *testing.T) { // T4
	m, fc := newTestMockClock()
	ctx := context.Background()
	createJobs(t, m, fc, "beta", "alpha-train", "gamma-train")

	names := func(o driver.ListOptions) []string {
		page, err := m.ListTrainingJobs(ctx, o)
		require.NoError(t, err)

		out := make([]string, 0, len(page.Items))
		for _, j := range page.Items {
			out = append(out, j.JobName)
		}

		return out
	}

	assert.Equal(t, []string{"beta", "alpha-train", "gamma-train"}, names(driver.ListOptions{}), "default CreationTime ascending")
	assert.Equal(t, []string{"gamma-train", "alpha-train", "beta"},
		names(driver.ListOptions{SortBy: driver.SortByCreationTime, SortOrder: driver.SortDescending}))
	assert.Equal(t, []string{"alpha-train", "beta", "gamma-train"}, names(driver.ListOptions{SortBy: driver.SortByName}))
	assert.Equal(t, []string{"alpha-train", "gamma-train"}, names(driver.ListOptions{NameContains: "train", SortBy: driver.SortByName}))
	assert.Equal(t, []string{"beta", "alpha-train", "gamma-train"}, names(driver.ListOptions{StatusEquals: driver.JobCompleted}))
	assert.Empty(t, names(driver.ListOptions{StatusEquals: driver.JobStopped}))

	// "beta" was created at 00:00, "alpha-train" at 00:01, "gamma-train" at 00:02.
	after := time.Date(2025, 1, 1, 0, 0, 30, 0, time.UTC).Format(time.RFC3339Nano)
	before := time.Date(2025, 1, 1, 0, 1, 30, 0, time.UTC).Format(time.RFC3339Nano)

	assert.Equal(t, []string{"alpha-train", "gamma-train"}, names(driver.ListOptions{CreationTimeAfter: after}))
	assert.Equal(t, []string{"beta", "alpha-train"}, names(driver.ListOptions{CreationTimeBefore: before}))
	assert.Equal(t, []string{"alpha-train"}, names(driver.ListOptions{CreationTimeAfter: after, CreationTimeBefore: before}))

	_, err := m.ListTrainingJobs(ctx, driver.ListOptions{SortBy: "Bogus"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.ListTrainingJobs(ctx, driver.ListOptions{SortOrder: "Sideways"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestList_EmptyIsEmptySlice(t *testing.T) { // T5
	m := newTestMock()
	page, err := m.ListTrainingJobs(context.Background(), driver.ListOptions{})
	require.NoError(t, err)
	assert.NotNil(t, page.Items)
	assert.Empty(t, page.Items)
	assert.Empty(t, page.NextToken)
}

// raceOnce runs create from 50 goroutines and returns how many succeeded.
func raceOnce(create func() error) int {
	const workers = 50

	var (
		wg   sync.WaitGroup
		wins atomic.Int32
	)

	start := make(chan struct{})

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			if create() == nil {
				wins.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	return int(wins.Load())
}

func TestCreateSameName_Concurrent50(t *testing.T) { // T18
	ctx := context.Background()
	m := newTestMock()

	creators := map[string]func() error{
		"training job": func() error {
			_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "j"})
			return err
		},
		"processing job": func() error {
			_, err := m.CreateProcessingJob(ctx, driver.ProcessingJobConfig{JobName: "j"})
			return err
		},
		"model": func() error {
			_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "m"})
			return err
		},
		"endpoint config": func() error {
			_, err := m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{ConfigName: "c", ProductionVariants: []driver.ProductionVariant{{VariantName: "v"}}})
			return err
		},
		"notebook": func() error {
			_, err := m.CreateNotebookInstance(ctx, driver.NotebookInstanceSpec{Name: "n"})
			return err
		},
		"pipeline": func() error {
			_, err := m.CreatePipeline(ctx, driver.PipelineSpec{PipelineName: "p"})
			return err
		},
		"experiment": func() error {
			_, err := m.CreateExperiment(ctx, driver.ExperimentSpec{ExperimentName: "e"})
			return err
		},
		"package group": func() error {
			_, err := m.CreateModelPackageGroup(ctx, driver.ModelPackageGroupSpec{GroupName: "g"})
			return err
		},
		"domain user profile": func() error {
			_, err := m.CreateUserProfile(ctx, driver.UserProfileSpec{DomainID: "d-1", UserProfileName: "u"})
			return err
		},
	}

	_, err := m.CreateDomain(ctx, driver.DomainSpec{DomainName: "dom"})
	require.NoError(t, err)

	creators["domain user profile"] = func() error {
		doms, _ := m.ListDomains(ctx, driver.ListOptions{})
		_, err := m.CreateUserProfile(ctx, driver.UserProfileSpec{DomainID: doms.Items[0].DomainID, UserProfileName: "u"})

		return err
	}

	for name, create := range creators {
		assert.Equal(t, 1, raceOnce(create), name)
	}
}

func TestDeletePurgesTags(t *testing.T) { // T19
	ctx := context.Background()
	m := newTestMock()

	model, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "tagged", Tags: []driver.Tag{{Key: "k", Value: "v"}}})
	require.NoError(t, err)

	tags, err := m.ListTags(ctx, model.ModelARN)
	require.NoError(t, err)
	assert.Len(t, tags, 1)

	require.NoError(t, m.DeleteModel(ctx, "tagged"))

	_, err = m.ListTags(ctx, model.ModelARN)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "tags of a deleted resource are gone")

	again, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "tagged"})
	require.NoError(t, err)

	tags, err = m.ListTags(ctx, again.ModelARN)
	require.NoError(t, err)
	assert.Empty(t, tags, "a re-created resource does not inherit tags")
}

func TestTags_Validation(t *testing.T) { // T20
	ctx := context.Background()
	m := newTestMock()

	model, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "m"})
	require.NoError(t, err)

	many := make([]driver.Tag, 0, 51)
	for i := 0; i < 51; i++ {
		many = append(many, driver.Tag{Key: "k" + string(rune('A'+i)), Value: "v"})
	}

	_, err = m.AddTags(ctx, model.ModelARN, many)
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err), "51 tags exceed the cap")

	_, err = m.AddTags(ctx, model.ModelARN, many[:50])
	require.NoError(t, err)

	_, err = m.AddTags(ctx, model.ModelARN, []driver.Tag{{Key: "one-more", Value: "v"}})
	require.Error(t, err, "the 51st distinct key is rejected")

	_, err = m.AddTags(ctx, model.ModelARN, []driver.Tag{{Key: many[0].Key, Value: "new"}})
	require.NoError(t, err, "overwriting an existing key stays within the cap")

	_, err = m.AddTags(ctx, model.ModelARN, []driver.Tag{{Key: "", Value: "v"}})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.AddTags(ctx, "arn:aws:sagemaker:us-east-1:123456789012:model/missing", many[:1])
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "tagging a missing resource is not a silent success")

	_, err = m.AddTags(ctx, "not-an-arn", many[:1])
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	assert.Error(t, m.DeleteTags(ctx, "arn:aws:sagemaker:us-east-1:123456789012:model/missing", []string{"k"}))
}

func TestTags_ConcurrentAdd(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	model, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "m"})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := 0; i < 40; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			_, _ = m.AddTags(ctx, model.ModelARN, []driver.Tag{{Key: "key-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Value: "v"}})
		}(i)
	}

	wg.Wait()

	tags, err := m.ListTags(ctx, model.ModelARN)
	require.NoError(t, err)
	assert.Len(t, tags, 40, "no concurrent AddTags update is lost")
}

func TestTimesAreClockDriven(t *testing.T) { // T12
	m, fc := newTestMockClock()
	ctx := context.Background()

	first, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "a"})
	require.NoError(t, err)

	fc.Advance(250 * time.Millisecond)

	second, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "b"})
	require.NoError(t, err)

	t1, err := time.Parse(time.RFC3339Nano, first.CreationTime)
	require.NoError(t, err)

	t2, err := time.Parse(time.RFC3339Nano, second.CreationTime)
	require.NoError(t, err)

	assert.Equal(t, 250*time.Millisecond, t2.Sub(t1), "sub-second Clock movement is visible in stored times")
	assert.Equal(t, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), t1, "times come from the Clock, not time.Now")
}

func ptr[T any](v T) *T { return &v }

func newAsyncMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
		config.WithAsyncSettle(),
	)

	return New(opts), fc
}

// seedEndpoint creates a model, an endpoint config "cfg-<suffix>" with one
// variant, and returns the config name.
func seedConfig(t *testing.T, m *Mock, suffix string, count int, weight float64) string {
	t.Helper()

	ctx := context.Background()
	if !m.models.Has("model-a") {
		_, err := m.CreateModel(ctx, driver.ModelConfig{
			ModelName: "model-a", RoleARN: "role",
			Containers: []driver.ContainerDefinition{{Image: "123.dkr.ecr/model-a:1"}},
		})
		require.NoError(t, err)
	}

	name := "cfg-" + suffix
	_, err := m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{
		ConfigName: name,
		ProductionVariants: []driver.ProductionVariant{
			{VariantName: "v1", ModelName: "model-a", InstanceType: "ml.m5.large", InitialInstanceCount: count, InitialVariantWeight: ptr(weight)},
		},
	})
	require.NoError(t, err)

	return name
}

func TestEndpoint_SyncDefaultIsInService(t *testing.T) { // T35
	m := newTestMock()
	ctx := context.Background()
	cfg := seedConfig(t, m, "a", 2, 1)

	ep, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfg})
	require.NoError(t, err)
	assert.Equal(t, driver.EndpointInService, ep.Status)
	assert.Nil(t, ep.Pending)

	require.Len(t, ep.VariantSummaries, 1)
	vs := ep.VariantSummaries[0]
	assert.Equal(t, "v1", vs.VariantName)
	assert.Equal(t, 2, vs.CurrentInstanceCount)
	assert.Equal(t, 2, vs.DesiredInstanceCount)
	assert.InDelta(t, 1.0, vs.CurrentWeight, 0.001)
	require.Len(t, vs.DeployedImages, 1)
	assert.Equal(t, "123.dkr.ecr/model-a:1", vs.DeployedImages[0].SpecifiedImage)

	require.NoError(t, m.DeleteEndpoint(ctx, "ep"))

	_, err = m.DescribeEndpoint(ctx, "ep")
	require.Error(t, err, "sync delete is immediate")
}

func TestEndpoint_AsyncSettle_CreatingThenInService(t *testing.T) { // T36, T39
	m, fc := newAsyncMock()
	ctx := context.Background()
	cfg := seedConfig(t, m, "a", 2, 1)

	_, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfg})
	require.NoError(t, err)

	got, err := m.DescribeEndpoint(ctx, "ep")
	require.NoError(t, err)
	assert.Equal(t, driver.EndpointCreating, got.Status)
	require.NotNil(t, got.Pending)
	assert.Equal(t, cfg, got.Pending.ConfigName)
	assert.Equal(t, 0, got.VariantSummaries[0].CurrentInstanceCount, "nothing serves yet")
	assert.Equal(t, 2, got.VariantSummaries[0].DesiredInstanceCount)
	require.Len(t, got.VariantSummaries[0].VariantStatus, 1)
	assert.Equal(t, driver.VariantCreating, got.VariantSummaries[0].VariantStatus[0].Status)

	_, err = m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep"})
	require.Error(t, err, "a Creating endpoint cannot be invoked")
	assert.True(t, errors.IsFailedPrecondition(err))

	list, err := m.ListEndpoints(ctx, driver.ListOptions{StatusEquals: driver.EndpointCreating})
	require.NoError(t, err)
	assert.Len(t, list.Items, 1, "List reports the same observed status as Describe")

	modified := got.LastModifiedTime

	fc.Advance(3 * time.Second)

	got, err = m.DescribeEndpoint(ctx, "ep")
	require.NoError(t, err)
	assert.Equal(t, driver.EndpointInService, got.Status)
	assert.Nil(t, got.Pending)
	assert.Equal(t, 2, got.VariantSummaries[0].CurrentInstanceCount)
	assert.Empty(t, got.VariantSummaries[0].VariantStatus)
	assert.Equal(t, modified, got.LastModifiedTime, "LastModifiedTime is stable across reads")

	_, err = m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep"})
	require.NoError(t, err)
}

func TestEndpoint_AsyncSettle_UpdatingAndDeleting(t *testing.T) { // T37, T39
	m, fc := newAsyncMock()
	ctx := context.Background()
	cfgA := seedConfig(t, m, "a", 1, 1)
	cfgB := seedConfig(t, m, "b", 4, 1)

	_, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfgA})
	require.NoError(t, err)
	fc.Advance(3 * time.Second)

	_, err = m.UpdateEndpoint(ctx, driver.UpdateEndpointSpec{EndpointName: "ep", ConfigName: cfgB})
	require.NoError(t, err)

	got, err := m.DescribeEndpoint(ctx, "ep")
	require.NoError(t, err)
	assert.Equal(t, driver.EndpointUpdating, got.Status)
	assert.Equal(t, cfgA, got.ConfigName, "the previous configuration serves until the swap")
	require.NotNil(t, got.Pending)
	assert.Equal(t, cfgB, got.Pending.ConfigName)
	assert.Equal(t, 1, got.VariantSummaries[0].CurrentInstanceCount)
	assert.Equal(t, 4, got.VariantSummaries[0].DesiredInstanceCount)

	_, err = m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep"})
	require.NoError(t, err, "an Updating endpoint keeps serving")

	_, err = m.UpdateEndpoint(ctx, driver.UpdateEndpointSpec{EndpointName: "ep", ConfigName: cfgA})
	require.Error(t, err, "no second update while Updating")
	assert.True(t, errors.IsFailedPrecondition(err))

	fc.Advance(2 * time.Second)

	got, err = m.DescribeEndpoint(ctx, "ep")
	require.NoError(t, err)
	assert.Equal(t, driver.EndpointInService, got.Status)
	assert.Equal(t, cfgB, got.ConfigName)
	assert.Equal(t, 4, got.VariantSummaries[0].CurrentInstanceCount)

	require.NoError(t, m.DeleteEndpoint(ctx, "ep"))

	got, err = m.DescribeEndpoint(ctx, "ep")
	require.NoError(t, err)
	assert.Equal(t, driver.EndpointDeleting, got.Status)

	require.NoError(t, m.DeleteEndpoint(ctx, "ep"), "deleting a Deleting endpoint is a no-op")

	fc.Advance(2 * time.Second)

	_, err = m.DescribeEndpoint(ctx, "ep")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	list, err := m.ListEndpoints(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, list.Items)
}

func TestUpdateEndpoint_Errors(t *testing.T) { // T34
	m := newTestMock()
	ctx := context.Background()
	cfg := seedConfig(t, m, "a", 1, 1)

	_, err := m.UpdateEndpoint(ctx, driver.UpdateEndpointSpec{EndpointName: "missing", ConfigName: cfg})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfg})
	require.NoError(t, err)

	_, err = m.UpdateEndpoint(ctx, driver.UpdateEndpointSpec{EndpointName: "ep", ConfigName: "no-such-config"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	cases := []struct {
		name    string
		weights []driver.VariantWeight
	}{
		{"no items", nil},
		{"unknown variant", []driver.VariantWeight{{VariantName: "ghost", DesiredWeight: ptr(1.0)}}},
		{"negative weight", []driver.VariantWeight{{VariantName: "v1", DesiredWeight: ptr(-0.1)}}},
		{"negative instances", []driver.VariantWeight{{VariantName: "v1", DesiredInstanceCount: ptr(-1)}}},
	}

	for _, tc := range cases {
		_, err = m.UpdateEndpointWeightsAndCapacities(ctx, "ep", tc.weights)
		require.Error(t, err, tc.name)
		assert.True(t, errors.IsInvalidArgument(err), tc.name)
	}

	_, err = m.UpdateEndpointWeightsAndCapacities(ctx, "missing", []driver.VariantWeight{{VariantName: "v1", DesiredWeight: ptr(1.0)}})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	got, err := m.DescribeEndpoint(ctx, "ep")
	require.NoError(t, err)
	assert.InDelta(t, 1.0, got.VariantSummaries[0].DesiredWeight, 0.001, "a rejected update changes nothing")
}

func TestUpdateEndpoint_RetainVariantProperties(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	cfgA := seedConfig(t, m, "a", 2, 1)
	cfgB := seedConfig(t, m, "b", 5, 1)

	_, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfgA})
	require.NoError(t, err)
	_, err = m.UpdateEndpointWeightsAndCapacities(ctx, "ep", []driver.VariantWeight{
		{VariantName: "v1", DesiredWeight: ptr(0.3), DesiredInstanceCount: ptr(7)},
	})
	require.NoError(t, err)

	got, err := m.UpdateEndpoint(ctx, driver.UpdateEndpointSpec{EndpointName: "ep", ConfigName: cfgB, RetainAllVariantProperties: true})
	require.NoError(t, err)
	assert.Equal(t, 7, got.VariantSummaries[0].DesiredInstanceCount, "retained")
	assert.InDelta(t, 0.3, got.VariantSummaries[0].DesiredWeight, 0.001, "retained")

	got, err = m.UpdateEndpoint(ctx, driver.UpdateEndpointSpec{
		EndpointName: "ep", ConfigName: cfgA, RetainAllVariantProperties: true, ExcludeRetained: []string{"DesiredInstanceCount"},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, got.VariantSummaries[0].DesiredInstanceCount, "excluded from retention: the new config's count")
	assert.InDelta(t, 0.3, got.VariantSummaries[0].DesiredWeight, 0.001)

	got, err = m.UpdateEndpoint(ctx, driver.UpdateEndpointSpec{
		EndpointName: "ep", ConfigName: cfgB, DeploymentConfig: []byte(`{"RollingUpdatePolicy":{}}`),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"RollingUpdatePolicy":{}}`, string(got.LastDeploymentConfig))

	got, err = m.UpdateEndpoint(ctx, driver.UpdateEndpointSpec{EndpointName: "ep", ConfigName: cfgA, RetainDeploymentConfig: true})
	require.NoError(t, err)
	assert.JSONEq(t, `{"RollingUpdatePolicy":{}}`, string(got.LastDeploymentConfig), "RetainDeploymentConfig keeps the last one")

	got, err = m.UpdateEndpoint(ctx, driver.UpdateEndpointSpec{EndpointName: "ep", ConfigName: cfgB})
	require.NoError(t, err)
	assert.Empty(t, got.LastDeploymentConfig)
}

func TestEndpoint_FailedStatusGates(t *testing.T) { // T38
	m := newTestMock()
	ctx := context.Background()
	cfg := seedConfig(t, m, "a", 1, 1)

	_, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfg})
	require.NoError(t, err)

	// Failed, RollingBack and UpdateRollbackFailed are not time-derived; set the
	// stored status directly the way fault injection would.
	for _, st := range []string{driver.EndpointFailed, driver.EndpointRollingBack, driver.EndpointUpdateRollbackFailed, driver.EndpointSystemUpdating} {
		m.endpoints.Update("ep", func(e *driver.Endpoint) *driver.Endpoint {
			c := *e
			c.Status, c.FailureReason = st, "boom"

			return &c
		})

		_, err = m.UpdateEndpoint(ctx, driver.UpdateEndpointSpec{EndpointName: "ep", ConfigName: cfg})
		require.Error(t, err, st)
		assert.True(t, errors.IsFailedPrecondition(err), st)

		_, err = m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep"})
		if st == driver.EndpointRollingBack {
			require.Error(t, err, st)
		}

		got, derr := m.DescribeEndpoint(ctx, "ep")
		require.NoError(t, derr)
		assert.Equal(t, st, got.Status)
		assert.Equal(t, "boom", got.FailureReason)
	}

	require.NoError(t, m.DeleteEndpoint(ctx, "ep"), "DeleteEndpoint works on a failed endpoint")
}

func TestEndpoint_CloneIsolation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	cfg := seedConfig(t, m, "a", 1, 1)

	_, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfg})
	require.NoError(t, err)

	got, _ := m.DescribeEndpoint(ctx, "ep")
	got.VariantSummaries[0].DesiredWeight = 99
	got.VariantSummaries[0].DeployedImages[0].SpecifiedImage = "mutated"

	again, _ := m.DescribeEndpoint(ctx, "ep")
	assert.InDelta(t, 1.0, again.VariantSummaries[0].DesiredWeight, 0.001)
	assert.Equal(t, "123.dkr.ecr/model-a:1", again.VariantSummaries[0].DeployedImages[0].SpecifiedImage)
}

func TestEndpoint_ConcurrentUpdates(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	cfg := seedConfig(t, m, "a", 1, 1)

	_, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfg})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := 1; i <= 20; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			_, _ = m.UpdateEndpointWeightsAndCapacities(ctx, "ep", []driver.VariantWeight{{VariantName: "v1", DesiredInstanceCount: ptr(n)}})
			_, _ = m.DescribeEndpoint(ctx, "ep")
			_, _ = m.ListEndpoints(ctx, driver.ListOptions{})
		}(i)
	}

	wg.Wait()

	got, err := m.DescribeEndpoint(ctx, "ep")
	require.NoError(t, err)
	assert.Equal(t, got.VariantSummaries[0].DesiredInstanceCount, got.VariantSummaries[0].CurrentInstanceCount)
}

func TestInvoke_TargetVariantAndContentType(t *testing.T) { // T44, T46
	m := newTestMock()
	ctx := context.Background()
	seedConfig(t, m, "a", 1, 1)

	_, err := m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{
		ConfigName: "two",
		ProductionVariants: []driver.ProductionVariant{
			{VariantName: "blue", ModelName: "model-a", InstanceType: "ml.m5.large", InitialInstanceCount: 1, InitialVariantWeight: ptr(0.0)},
			{VariantName: "green", ModelName: "model-a", InstanceType: "ml.m5.large", InitialInstanceCount: 1, InitialVariantWeight: ptr(1.0)},
		},
	})
	require.NoError(t, err)

	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: "two"})
	require.NoError(t, err)

	out, err := m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep", Body: []byte("x")})
	require.NoError(t, err)
	assert.Equal(t, "green", out.InvokedVariant, "the first variant that carries traffic")

	out, err = m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep", TargetVariant: "blue"})
	require.NoError(t, err)
	assert.Equal(t, "blue", out.InvokedVariant)

	_, err = m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep", TargetVariant: "red"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	for _, tc := range []struct{ accept, contentType, want string }{
		{"*/*", "application/json", "application/json"},
		{"", "text/csv", "text/csv"},
		{"", "", "application/octet-stream"},
		{"application/json", "text/csv", "application/json"},
		{"*/*, application/xml;q=0.5", "text/csv", "application/xml"},
		{"text/*", "", "application/octet-stream"},
	} {
		out, err = m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep", Accept: tc.accept, ContentType: tc.contentType})
		require.NoError(t, err)
		assert.Equal(t, tc.want, out.ContentType, "accept=%q contentType=%q", tc.accept, tc.contentType)
	}
}

func seedNotebook(t *testing.T, m *Mock, name string) {
	t.Helper()

	_, err := m.CreateNotebookInstance(context.Background(), driver.NotebookInstanceSpec{
		Name: name, InstanceType: "ml.t3.medium", RoleARN: "arn:aws:iam::123456789012:role/nb", VolumeSizeInGB: 10,
	})
	require.NoError(t, err)
}

func TestUpdateNotebookInstance_StoppedOnly(t *testing.T) { // T40
	m, fc := newTestMockClock()
	ctx := context.Background()
	seedNotebook(t, m, "nb")

	_, err := m.CreateNotebookInstanceLifecycleConfig(ctx, driver.NotebookLifecycleConfigSpec{Name: "lc", OnStart: "echo"})
	require.NoError(t, err)

	err = m.UpdateNotebookInstance(ctx, driver.NotebookInstanceUpdate{Name: "nb", InstanceType: ptr("ml.m5.large")})
	require.Error(t, err, "an InService notebook cannot be updated")
	assert.True(t, errors.IsFailedPrecondition(err))
	assert.Contains(t, err.Error(), "Status (InService) not in ([Stopped, Failed])")

	require.NoError(t, m.StopNotebookInstance(ctx, "nb"))

	before, err := m.DescribeNotebookInstance(ctx, "nb")
	require.NoError(t, err)

	fc.Advance(time.Minute)
	require.NoError(t, m.UpdateNotebookInstance(ctx, driver.NotebookInstanceUpdate{
		Name: "nb", InstanceType: ptr("ml.m5.large"), VolumeSizeInGB: ptr(50), LifecycleConfig: ptr("lc"),
		DefaultCodeRepo: ptr("https://github.com/o/r"), AdditionalCodeRepos: []string{"a", "b"}, RootAccess: ptr(driver.NotebookRootAccessOff),
		RoleARN: ptr("arn:aws:iam::123456789012:role/new"),
	}))

	got, err := m.DescribeNotebookInstance(ctx, "nb")
	require.NoError(t, err)
	assert.Equal(t, driver.NotebookStopped, got.Status, "sync mode: back to Stopped immediately")
	assert.Equal(t, "ml.m5.large", got.InstanceType)
	assert.Equal(t, 50, got.VolumeSizeInGB)
	assert.Equal(t, "lc", got.LifecycleConfig)
	assert.Equal(t, "https://github.com/o/r", got.DefaultCodeRepo)
	assert.Equal(t, []string{"a", "b"}, got.AdditionalCodeRepos)
	assert.Equal(t, driver.NotebookRootAccessOff, got.RootAccess)
	assert.Equal(t, "arn:aws:iam::123456789012:role/new", got.RoleARN)
	assert.Greater(t, parseTime(got.LastModifiedTime).UnixNano(), parseTime(before.LastModifiedTime).UnixNano(), "LastModifiedTime advances")
	assert.Equal(t, before.CreationTime, got.CreationTime)

	// disassociating is idempotent and clears the settings
	for i := 0; i < 2; i++ {
		require.NoError(t, m.UpdateNotebookInstance(ctx, driver.NotebookInstanceUpdate{
			Name: "nb", DisassociateLifecycleConfig: true, DisassociateDefaultCodeRepo: true, DisassociateAdditionalCodeRepos: true,
		}))
	}

	got, _ = m.DescribeNotebookInstance(ctx, "nb")
	assert.Empty(t, got.LifecycleConfig)
	assert.Empty(t, got.DefaultCodeRepo)
	assert.Empty(t, got.AdditionalCodeRepos)

	require.NoError(t, m.StartNotebookInstance(ctx, "nb"))
}

func TestUpdateNotebookInstance_AsyncSettle(t *testing.T) { // T40
	m, fc := newAsyncMock()
	ctx := context.Background()
	seedNotebook(t, m, "nb")
	require.NoError(t, m.StopNotebookInstance(ctx, "nb"))
	require.NoError(t, m.UpdateNotebookInstance(ctx, driver.NotebookInstanceUpdate{Name: "nb", VolumeSizeInGB: ptr(20)}))

	got, err := m.DescribeNotebookInstance(ctx, "nb")
	require.NoError(t, err)
	assert.Equal(t, driver.NotebookUpdating, got.Status)

	list, err := m.ListNotebookInstances(ctx, driver.ListOptions{StatusEquals: driver.NotebookUpdating})
	require.NoError(t, err)
	assert.Len(t, list.Items, 1, "List reports the observed status")

	err = m.StartNotebookInstance(ctx, "nb")
	require.Error(t, err, "no start while Updating")
	assert.True(t, errors.IsFailedPrecondition(err))

	err = m.UpdateNotebookInstance(ctx, driver.NotebookInstanceUpdate{Name: "nb", VolumeSizeInGB: ptr(30)})
	require.Error(t, err, "no second update while Updating")

	fc.Advance(3 * time.Second)

	got, _ = m.DescribeNotebookInstance(ctx, "nb")
	assert.Equal(t, driver.NotebookStopped, got.Status)
	require.NoError(t, m.StartNotebookInstance(ctx, "nb"))
}

func TestUpdateNotebookInstance_Validation(t *testing.T) { // T41
	m := newTestMock()
	ctx := context.Background()
	seedNotebook(t, m, "nb")
	require.NoError(t, m.StopNotebookInstance(ctx, "nb"))

	err := m.UpdateNotebookInstance(ctx, driver.NotebookInstanceUpdate{Name: "missing", VolumeSizeInGB: ptr(20)})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	bad := map[string]driver.NotebookInstanceUpdate{
		"shrink":     {Name: "nb", VolumeSizeInGB: ptr(5)},
		"below min":  {Name: "nb", VolumeSizeInGB: ptr(4)},
		"above max":  {Name: "nb", VolumeSizeInGB: ptr(16385)},
		"unknown lc": {Name: "nb", LifecycleConfig: ptr("ghost")},
		"four repos": {Name: "nb", AdditionalCodeRepos: []string{"a", "b", "c", "d"}},
		"bad role":   {Name: "nb", RoleARN: ptr("not-an-arn")},
		"bad root":   {Name: "nb", RootAccess: ptr("Maybe")},
		"empty type": {Name: "nb", InstanceType: ptr("")},
	}

	for name, up := range bad {
		err = m.UpdateNotebookInstance(ctx, up)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	got, _ := m.DescribeNotebookInstance(ctx, "nb")
	assert.Equal(t, 10, got.VolumeSizeInGB, "a rejected update changes nothing")
}

func TestUpdateNotebookInstanceLifecycleConfig(t *testing.T) { // T40, T43
	m, fc := newTestMockClock()
	ctx := context.Background()
	seedNotebook(t, m, "nb")

	_, err := m.CreateNotebookInstanceLifecycleConfig(ctx, driver.NotebookLifecycleConfigSpec{Name: "lc", OnCreate: "c1", OnStart: "s1"})
	require.NoError(t, err)

	fc.Advance(time.Minute)
	require.NoError(t, m.UpdateNotebookInstanceLifecycleConfig(ctx, driver.NotebookLifecycleConfigUpdate{Name: "lc", OnStart: ptr("s2")}),
		"no status gate: the notebook is InService")

	lc, err := m.DescribeNotebookInstanceLifecycleConfig(ctx, "lc")
	require.NoError(t, err)
	assert.Equal(t, "c1", lc.OnCreate, "an omitted script is unchanged")
	assert.Equal(t, "s2", lc.OnStart)
	assert.Greater(t, parseTime(lc.LastModifiedTime).UnixNano(), parseTime(lc.CreationTime).UnixNano())

	err = m.UpdateNotebookInstanceLifecycleConfig(ctx, driver.NotebookLifecycleConfigUpdate{Name: "ghost", OnStart: ptr("x")})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

func TestNotebook_ConcurrentUpdateAndStop(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	seedNotebook(t, m, "nb")
	require.NoError(t, m.StopNotebookInstance(ctx, "nb"))

	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			_ = m.UpdateNotebookInstance(ctx, driver.NotebookInstanceUpdate{Name: "nb", VolumeSizeInGB: ptr(10 + i)})
			_, _ = m.DescribeNotebookInstance(ctx, "nb")
			_ = m.StartNotebookInstance(ctx, "nb")
			_ = m.StopNotebookInstance(ctx, "nb")
		}(i)
	}

	wg.Wait()
}

func groupSpec(name string, count int) driver.ClusterInstanceGroupSpec {
	return driver.ClusterInstanceGroupSpec{GroupName: name, InstanceType: "ml.g5.xlarge", InstanceCount: count, ExecutionRole: "arn:aws:iam::123456789012:role/hp"}
}

func TestCluster_InstanceGroupFieldsAndLimits(t *testing.T) { // T47, T50
	m := newTestMock()
	ctx := context.Background()

	g := groupSpec("workers", 3)
	g.MinInstanceCount, g.ThreadsPerCore = 1, 2
	g.LifeCycleConfig = &driver.LifeCycleConfig{SourceS3URI: "s3://b/lc", OnCreate: "on_create.sh"}

	c, err := m.CreateCluster(ctx, driver.ClusterSpec{ClusterName: "hp", InstanceGroups: []driver.ClusterInstanceGroupSpec{g, groupSpec("head", 1)}})
	require.NoError(t, err)
	require.Len(t, c.InstanceGroups, 2)

	w := c.InstanceGroups[0]
	assert.Equal(t, 3, w.CurrentCount)
	assert.Equal(t, 3, w.TargetCount)
	assert.Equal(t, 1, w.MinCount)
	assert.Equal(t, 2, w.ThreadsPerCore)
	assert.Equal(t, driver.GroupInService, w.Status)
	require.NotNil(t, w.LifeCycleConfig)
	assert.Equal(t, "s3://b/lc", w.LifeCycleConfig.SourceS3URI)
	assert.Equal(t, driver.DefaultThreadsPerCore, c.InstanceGroups[1].ThreadsPerCore, "ThreadsPerCore defaults to 1")

	nodes := listNodes(t, m, "hp")
	assert.Len(t, nodes, 4, "one node per running instance")

	bad := map[string][]driver.ClusterInstanceGroupSpec{
		"too many per group":   {groupSpec("a", 6759)},
		"negative":             {groupSpec("a", -1)},
		"threads per core 3":   {{GroupName: "a", InstanceType: "t", InstanceCount: 1, ThreadsPerCore: 3}},
		"min above count":      {{GroupName: "a", InstanceType: "t", InstanceCount: 1, MinInstanceCount: 2}},
		"duplicate names":      {groupSpec("a", 1), groupSpec("a", 1)},
		"over total instances": {groupSpec("a", 6758), groupSpec("b", 6758)},
	}

	over := make([]driver.ClusterInstanceGroupSpec, 0, 101)
	for i := 0; i < 101; i++ {
		over = append(over, groupSpec("g"+string(rune('a'+i%26))+string(rune('a'+i/26)), 1))
	}

	bad["101 groups"] = over

	for name, groups := range bad {
		_, err = m.CreateCluster(ctx, driver.ClusterSpec{ClusterName: "bad-" + name, InstanceGroups: groups})
		require.Error(t, err, name)
	}

	_, err = m.CreateCluster(ctx, driver.ClusterSpec{ClusterName: "over", InstanceGroups: bad["over total instances"]})
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err), "the cluster-wide cap is a limit error")
}

func TestUpdateCluster_MergeByName(t *testing.T) { // T48
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.ClusterSpec{ClusterName: "hp", InstanceGroups: []driver.ClusterInstanceGroupSpec{groupSpec("a", 1), groupSpec("b", 2)}})
	require.NoError(t, err)

	c, err := m.UpdateCluster(ctx, driver.ClusterUpdate{ClusterName: "hp", InstanceGroups: []driver.ClusterInstanceGroupSpec{groupSpec("a", 5), groupSpec("c", 3)}})
	require.NoError(t, err)

	counts := map[string]int{}
	for _, g := range c.InstanceGroups {
		counts[g.GroupName] = g.TargetCount
	}

	assert.Equal(t, map[string]int{"a": 5, "b": 2, "c": 3}, counts, "untouched groups are kept, new added, named updated")

	c, err = m.UpdateCluster(ctx, driver.ClusterUpdate{ClusterName: "hp", InstanceGroupsToDelete: []string{"b"}})
	require.NoError(t, err)
	assert.Len(t, c.InstanceGroups, 2)

	for name, up := range map[string]driver.ClusterUpdate{
		"delete unknown":    {ClusterName: "hp", InstanceGroupsToDelete: []string{"ghost"}},
		"update and delete": {ClusterName: "hp", InstanceGroups: []driver.ClusterInstanceGroupSpec{groupSpec("a", 1)}, InstanceGroupsToDelete: []string{"a"}},
		"nothing":           {ClusterName: "hp"},
	} {
		_, err = m.UpdateCluster(ctx, up)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	_, err = m.UpdateCluster(ctx, driver.ClusterUpdate{ClusterName: "missing", InstanceGroups: []driver.ClusterInstanceGroupSpec{groupSpec("a", 1)}})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

func TestCluster_CloneIsolationAndAsyncRamp(t *testing.T) { // T49, T52
	m, fc := newAsyncMock()
	ctx := context.Background()

	g := groupSpec("a", 4)
	g.LifeCycleConfig = &driver.LifeCycleConfig{SourceS3URI: "s3://b/lc", OnCreate: "x.sh"}

	_, err := m.CreateCluster(ctx, driver.ClusterSpec{ClusterName: "hp", InstanceGroups: []driver.ClusterInstanceGroupSpec{g}})
	require.NoError(t, err)

	got, err := m.DescribeCluster(ctx, "hp")
	require.NoError(t, err)
	assert.Equal(t, driver.ClusterCreating, got.Status)
	assert.Equal(t, 0, got.InstanceGroups[0].CurrentCount, "nothing is running yet")
	assert.Equal(t, 4, got.InstanceGroups[0].TargetCount)
	assert.Equal(t, driver.GroupCreating, got.InstanceGroups[0].Status)

	nodes := listNodes(t, m, "hp")
	assert.Empty(t, nodes)

	_, err = m.UpdateCluster(ctx, driver.ClusterUpdate{ClusterName: "hp", InstanceGroupsToDelete: []string{"a"}})
	require.Error(t, err)
	assert.True(t, isConflict(err), "no update while Creating")

	fc.Advance(5 * time.Second)

	got, err = m.DescribeCluster(ctx, "hp")
	require.NoError(t, err)
	assert.Equal(t, driver.ClusterInService, got.Status)
	assert.Equal(t, 4, got.InstanceGroups[0].CurrentCount)

	got.InstanceGroups[0].LifeCycleConfig.SourceS3URI = "mutated"

	again, _ := m.DescribeCluster(ctx, "hp")
	assert.Equal(t, "s3://b/lc", again.InstanceGroups[0].LifeCycleConfig.SourceS3URI, "a returned lifecycle config does not alias stored state")

	_, err = m.UpdateCluster(ctx, driver.ClusterUpdate{ClusterName: "hp", InstanceGroups: []driver.ClusterInstanceGroupSpec{groupSpec("a", 6)}})
	require.NoError(t, err)

	got, _ = m.DescribeCluster(ctx, "hp")
	assert.Equal(t, driver.ClusterUpdating, got.Status)
	assert.Equal(t, 4, got.InstanceGroups[0].CurrentCount, "still the previous count while Updating")
	assert.Equal(t, 6, got.InstanceGroups[0].TargetCount)

	fc.Advance(5 * time.Second)

	got, _ = m.DescribeCluster(ctx, "hp")
	assert.Equal(t, 6, got.InstanceGroups[0].CurrentCount)
}

func isConflict(err error) bool {
	ex, ok := driver.ExceptionOf(err)

	return ok && ex == driver.ExConflict
}

func TestTrainingJob_TransitionsDistinct(t *testing.T) { // T64
	m, fc := newTestMockClock()
	ctx := context.Background()

	fc.Advance(time.Minute)

	job, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "t1", OutputS3URI: "s3://b"})
	require.NoError(t, err)

	assert.Equal(t, driver.JobCompleted, job.Status, "sync default: terminal immediately")
	assert.Equal(t, driver.SecondaryCompleted, job.SecondaryStatus)
	require.Len(t, job.SecondaryTransitions, 5)

	var prevStart time.Time

	for i, tr := range job.SecondaryTransitions {
		start := parseTime(tr.StartTime)
		assert.True(t, start.After(prevStart), "transition %d (%s) starts after the previous one", i, tr.Status)
		assert.NotEmpty(t, tr.StatusMessage, tr.Status)

		if i < len(job.SecondaryTransitions)-1 {
			assert.Equal(t, job.SecondaryTransitions[i+1].StartTime, tr.EndTime, "a stage ends when the next begins")
		} else {
			assert.Equal(t, tr.StartTime, tr.EndTime, "the final Completed entry is a point in time")
		}

		prevStart = start
	}

	statuses := make([]string, 0, 5)
	for _, tr := range job.SecondaryTransitions {
		statuses = append(statuses, tr.Status)
	}

	assert.Equal(t, []string{"Starting", "Downloading", "Training", "Uploading", "Completed"}, statuses)

	created, start, end := parseTime(job.CreationTime), parseTime(job.TrainingStartTime), parseTime(job.TrainingEndTime)
	assert.True(t, !start.Before(created) && end.After(start), "CreationTime <= TrainingStartTime < TrainingEndTime")
	assert.GreaterOrEqual(t, job.TrainingTimeSeconds, 1)
	assert.Equal(t, job.TrainingTimeSeconds, job.BillableTimeSeconds)
	assert.Equal(t, job.TrainingEndTime, job.LastModifiedTime)
}

func TestTrainingJob_AsyncSettle_Stages(t *testing.T) { // T65
	m, fc := newAsyncMock()
	ctx := context.Background()

	_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "t1", OutputS3URI: "s3://b"})
	require.NoError(t, err)

	type obs struct {
		status, secondary string
		transitions       int
		started, ended    bool
	}

	look := func() obs {
		j, derr := m.DescribeTrainingJob(ctx, "t1")
		require.NoError(t, derr)

		return obs{j.Status, j.SecondaryStatus, len(j.SecondaryTransitions), j.TrainingStartTime != "", j.TrainingEndTime != ""}
	}

	assert.Equal(t, obs{driver.JobInProgress, "Starting", 1, false, false}, look())

	fc.Advance(1500 * time.Millisecond)
	assert.Equal(t, obs{driver.JobInProgress, "Downloading", 2, false, false}, look())

	fc.Advance(time.Second)
	assert.Equal(t, obs{driver.JobInProgress, "Training", 3, true, false}, look())

	fc.Advance(time.Second)
	assert.Equal(t, obs{driver.JobInProgress, "Uploading", 4, true, false}, look())

	fc.Advance(time.Second)
	assert.Equal(t, obs{driver.JobCompleted, "Completed", 5, true, true}, look())

	list, err := m.ListTrainingJobs(ctx, driver.ListOptions{StatusEquals: driver.JobCompleted})
	require.NoError(t, err)
	assert.Len(t, list.Items, 1, "List filters on the observed status")
}

func TestStopTrainingJob_Lifecycle(t *testing.T) { // T66
	m, fc := newAsyncMock()
	ctx := context.Background()

	_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "t1", OutputS3URI: "s3://b"})
	require.NoError(t, err)

	fc.Advance(2500 * time.Millisecond) // in the Training stage

	require.NoError(t, m.StopTrainingJob(ctx, "t1"))

	j, err := m.DescribeTrainingJob(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, driver.JobStopping, j.Status)
	assert.Equal(t, driver.SecondaryStopping, j.SecondaryStatus)
	assert.Empty(t, j.TrainingEndTime)

	fc.Advance(2 * time.Second)

	j, err = m.DescribeTrainingJob(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, driver.JobStopped, j.Status)
	assert.Equal(t, driver.SecondaryStopped, j.SecondaryStatus)
	assert.NotEmpty(t, j.TrainingEndTime)

	statuses := make([]string, 0, 6)
	for _, tr := range j.SecondaryTransitions {
		statuses = append(statuses, tr.Status)
	}

	assert.Equal(t, []string{"Starting", "Downloading", "Training", "Stopping", "Stopped"}, statuses, "the history shows the stop")
	assert.NotEmpty(t, j.SecondaryTransitions[2].EndTime, "the interrupted stage is closed when the stop is accepted")
}

func TestStopJobs_TerminalIsNoopAndMissingIsNotFound(t *testing.T) { // T67
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "t1"})
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		require.NoError(t, m.StopTrainingJob(ctx, "t1"), "stopping a Completed job succeeds")
	}

	j, _ := m.DescribeTrainingJob(ctx, "t1")
	assert.Equal(t, driver.JobCompleted, j.Status, "and never turns it into Stopped")
	assert.Empty(t, j.StopRequested)

	err = m.StopTrainingJob(ctx, "ghost")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	stops := map[string]func(context.Context, string) error{
		"processing": m.StopProcessingJob, "transform": m.StopTransformJob, "tuning": m.StopHyperParameterTuningJob,
		"automl": m.StopAutoMLJob, "labeling": m.StopLabelingJob, "compilation": m.StopCompilationJob,
	}

	for name, stop := range stops {
		err = stop(ctx, "ghost")
		require.Error(t, err, name)
		assert.True(t, errors.IsNotFound(err), name)
	}
}

func TestJobFamilies_AsyncStopAndStatuses(t *testing.T) { // T66, T67 for every family
	m, fc := newAsyncMock()
	ctx := context.Background()

	_, err := m.CreateProcessingJob(ctx, driver.ProcessingJobConfig{JobName: "p"})
	require.NoError(t, err)
	_, err = m.CreateTransformJob(ctx, driver.TransformJobConfig{JobName: "x", ModelName: "m"})
	require.NoError(t, err)
	_, err = m.CreateHyperParameterTuningJob(ctx, driver.HyperParameterTuningJobConfig{JobName: "h", MaxJobs: 4, MaxParallelJobs: 2})
	require.NoError(t, err)
	_, err = m.CreateAutoMLJobV2(ctx, driver.AutoMLJobConfig{JobName: "a"})
	require.NoError(t, err)
	_, err = m.CreateLabelingJob(ctx, driver.LabelingJobConfig{JobName: "l"})
	require.NoError(t, err)
	_, err = m.CreateCompilationJob(ctx, driver.CompilationJobConfig{JobName: "c"})
	require.NoError(t, err)

	status := func() map[string]string {
		p, _ := m.DescribeProcessingJob(ctx, "p")
		x, _ := m.DescribeTransformJob(ctx, "x")
		h, _ := m.DescribeHyperParameterTuningJob(ctx, "h")
		a, _ := m.DescribeAutoMLJobV2(ctx, "a")
		l, _ := m.DescribeLabelingJob(ctx, "l")
		c, _ := m.DescribeCompilationJob(ctx, "c")

		return map[string]string{"p": p.Status, "x": x.Status, "h": h.Status, "a": a.Status, "l": l.Status, "c": c.Status}
	}

	assert.Equal(t, map[string]string{"p": "InProgress", "x": "InProgress", "h": "InProgress", "a": "InProgress", "l": "Initializing", "c": "STARTING"}, status())

	require.NoError(t, m.StopProcessingJob(ctx, "p"))
	require.NoError(t, m.StopTransformJob(ctx, "x"))
	require.NoError(t, m.StopHyperParameterTuningJob(ctx, "h"))
	require.NoError(t, m.StopAutoMLJob(ctx, "a"))
	require.NoError(t, m.StopLabelingJob(ctx, "l"))
	require.NoError(t, m.StopCompilationJob(ctx, "c"))

	assert.Equal(t, map[string]string{"p": "Stopping", "x": "Stopping", "h": "Stopping", "a": "Stopping", "l": "Stopping", "c": "STOPPING"}, status())

	fc.Advance(3 * time.Second)

	assert.Equal(t, map[string]string{"p": "Stopped", "x": "Stopped", "h": "Stopped", "a": "Stopped", "l": "Stopped", "c": "STOPPED"}, status())

	h, _ := m.DescribeHyperParameterTuningJob(ctx, "h")
	assert.Equal(t, map[string]int{"Stopped": 4}, h.TrainingJobCounts)
	assert.Empty(t, h.BestTrainingJob)

	// completed jobs report best candidate / counters
	_, err = m.CreateAutoMLJobV2(ctx, driver.AutoMLJobConfig{JobName: "a2"})
	require.NoError(t, err)
	_, err = m.CreateHyperParameterTuningJob(ctx, driver.HyperParameterTuningJobConfig{JobName: "h2", MaxJobs: 4})
	require.NoError(t, err)
	fc.Advance(10 * time.Second)

	a2, _ := m.DescribeAutoMLJobV2(ctx, "a2")
	assert.Equal(t, "a2-best", a2.BestCandidateName)
	assert.Equal(t, driver.SecondaryCompleted, a2.SecondaryStatus)

	h2, _ := m.DescribeHyperParameterTuningJob(ctx, "h2")
	assert.Equal(t, map[string]int{"Completed": 4}, h2.TrainingJobCounts)
	assert.Equal(t, "h2-001", h2.BestTrainingJob)
}

func TestStopAndDescribe_Concurrent(t *testing.T) { // T68
	m, fc := newAsyncMock()
	ctx := context.Background()

	_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "t1"})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := 0; i < 30; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = m.StopTrainingJob(ctx, "t1")
			_, _ = m.DescribeTrainingJob(ctx, "t1")
			_, _ = m.ListTrainingJobs(ctx, driver.ListOptions{})
		}()
	}

	wg.Wait()
	fc.Advance(5 * time.Second)

	j, err := m.DescribeTrainingJob(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, driver.JobStopped, j.Status)
	assert.NotEmpty(t, j.StopRequested)

	names := 0
	for _, tr := range j.SecondaryTransitions {
		if tr.Status == driver.SecondaryStopping {
			names++
		}
	}

	assert.Equal(t, 1, names, "exactly one stop was accepted")
}

func TestJobNameValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	for _, bad := range []string{"", "-lead", "has space", "under_score", string(make([]byte, 0)) + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: bad})
		require.Error(t, err, bad)
		assert.True(t, errors.IsInvalidArgument(err), bad)
	}

	_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "ok-1-job"})
	require.NoError(t, err)
}

func TestJobDescribe_DeepClone(t *testing.T) { // T62
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{
		JobName: "t1", HyperParameters: map[string]string{"eta": "0.1"}, InputChannels: []driver.Channel{{Name: "train", S3URI: "s3://b/t"}},
		Tags: []driver.Tag{{Key: "k", Value: "v"}}, Echo: map[string]json.RawMessage{"StoppingCondition": json.RawMessage(`{"MaxRuntimeInSeconds":60}`)},
	})
	require.NoError(t, err)

	got, _ := m.DescribeTrainingJob(ctx, "t1")
	got.HyperParameters["eta"] = "mutated"
	got.InputChannels[0].Name = "mutated"
	got.Tags[0].Value = "mutated"
	got.SecondaryTransitions[0].Status = "mutated"
	got.Echo["StoppingCondition"] = json.RawMessage(`{}`)

	again, _ := m.DescribeTrainingJob(ctx, "t1")
	assert.Equal(t, "0.1", again.HyperParameters["eta"])
	assert.Equal(t, "train", again.InputChannels[0].Name)
	assert.Equal(t, "v", again.Tags[0].Value)
	assert.Equal(t, "Starting", again.SecondaryTransitions[0].Status)
	assert.JSONEq(t, `{"MaxRuntimeInSeconds":60}`, string(again.Echo["StoppingCondition"]))
}

func seedPackages(t *testing.T, m *Mock, group string, n int) []string {
	t.Helper()

	ctx := context.Background()
	_, err := m.CreateModelPackageGroup(ctx, driver.ModelPackageGroupSpec{GroupName: group})
	require.NoError(t, err)

	arns := make([]string, 0, n)

	for i := 0; i < n; i++ {
		p, perr := m.CreateModelPackage(ctx, driver.ModelPackageSpec{GroupName: group, InferenceImage: "img:1", ModelDataURL: "s3://b/m.tgz", Description: "d"})
		require.NoError(t, perr)

		arns = append(arns, p.PackageARN)
	}

	return arns
}

func TestBatchDescribeModelPackage(t *testing.T) { // T69, T70
	m := newTestMock()
	ctx := context.Background()
	arns := seedPackages(t, m, "grp", 2)
	missing := "arn:aws:sagemaker:us-east-1:123456789012:model-package/grp/99"

	res, err := m.BatchDescribeModelPackage(ctx, []string{arns[0], missing, arns[1]})
	require.NoError(t, err)
	assert.Len(t, res.Packages, 2)
	assert.Equal(t, "img:1", res.Packages[arns[0]].InferenceImage)
	require.Contains(t, res.Errors, missing, "a missing package is reported per ARN, never failing the call")
	assert.NotEmpty(t, res.Errors[missing].Code)

	for name, in := range map[string][]string{
		"empty":     nil,
		"malformed": {"not-an-arn"},
		"over 100":  manyARNs(101),
	} {
		_, err = m.BatchDescribeModelPackage(ctx, in)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	_, err = m.BatchDescribeModelPackage(ctx, manyARNs(100))
	require.NoError(t, err, "exactly 100 is allowed")
}

func manyARNs(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, "arn:aws:sagemaker:us-east-1:123456789012:model-package/g/"+strconv.Itoa(i+1))
	}

	return out
}

func TestModelPackageGroupPolicy(t *testing.T) { // T71, T72
	m := newTestMock()
	ctx := context.Background()
	seedPackages(t, m, "grp", 0)

	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"sagemaker:DescribeModelPackage","Resource":"*"}]}`

	_, err := m.GetModelPackageGroupPolicy(ctx, "grp")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "no policy attached yet")

	arn, err := m.PutModelPackageGroupPolicy(ctx, "grp", policy)
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:model-package-group/grp", arn)

	got, err := m.GetModelPackageGroupPolicy(ctx, arn)
	require.NoError(t, err, "a group ARN is accepted wherever a name is")
	assert.Equal(t, policy, got, "the policy is stored verbatim")

	_, err = m.PutModelPackageGroupPolicy(ctx, "grp", `{"Version":"2012-10-17"}`)
	require.NoError(t, err)

	got, _ = m.GetModelPackageGroupPolicy(ctx, "grp")
	assert.JSONEq(t, `{"Version":"2012-10-17"}`, got, "a second Put replaces")

	for name, bad := range map[string]string{
		"empty": "", "not json": "allow all", "too long": `{"a":"` + strings.Repeat("x", driver.MaxGroupPolicyLength) + `"}`,
	} {
		_, err = m.PutModelPackageGroupPolicy(ctx, "grp", bad)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	require.NoError(t, m.DeleteModelPackageGroupPolicy(ctx, "grp"))
	require.NoError(t, m.DeleteModelPackageGroupPolicy(ctx, "grp"), "deleting a missing policy is idempotent")

	_, err = m.GetModelPackageGroupPolicy(ctx, "grp")
	require.Error(t, err)

	for _, call := range []func() error{
		func() error { _, e := m.PutModelPackageGroupPolicy(ctx, "ghost", policy); return e },
		func() error { _, e := m.GetModelPackageGroupPolicy(ctx, "ghost"); return e },
		func() error { return m.DeleteModelPackageGroupPolicy(ctx, "ghost") },
	} {
		err = call()
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))
	}
}

func TestDeleteModelPackageGroup_Cascade(t *testing.T) { // T73, T75
	m := newTestMock()
	ctx := context.Background()
	arns := seedPackages(t, m, "grp", 2)
	_, err := m.PutModelPackageGroupPolicy(ctx, "grp", `{"Version":"1"}`)
	require.NoError(t, err)

	_, err = m.AddTags(ctx, arns[0], []driver.Tag{{Key: "k", Value: "v"}})
	require.NoError(t, err)

	other := seedPackages(t, m, "other", 1)

	require.NoError(t, m.DeleteModelPackageGroup(ctx, "arn:aws:sagemaker:us-east-1:123456789012:model-package-group/grp"), "by ARN")

	for _, a := range arns {
		_, err = m.DescribeModelPackage(ctx, a)
		require.Error(t, err, "the group's packages are removed with it")
		assert.True(t, errors.IsNotFound(err))
	}

	assert.Zero(t, m.packagePolicies.Len())

	_, err = m.DescribeModelPackage(ctx, other[0])
	require.NoError(t, err, "another group's packages are untouched")

	_, err = m.CreateModelPackageGroup(ctx, driver.ModelPackageGroupSpec{GroupName: "grp"})
	require.NoError(t, err)

	p, err := m.CreateModelPackage(ctx, driver.ModelPackageSpec{GroupName: "grp"})
	require.NoError(t, err)
	assert.Equal(t, 1, p.Version, "a re-created group starts again at version 1")

	tags, err := m.ListTags(ctx, p.PackageARN)
	require.NoError(t, err)
	assert.Empty(t, tags)
}

func TestCreatePackage_VsGroupDelete_Race(t *testing.T) { // T74
	for i := 0; i < 50; i++ {
		m := newTestMock()
		ctx := context.Background()
		_, err := m.CreateModelPackageGroup(ctx, driver.ModelPackageGroupSpec{GroupName: "grp"})
		require.NoError(t, err)

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = m.CreateModelPackage(ctx, driver.ModelPackageSpec{GroupName: "grp"})
		}()
		go func() {
			defer wg.Done()

			_ = m.DeleteModelPackageGroup(ctx, "grp")
		}()

		wg.Wait()

		if !m.packageGroups.Has("grp") {
			assert.Zero(t, m.packages.Len(), "iteration %d: a package outlived its group", i)
		}
	}
}

func TestModelPackageList_ApprovalFilterAndGroupARN(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	arns := seedPackages(t, m, "grp", 3)

	_, err := m.UpdateModelPackage(ctx, arns[1], driver.ApprovalApproved)
	require.NoError(t, err)

	page, err := m.ListModelPackages(ctx, "arn:aws:sagemaker:us-east-1:123456789012:model-package-group/grp",
		driver.ListOptions{Filters: map[string]string{"ModelApprovalStatus": driver.ApprovalApproved}})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, arns[1], page.Items[0].PackageARN)
}

func TestSnapshot_PoliciesSurvive(t *testing.T) { // T75
	src := newTestMock()
	ctx := context.Background()
	seedPackages(t, src, "grp", 1)
	_, err := src.PutModelPackageGroupPolicy(ctx, "grp", `{"Version":"1"}`)
	require.NoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))

	got, err := dst.GetModelPackageGroupPolicy(ctx, "grp")
	require.NoError(t, err)
	assert.JSONEq(t, `{"Version":"1"}`, got)
}

func scSpec(name string) driver.ProjectSpec {
	return driver.ProjectSpec{
		Name: name, Description: "d",
		ServiceCatalog: &driver.ServiceCatalogProvisioning{
			ProductID: "prod-1", ProvisioningArtifactID: "pa-1",
			Parameters: []driver.ProvisioningParameter{{Key: "k", Value: "v"}},
		},
		Tags: []driver.Tag{{Key: "team", Value: "ml"}},
	}
}

func TestProjects_LifecycleAndValidation(t *testing.T) { // T101
	m, fc := newTestMockClock()
	ctx := context.Background()

	p, err := m.CreateProject(ctx, scSpec("proj-1"))
	require.NoError(t, err)
	assert.Equal(t, driver.ProjectCreateCompleted, p.Status)
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:project/proj-1", p.ARN)
	assert.True(t, strings.HasPrefix(p.ID, "p-"))
	assert.NotEmpty(t, p.ProvisionedProductID)

	tags, err := m.ListTags(ctx, p.ARN)
	require.NoError(t, err)
	assert.Len(t, tags, 1)

	fc.Advance(time.Minute)

	desc := "new description"
	up, err := m.UpdateProject(ctx, driver.ProjectUpdate{
		Name: "proj-1", Description: &desc, ProvisioningArtifactID: "pa-2", Tags: []driver.Tag{{Key: "env", Value: "prod"}},
	})
	require.NoError(t, err)
	assert.Equal(t, driver.ProjectUpdateCompleted, up.Status)
	assert.Equal(t, "new description", up.Description)
	assert.Equal(t, "pa-2", up.ServiceCatalog.ProvisioningArtifactID)
	assert.Equal(t, "prod", up.Tags[0].Value)
	assert.Greater(t, parseTime(up.LastModifiedTime).UnixNano(), parseTime(up.CreationTime).UnixNano())

	for name, spec := range map[string]driver.ProjectSpec{
		"empty name":    {Name: "", ServiceCatalog: scSpec("x").ServiceCatalog},
		"33 chars":      scSpec(strings.Repeat("a", 33)),
		"leading dash":  scSpec("-x"),
		"neither":       {Name: "n1"},
		"both":          {Name: "n2", ServiceCatalog: scSpec("x").ServiceCatalog, TemplateProviders: []driver.TemplateProvider{{TemplateName: "t", TemplateURL: "https://x"}}},
		"no product":    {Name: "n3", ServiceCatalog: &driver.ServiceCatalogProvisioning{}},
		"two templates": {Name: "n4", TemplateProviders: []driver.TemplateProvider{{TemplateName: "a"}, {TemplateName: "b"}}},
	} {
		_, err = m.CreateProject(ctx, spec)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	_, err = m.CreateProject(ctx, scSpec("proj-1"))
	require.Error(t, err)
	assert.True(t, errors.IsAlreadyExists(err))

	require.NoError(t, m.DeleteProject(ctx, "proj-1"))

	_, err = m.DescribeProject(ctx, "proj-1")
	assert.True(t, errors.IsNotFound(err))

	_, err = m.ListTags(ctx, p.ARN)
	assert.True(t, errors.IsNotFound(err), "tags go with the project")

	_, err = m.CreateProject(ctx, scSpec("proj-1"))
	require.NoError(t, err, "the name is reusable after delete")

	for _, op := range []func() error{
		func() error { _, e := m.UpdateProject(ctx, driver.ProjectUpdate{Name: "ghost"}); return e },
		func() error { return m.DeleteProject(ctx, "ghost") },
	} {
		assert.True(t, errors.IsNotFound(op()))
	}
}

func TestProjects_AsyncConflictAndList(t *testing.T) { // T101
	m, fc := newAsyncMock()
	ctx := context.Background()

	_, err := m.CreateProject(ctx, scSpec("proj-a"))
	require.NoError(t, err)

	got, _ := m.DescribeProject(ctx, "proj-a")
	assert.Equal(t, driver.ProjectCreateInProgres, got.Status)

	desc := "x"
	_, err = m.UpdateProject(ctx, driver.ProjectUpdate{Name: "proj-a", Description: &desc})
	require.Error(t, err)
	assert.True(t, isConflict(err), "no update while CreateInProgress")
	assert.True(t, isConflict(m.DeleteProject(ctx, "proj-a")), "no delete while CreateInProgress")

	list, err := m.ListProjects(ctx, driver.ListOptions{StatusEquals: driver.ProjectCreateInProgres})
	require.NoError(t, err)
	assert.Len(t, list.Items, 1)

	fc.Advance(5 * time.Second)

	got, _ = m.DescribeProject(ctx, "proj-a")
	assert.Equal(t, driver.ProjectCreateCompleted, got.Status)

	_, err = m.UpdateProject(ctx, driver.ProjectUpdate{Name: "proj-a", Description: &desc})
	require.NoError(t, err)
	assert.True(t, isConflict(m.DeleteProject(ctx, "proj-a")), "UpdateInProgress blocks delete")

	fc.Advance(5 * time.Second)
	require.NoError(t, m.DeleteProject(ctx, "proj-a"))
}

func TestProjects_Cap500(t *testing.T) { // T102
	m := newTestMock()
	ctx := context.Background()

	for i := 0; i < driver.MaxProjects; i++ {
		_, err := m.CreateProject(ctx, scSpec("p"+strconv.Itoa(i)))
		require.NoError(t, err)
	}

	_, err := m.CreateProject(ctx, scSpec("one-too-many"))
	require.Error(t, err)
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err))

	// concurrent creates against a nearly-full account never exceed the cap
	require.NoError(t, m.DeleteProject(ctx, "p0"))
	require.NoError(t, m.DeleteProject(ctx, "p1"))

	wins := 0

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	for i := 0; i < 20; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			if _, cerr := m.CreateProject(ctx, scSpec("race"+strconv.Itoa(i))); cerr == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	assert.Equal(t, 2, wins, "exactly the two free slots are taken")
	assert.Equal(t, driver.MaxProjects, m.projects.Len())
}

func cognitoWorkforce(name string) driver.WorkforceSpec {
	return driver.WorkforceSpec{Name: name, Cognito: &driver.CognitoConfig{UserPool: "us-east-1_abc", ClientID: "client-1"}}
}

func oidcConfig() *driver.OIDCConfig {
	return &driver.OIDCConfig{
		AuthorizationEndpoint: "https://idp/auth", ClientID: "c", ClientSecret: "s3cret", Issuer: "https://idp",
		JwksURI: "https://idp/jwks", LogoutEndpoint: "https://idp/logout", TokenEndpoint: "https://idp/token", UserInfoEndpoint: "https://idp/userinfo",
	}
}

func cognitoMember(group string) driver.MemberDefinition {
	return driver.MemberDefinition{CognitoClientID: "client-1", CognitoUserPool: "us-east-1_abc", CognitoUserGroup: group}
}

func TestWorkforce_CRUDAndValidation(t *testing.T) { // T103, T104
	m, fc := newTestMockClock()
	ctx := context.Background()

	w, err := m.CreateWorkforce(ctx, driver.WorkforceSpec{
		Name: "default", Cognito: &driver.CognitoConfig{UserPool: "p", ClientID: "c"}, SourceCIDRs: []string{"10.0.0.0/24"}, Tags: []driver.Tag{{Key: "k", Value: "v"}},
	})
	require.NoError(t, err)
	assert.Equal(t, driver.WorkforceActive, w.Status)
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:workforce/default", w.ARN)
	assert.Equal(t, "ipv4", w.IPAddressType)
	assert.NotEmpty(t, w.SubDomain)

	_, err = m.CreateWorkforce(ctx, cognitoWorkforce("second"))
	require.Error(t, err)
	assert.True(t, errors.IsAlreadyExists(err), "one workforce per region and account")

	fc.Advance(time.Minute)

	up, err := m.UpdateWorkforce(ctx, driver.WorkforceUpdate{Name: "default", SourceCIDRs: []string{"10.1.0.0/16", "192.168.0.0/24"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"10.1.0.0/16", "192.168.0.0/24"}, up.SourceCIDRs)
	assert.Greater(t, parseTime(up.LastUpdatedDate).UnixNano(), parseTime(up.CreateDate).UnixNano())

	_, err = m.UpdateWorkforce(ctx, driver.WorkforceUpdate{Name: "default", OIDC: oidcConfig()})
	require.Error(t, err, "a Cognito workforce takes no OIDC config")
	assert.True(t, errors.IsInvalidArgument(err))

	require.NoError(t, m.DeleteWorkforce(ctx, "default"))

	_, err = m.DescribeWorkforce(ctx, "default")
	assert.True(t, errors.IsNotFound(err))

	for name, spec := range map[string]driver.WorkforceSpec{
		"bad name":        {Name: "-x", Cognito: cognitoWorkforce("x").Cognito},
		"neither":         {Name: "w"},
		"both":            {Name: "w", Cognito: cognitoWorkforce("x").Cognito, OIDC: oidcConfig()},
		"cognito partial": {Name: "w", Cognito: &driver.CognitoConfig{UserPool: "p"}},
		"oidc missing":    {Name: "w", OIDC: &driver.OIDCConfig{ClientID: "c"}},
		"oidc http":       {Name: "w", OIDC: func() *driver.OIDCConfig { o := oidcConfig(); o.Issuer = "http://idp"; return o }()},
		"bad cidr":        {Name: "w", Cognito: cognitoWorkforce("x").Cognito, SourceCIDRs: []string{"nope"}},
		"bare ip":         {Name: "w", Cognito: cognitoWorkforce("x").Cognito, SourceCIDRs: []string{"10.0.0.1"}},
		"11 cidrs":        {Name: "w", Cognito: cognitoWorkforce("x").Cognito, SourceCIDRs: manyCIDRs(11)},
		"ipv6 w/o dual":   {Name: "w", Cognito: cognitoWorkforce("x").Cognito, SourceCIDRs: []string{"2001:db8::/32"}},
		"cidr + vpc":      {Name: "w", Cognito: cognitoWorkforce("x").Cognito, SourceCIDRs: []string{"10.0.0.0/8"}, VPC: &driver.WorkforceVPC{VPCID: "vpc-1"}},
		"bad ip type":     {Name: "w", Cognito: cognitoWorkforce("x").Cognito, IPAddressType: "ipx"},
	} {
		_, err = m.CreateWorkforce(ctx, spec)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	_, err = m.CreateWorkforce(ctx, driver.WorkforceSpec{Name: "dual", Cognito: cognitoWorkforce("x").Cognito, IPAddressType: "dualstack", SourceCIDRs: []string{"2001:db8::/32", "10.0.0.0/8"}})
	require.NoError(t, err)
}

func manyCIDRs(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, "10."+strconv.Itoa(i)+".0.0/16")
	}

	return out
}

func exceptionOf(err error) string {
	ex, _ := driver.ExceptionOf(err)

	return ex
}

func TestWorkteam_CRUDAndGuards(t *testing.T) { // T105, T106, T107
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateWorkteam(ctx, driver.WorkteamSpec{Name: "t1", Description: "d", Members: []driver.MemberDefinition{cognitoMember("g")}})
	require.Error(t, err, "no workforce yet")
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.CreateWorkforce(ctx, cognitoWorkforce("default"))
	require.NoError(t, err)

	tm, err := m.CreateWorkteam(ctx, driver.WorkteamSpec{
		Name: "t1", Description: "labelers", Members: []driver.MemberDefinition{cognitoMember("g1")},
		NotificationTopicARN: "arn:aws:sns:us-east-1:123456789012:t", Tags: []driver.Tag{{Key: "k", Value: "v"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:workteam/private-crowd/t1", tm.ARN)
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:workforce/default", tm.WorkforceARN)

	_, err = m.CreateWorkteam(ctx, driver.WorkteamSpec{Name: "t1", Description: "x", Members: []driver.MemberDefinition{cognitoMember("g")}})
	require.Error(t, err)
	assert.Equal(t, driver.ExResourceInUse, exceptionOf(err))

	err = m.DeleteWorkforce(ctx, "default")
	require.Error(t, err)
	assert.True(t, errors.IsFailedPrecondition(err), "a workforce with workteams cannot be deleted")

	for name, spec := range map[string]driver.WorkteamSpec{
		"bad name":     {Name: "-t", Description: "d", Members: []driver.MemberDefinition{cognitoMember("g")}},
		"no desc":      {Name: "t2", Members: []driver.MemberDefinition{cognitoMember("g")}},
		"long desc":    {Name: "t2", Description: strings.Repeat("d", 201), Members: []driver.MemberDefinition{cognitoMember("g")}},
		"no members":   {Name: "t2", Description: "d"},
		"11 members":   {Name: "t2", Description: "d", Members: manyMembers(11)},
		"both forms":   {Name: "t2", Description: "d", Members: []driver.MemberDefinition{{CognitoClientID: "client-1", CognitoUserPool: "us-east-1_abc", CognitoUserGroup: "g", OIDCGroups: []string{"x"}}}},
		"oidc on cog":  {Name: "t2", Description: "d", Members: []driver.MemberDefinition{{OIDCGroups: []string{"x"}}}},
		"wrong pool":   {Name: "t2", Description: "d", Members: []driver.MemberDefinition{{CognitoClientID: "client-1", CognitoUserPool: "other", CognitoUserGroup: "g"}}},
		"no workforce": {Name: "t2", Description: "d", WorkforceName: "ghost", Members: []driver.MemberDefinition{cognitoMember("g")}},
	} {
		_, err = m.CreateWorkteam(ctx, spec)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	desc := "updated"
	up, err := m.UpdateWorkteam(ctx, driver.WorkteamUpdate{Name: "t1", Description: &desc, Members: []driver.MemberDefinition{cognitoMember("g2")}})
	require.NoError(t, err)
	assert.Equal(t, "updated", up.Description)
	assert.Equal(t, "g2", up.Members[0].CognitoUserGroup)

	_, err = m.UpdateWorkteam(ctx, driver.WorkteamUpdate{Name: "ghost", Description: &desc})
	assert.True(t, errors.IsNotFound(err))

	ok, err := m.DeleteWorkteam(ctx, "t1")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = m.DeleteWorkteam(ctx, "t1")
	require.NoError(t, err)
	assert.False(t, ok, "a missing workteam is Success=false, not an error")

	_, err = m.ListTags(ctx, tm.ARN)
	assert.True(t, errors.IsNotFound(err), "tags go with the workteam")

	require.NoError(t, m.DeleteWorkforce(ctx, "default"))
}

func manyMembers(n int) []driver.MemberDefinition {
	out := make([]driver.MemberDefinition, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, cognitoMember("g"+strconv.Itoa(i)))
	}

	return out
}

func TestWorkteam_Cap25AndOIDCConflict(t *testing.T) { // T105, T107
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateWorkforce(ctx, driver.WorkforceSpec{Name: "default", OIDC: oidcConfig()})
	require.NoError(t, err)

	for i := 0; i < driver.MaxWorkteams; i++ {
		_, err = m.CreateWorkteam(ctx, driver.WorkteamSpec{Name: "t" + strconv.Itoa(i), Description: "d", Members: []driver.MemberDefinition{{OIDCGroups: []string{"g"}}}})
		require.NoError(t, err)
	}

	_, err = m.CreateWorkteam(ctx, driver.WorkteamSpec{Name: "extra", Description: "d", Members: []driver.MemberDefinition{{OIDCGroups: []string{"g"}}}})
	require.Error(t, err)
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err))

	o := oidcConfig()
	o.Issuer = "https://new-idp"
	_, err = m.UpdateWorkforce(ctx, driver.WorkforceUpdate{Name: "default", OIDC: o})
	require.Error(t, err)
	assert.True(t, isConflict(err), "the OIDC config cannot change while workteams exist")

	for i := 0; i < driver.MaxWorkteams; i++ {
		_, derr := m.DeleteWorkteam(ctx, "t"+strconv.Itoa(i))
		require.NoError(t, derr)
	}

	up, err := m.UpdateWorkforce(ctx, driver.WorkforceUpdate{Name: "default", OIDC: o})
	require.NoError(t, err)
	assert.Equal(t, "https://new-idp", up.OIDC.Issuer)
}

func TestWorkteam_ConcurrentCap(t *testing.T) { // T107
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateWorkforce(ctx, cognitoWorkforce("default"))
	require.NoError(t, err)

	var (
		wg   sync.WaitGroup
		wins atomic.Int32
	)

	for i := 0; i < 60; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			if _, cerr := m.CreateWorkteam(ctx, driver.WorkteamSpec{Name: "t" + strconv.Itoa(i), Description: "d", Members: []driver.MemberDefinition{cognitoMember("g")}}); cerr == nil {
				wins.Add(1)
			}
		}(i)
	}

	wg.Wait()
	assert.Equal(t, int32(driver.MaxWorkteams), wins.Load())
}

func TestListLabelingJobsForWorkteam(t *testing.T) { // T108
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateWorkforce(ctx, cognitoWorkforce("default"))
	require.NoError(t, err)

	tm, err := m.CreateWorkteam(ctx, driver.WorkteamSpec{Name: "t1", Description: "d", Members: []driver.MemberDefinition{cognitoMember("g")}})
	require.NoError(t, err)

	for _, name := range []string{"lj-b", "lj-a"} {
		_, err = m.CreateLabelingJob(ctx, driver.LabelingJobConfig{JobName: name, WorkteamARN: tm.ARN})
		require.NoError(t, err)
	}

	_, err = m.CreateLabelingJob(ctx, driver.LabelingJobConfig{JobName: "other", WorkteamARN: "arn:aws:sagemaker:us-east-1:123456789012:workteam/private-crowd/elsewhere"})
	require.NoError(t, err)

	page, err := m.ListLabelingJobsForWorkteam(ctx, tm.ARN, driver.ListOptions{SortBy: driver.SortByName})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.Equal(t, "lj-a", page.Items[0].JobName)
	assert.Equal(t, driver.JobReferenceCode("lj-a"), page.Items[0].JobReferenceCode)
	assert.Equal(t, "123456789012", page.Items[0].WorkRequesterAccountID)

	page, err = m.ListLabelingJobsForWorkteam(ctx, tm.ARN, driver.ListOptions{Filters: map[string]string{"JobReferenceCodeContains": driver.JobReferenceCode("lj-b")}})
	require.NoError(t, err)
	assert.Len(t, page.Items, 1)

	_, err = m.ListLabelingJobsForWorkteam(ctx, "arn:aws:sagemaker:us-east-1:123456789012:workteam/private-crowd/ghost", driver.ListOptions{})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	subs, err := m.ListSubscribedWorkteams(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, subs.Items)

	_, err = m.DescribeSubscribedWorkteam(ctx, "arn:aws:sagemaker:us-east-1:123456789012:workteam/vendor-crowd/x")
	assert.True(t, errors.IsNotFound(err))
}

const testRole = "arn:aws:iam::123456789012:role/svc"

func seedImage(t *testing.T, m *Mock, name string) {
	t.Helper()

	_, err := m.CreateImage(context.Background(), driver.ImageSpec{Name: name, RoleARN: testRole, DisplayName: "Disp", Description: "desc"})
	require.NoError(t, err)
}

func versionSpec(image, token string, aliases ...string) driver.ImageVersionSpec {
	return driver.ImageVersionSpec{ImageName: image, BaseImage: "123456789012.dkr.ecr.us-east-1.amazonaws.com/repo:1", ClientToken: token, Aliases: aliases}
}

func TestImage_CRUDAndValidation(t *testing.T) { // T109, T113
	m, fc := newTestMockClock()
	ctx := context.Background()

	img, err := m.CreateImage(ctx, driver.ImageSpec{Name: "my.image-1", RoleARN: testRole, DisplayName: "My image", Description: "d", Tags: []driver.Tag{{Key: "k", Value: "v"}}})
	require.NoError(t, err)
	assert.Equal(t, driver.ImageStatusCreated, img.Status)
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:image/my.image-1", img.ImageARN)

	_, err = m.CreateImage(ctx, driver.ImageSpec{Name: "my.image-1", RoleARN: testRole})
	require.Error(t, err)
	assert.Equal(t, driver.ExResourceInUse, exceptionOf(err), "CreateImage models ResourceInUse")

	for name, spec := range map[string]driver.ImageSpec{
		"empty":      {Name: "", RoleARN: testRole},
		"double dot": {Name: "a..b", RoleARN: testRole},
		"dash dot":   {Name: "a-.b", RoleARN: testRole},
		"64 chars":   {Name: strings.Repeat("a", 64), RoleARN: testRole},
		"bad role":   {Name: "x", RoleARN: "role"},
		"long desc":  {Name: "x", RoleARN: testRole, Description: strings.Repeat("d", 513)},
		"blank name": {Name: "x", RoleARN: testRole, DisplayName: " lead"},
	} {
		_, err = m.CreateImage(ctx, spec)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	_, err = m.CreateImage(ctx, driver.ImageSpec{Name: strings.Repeat("a", 63), RoleARN: testRole})
	require.NoError(t, err, "63 characters is allowed")

	fc.Advance(time.Minute)

	newDesc := "updated"
	up, err := m.UpdateImage(ctx, driver.ImageUpdate{Name: "my.image-1", Description: &newDesc, DeleteProperties: []string{"DisplayName"}})
	require.NoError(t, err)
	assert.Equal(t, "updated", up.Description)
	assert.Empty(t, up.DisplayName, "DeleteProperties clears it")
	assert.Greater(t, parseTime(up.LastModifiedTime).UnixNano(), parseTime(up.CreationTime).UnixNano())

	for name, u := range map[string]driver.ImageUpdate{
		"set and delete": {Name: "my.image-1", Description: &newDesc, DeleteProperties: []string{"Description"}},
		"bad property":   {Name: "my.image-1", DeleteProperties: []string{"RoleArn"}},
		"three props":    {Name: "my.image-1", DeleteProperties: []string{"DisplayName", "Description", "DisplayName"}},
		"bad role":       {Name: "my.image-1", RoleARN: ptr("nope")},
	} {
		_, err = m.UpdateImage(ctx, u)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	_, err = m.UpdateImage(ctx, driver.ImageUpdate{Name: "ghost", Description: &newDesc})
	assert.True(t, errors.IsNotFound(err))

	tags, err := m.ListTags(ctx, img.ImageARN)
	require.NoError(t, err)
	assert.Len(t, tags, 1)

	require.NoError(t, m.DeleteImage(ctx, "my.image-1"))
	assert.True(t, errors.IsNotFound(m.DeleteImage(ctx, "my.image-1")))

	_, err = m.ListTags(ctx, img.ImageARN)
	assert.True(t, errors.IsNotFound(err))
}

func TestImageVersion_MonotonicIdempotentAliases(t *testing.T) { // T110, T112
	m := newTestMock()
	ctx := context.Background()
	seedImage(t, m, "img")

	v1, err := m.CreateImageVersion(ctx, versionSpec("img", "tok-1", "stable"))
	require.NoError(t, err)
	v2, err := m.CreateImageVersion(ctx, versionSpec("img", "tok-2"))
	require.NoError(t, err)
	v3, err := m.CreateImageVersion(ctx, versionSpec("img", "tok-3", "latest"))
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, []int{v1.Version, v2.Version, v3.Version})
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:image-version/img/2", v2.ImageVersionARN)
	assert.Equal(t, driver.ImageVersionStatusCreate, v1.Status)
	assert.Equal(t, v1.BaseImage, v1.ContainerImage)

	again, err := m.CreateImageVersion(ctx, versionSpec("img", "tok-2"))
	require.NoError(t, err)
	assert.Equal(t, 2, again.Version, "a repeated ClientToken returns the same version")

	require.NoError(t, m.DeleteImageVersion(ctx, driver.ImageVersionRef{ImageName: "img", Version: ptr(3)}))

	v4, err := m.CreateImageVersion(ctx, versionSpec("img", "tok-4"))
	require.NoError(t, err)
	assert.Equal(t, 4, v4.Version, "a deleted highest version's number is never reused")

	_, err = m.CreateImageVersion(ctx, versionSpec("img", "tok-5", "stable"))
	require.Error(t, err)
	assert.Equal(t, driver.ExResourceInUse, exceptionOf(err), "an alias belongs to one version")

	byAlias, err := m.DescribeImageVersion(ctx, driver.ImageVersionRef{ImageName: "img", Alias: "stable"})
	require.NoError(t, err)
	assert.Equal(t, 1, byAlias.Version)

	latest, err := m.DescribeImageVersion(ctx, driver.ImageVersionRef{ImageName: "img"})
	require.NoError(t, err)
	assert.Equal(t, 4, latest.Version)

	_, err = m.DescribeImageVersion(ctx, driver.ImageVersionRef{ImageName: "img", Version: ptr(1), Alias: "stable"})
	assert.True(t, errors.IsInvalidArgument(err), "version and alias together are ambiguous")

	_, err = m.DescribeImageVersion(ctx, driver.ImageVersionRef{ImageName: "img", Version: ptr(99)})
	assert.True(t, errors.IsNotFound(err))

	up, err := m.UpdateImageVersion(ctx, driver.ImageVersionUpdate{
		Ref: driver.ImageVersionRef{ImageName: "img", Version: ptr(2)}, AliasesToAdd: []string{"beta", "rc.1"}, AliasesToDelete: []string{"missing"},
		ReleaseNotes: ptr("notes"), Horovod: ptr(true), JobType: ptr("TRAINING"), MLFramework: ptr("PyTorch 1.13"),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"beta", "rc.1"}, up.Aliases)
	assert.Equal(t, "notes", up.ReleaseNotes)
	assert.True(t, up.Horovod)

	_, err = m.UpdateImageVersion(ctx, driver.ImageVersionUpdate{Ref: driver.ImageVersionRef{ImageName: "img", Version: ptr(2)}, AliasesToAdd: []string{"stable"}})
	require.Error(t, err)
	assert.Equal(t, driver.ExResourceInUse, exceptionOf(err))

	up, err = m.UpdateImageVersion(ctx, driver.ImageVersionUpdate{Ref: driver.ImageVersionRef{ImageName: "img", Alias: "beta"}, AliasesToDelete: []string{"beta"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"rc.1"}, up.Aliases)

	all, err := m.ListAliases(ctx, driver.ImageVersionRef{ImageName: "img"}, driver.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"rc.1", "stable"}, all.Items, "aliases of every version, sorted")

	one, err := m.ListAliases(ctx, driver.ImageVersionRef{ImageName: "img", Version: ptr(1)}, driver.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"stable"}, one.Items)

	page, err := m.ListAliases(ctx, driver.ImageVersionRef{ImageName: "img"}, driver.ListOptions{MaxResults: 1})
	require.NoError(t, err)
	assert.Len(t, page.Items, 1)
	assert.NotEmpty(t, page.NextToken)

	vs, err := m.ListImageVersions(ctx, "img", driver.ListOptions{SortBy: driver.SortByName, SortOrder: driver.SortDescending})
	require.NoError(t, err)
	require.Len(t, vs.Items, 3)
	assert.Equal(t, []int{4, 2, 1}, []int{vs.Items[0].Version, vs.Items[1].Version, vs.Items[2].Version}, "VERSION sorts numerically")

	for name, spec := range map[string]driver.ImageVersionSpec{
		"no base image": {ImageName: "img", ClientToken: "t"},
		"no token":      {ImageName: "img", BaseImage: "b"},
		"bad token":     {ImageName: "img", BaseImage: "b", ClientToken: "bad token!"},
		"bad alias":     {ImageName: "img", BaseImage: "b", ClientToken: "t9", Aliases: []string{".hidden"}},
		"bad job type":  {ImageName: "img", BaseImage: "b", ClientToken: "t9", JobType: "SERVING"},
		"bad framework": {ImageName: "img", BaseImage: "b", ClientToken: "t9", MLFramework: "pytorch-one"},
		"long notes":    {ImageName: "img", BaseImage: "b", ClientToken: "t9", ReleaseNotes: strings.Repeat("n", 256)},
	} {
		_, err = m.CreateImageVersion(ctx, spec)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	_, err = m.CreateImageVersion(ctx, versionSpec("ghost", "t"))
	assert.True(t, errors.IsNotFound(err))
}

func TestImageVersion_ConcurrentCreates(t *testing.T) { // T110
	m := newTestMock()
	ctx := context.Background()
	seedImage(t, m, "img")

	const n = 40

	versions := make(chan int, n)

	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			v, err := m.CreateImageVersion(ctx, versionSpec("img", "tok-"+strconv.Itoa(i)))
			if err == nil {
				versions <- v.Version
			}
		}(i)
	}

	wg.Wait()
	close(versions)

	seen := map[int]bool{}
	for v := range versions {
		assert.False(t, seen[v], "version %d minted twice", v)
		seen[v] = true
	}

	assert.Len(t, seen, n)

	for i := 1; i <= n; i++ {
		assert.True(t, seen[i], "no gap at version %d", i)
	}
}

func TestDeleteImage_CascadesVersions(t *testing.T) { // T111
	m := newTestMock()
	ctx := context.Background()
	seedImage(t, m, "img")
	seedImage(t, m, "other")

	v, err := m.CreateImageVersion(ctx, versionSpec("img", "t1", "a"))
	require.NoError(t, err)
	_, err = m.AddTags(ctx, v.ImageVersionARN, []driver.Tag{{Key: "k", Value: "v"}})
	require.NoError(t, err)
	_, err = m.CreateImageVersion(ctx, versionSpec("other", "t1", "a"))
	require.NoError(t, err)

	require.NoError(t, m.DeleteImage(ctx, "img"))

	assert.Equal(t, 1, m.imageVersions.Len(), "only the other image's version remains")

	_, err = m.ListTags(ctx, v.ImageVersionARN)
	assert.True(t, errors.IsNotFound(err))

	_, err = m.ListImageVersions(ctx, "img", driver.ListOptions{})
	assert.True(t, errors.IsNotFound(err))

	seedImage(t, m, "img")

	again, err := m.CreateImageVersion(ctx, versionSpec("img", "t1", "a"))
	require.NoError(t, err)
	assert.Equal(t, 1, again.Version, "a re-created image starts over at version 1 and may reuse an alias")
}

func TestAppImageConfig_CRUDAndDefaults(t *testing.T) { // T112
	m, fc := newTestMockClock()
	ctx := context.Background()

	c, err := m.CreateAppImageConfig(ctx, driver.AppImageConfigSpec{
		Name: "cfg-1",
		KernelGateway: &driver.AppImageConfigBody{
			KernelSpecs:      []driver.KernelSpec{{Name: "python3", DisplayName: "Python 3"}},
			FileSystemConfig: &driver.FileSystemConfig{},
		},
		Tags: []driver.Tag{{Key: "k", Value: "v"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "/home/sagemaker-user", c.KernelGateway.FileSystemConfig.MountPath, "the documented default mount path")

	_, err = m.CreateAppImageConfig(ctx, driver.AppImageConfigSpec{Name: "cfg-1"})
	require.Error(t, err)
	assert.Equal(t, driver.ExResourceInUse, exceptionOf(err))

	for name, spec := range map[string]driver.AppImageConfigSpec{
		"bad name":      {Name: "-x"},
		"no kernels":    {Name: "x", KernelGateway: &driver.AppImageConfigBody{}},
		"six kernels":   {Name: "x", KernelGateway: &driver.AppImageConfigBody{KernelSpecs: make([]driver.KernelSpec, 6)}},
		"unnamed spec":  {Name: "x", KernelGateway: &driver.AppImageConfigBody{KernelSpecs: []driver.KernelSpec{{}}}},
		"uid too big":   {Name: "x", JupyterLab: &driver.AppImageConfigBody{FileSystemConfig: &driver.FileSystemConfig{DefaultUID: ptr(65536)}}},
		"relative path": {Name: "x", CodeEditor: &driver.AppImageConfigBody{FileSystemConfig: &driver.FileSystemConfig{MountPath: "home/x"}}},
	} {
		_, err = m.CreateAppImageConfig(ctx, spec)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	fc.Advance(time.Minute)

	up, err := m.UpdateAppImageConfig(ctx, driver.AppImageConfigSpec{
		Name: "cfg-1", JupyterLab: &driver.AppImageConfigBody{ContainerConfig: &driver.ContainerConfig{Entrypoint: []string{"jupyter"}, Env: map[string]string{"A": "1"}}},
	})
	require.NoError(t, err)
	require.NotNil(t, up.KernelGateway, "blocks not named in the update are kept")
	require.NotNil(t, up.JupyterLab)
	assert.Equal(t, []string{"jupyter"}, up.JupyterLab.ContainerConfig.Entrypoint)
	assert.Greater(t, parseTime(up.LastModifiedTime).UnixNano(), parseTime(up.CreationTime).UnixNano())

	up.JupyterLab.ContainerConfig.Env["A"] = "mutated"

	got, _ := m.DescribeAppImageConfig(ctx, "cfg-1")
	assert.Equal(t, "1", got.JupyterLab.ContainerConfig.Env["A"], "reads are deep copies")

	_, err = m.UpdateAppImageConfig(ctx, driver.AppImageConfigSpec{Name: "ghost"})
	assert.True(t, errors.IsNotFound(err))

	require.NoError(t, m.DeleteAppImageConfig(ctx, "cfg-1"))
	assert.True(t, errors.IsNotFound(m.DeleteAppImageConfig(ctx, "cfg-1")))

	_, err = m.ListTags(ctx, c.ARN)
	assert.True(t, errors.IsNotFound(err))
}

func strp(s string) *string { return &s }

func seedTrial(t *testing.T, m *Mock, exp, trial string) {
	t.Helper()

	ctx := context.Background()
	if !m.experiments.Has(exp) {
		_, err := m.CreateExperiment(ctx, driver.ExperimentSpec{ExperimentName: exp})
		require.NoError(t, err)
	}

	_, err := m.CreateTrial(ctx, driver.TrialSpec{TrialName: trial, ExperimentName: exp})
	require.NoError(t, err)
}

func TestTrialComponent_CRUDAndLimits(t *testing.T) { // T90, T91
	m, fc := newTestMockClock()
	ctx := context.Background()

	tc, err := m.CreateTrialComponent(ctx, driver.TrialComponentSpec{
		Name: "Run-1", DisplayName: "first", Status: &driver.TrialComponentStatus{PrimaryStatus: "InProgress", Message: "go"},
		Parameters:      map[string]driver.TrialComponentParameter{"lr": {NumberValue: ptr(0.1)}, "opt": {StringValue: strp("adam")}},
		InputArtifacts:  map[string]driver.TrialComponentArtifact{"train": {Value: "s3://b/train", MediaType: "text/csv"}},
		OutputArtifacts: map[string]driver.TrialComponentArtifact{"model": {Value: "s3://b/model"}},
		Tags:            []driver.Tag{{Key: "k", Value: "v"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:experiment-trial-component/Run-1", tc.ARN)

	got, err := m.DescribeTrialComponent(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, "Run-1", got.Name, "names are not case-sensitive but keep their spelling")

	_, err = m.CreateTrialComponent(ctx, driver.TrialComponentSpec{Name: "RUN-1"})
	require.Error(t, err)
	assert.True(t, errors.IsAlreadyExists(err), "a differently-cased duplicate is rejected")

	fc.Advance(time.Minute)

	up, err := m.UpdateTrialComponent(ctx, driver.TrialComponentUpdate{
		Name: "run-1", Parameters: map[string]driver.TrialComponentParameter{"epochs": {NumberValue: ptr(5.0)}, "opt": {StringValue: strp("sgd")}},
		ParametersToRemove: []string{"lr"}, InputArtifacts: map[string]driver.TrialComponentArtifact{"val": {Value: "s3://b/val"}},
		OutputArtifactsToRemove: []string{"model"}, Status: &driver.TrialComponentStatus{PrimaryStatus: "Completed"},
	})
	require.NoError(t, err)
	assert.Len(t, up.Parameters, 2, "merged by key: lr removed, epochs added, opt replaced")
	assert.Equal(t, "sgd", *up.Parameters["opt"].StringValue)
	assert.Len(t, up.InputArtifacts, 2)
	assert.Empty(t, up.OutputArtifacts)
	assert.Equal(t, "Completed", up.Status.PrimaryStatus)
	assert.Greater(t, parseTime(up.LastModifiedTime).UnixNano(), parseTime(up.CreationTime).UnixNano())

	bigParams := map[string]driver.TrialComponentParameter{}
	for i := 0; i < 301; i++ {
		bigParams["p"+strconv.Itoa(i)] = driver.TrialComponentParameter{NumberValue: ptr(1.0)}
	}

	bigArts := map[string]driver.TrialComponentArtifact{}
	for i := 0; i < 61; i++ {
		bigArts["a"+strconv.Itoa(i)] = driver.TrialComponentArtifact{Value: "v"}
	}

	for name, spec := range map[string]driver.TrialComponentSpec{
		"301 params":     {Name: "x", Parameters: bigParams},
		"61 inputs":      {Name: "x", InputArtifacts: bigArts},
		"61 outputs":     {Name: "x", OutputArtifacts: bigArts},
		"bad name":       {Name: "-x"},
		"bad display":    {Name: "x", DisplayName: "has space"},
		"bad status":     {Name: "x", Status: &driver.TrialComponentStatus{PrimaryStatus: "Unknown"}},
		"param no value": {Name: "x", Parameters: map[string]driver.TrialComponentParameter{"a": {}}},
		"param both":     {Name: "x", Parameters: map[string]driver.TrialComponentParameter{"a": {NumberValue: ptr(1.0), StringValue: strp("s")}}},
		"artifact empty": {Name: "x", InputArtifacts: map[string]driver.TrialComponentArtifact{"a": {}}},
	} {
		_, err = m.CreateTrialComponent(ctx, spec)
		require.Error(t, err, name)
	}

	_, err = m.CreateTrialComponent(ctx, driver.TrialComponentSpec{Name: "x", Parameters: bigParams})
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err), "301 parameters exceed the per-component limit")

	max := map[string]driver.TrialComponentParameter{}
	for i := 0; i < 300; i++ {
		max["p"+strconv.Itoa(i)] = driver.TrialComponentParameter{NumberValue: ptr(1.0)}
	}

	_, err = m.CreateTrialComponent(ctx, driver.TrialComponentSpec{Name: "ok-max", Parameters: max})
	require.NoError(t, err, "300 parameters is allowed")

	_, err = m.UpdateTrialComponent(ctx, driver.TrialComponentUpdate{Name: "ok-max", Parameters: map[string]driver.TrialComponentParameter{"one-more": {NumberValue: ptr(1.0)}}})
	require.Error(t, err, "the merged result may not exceed the limit")

	_, err = m.UpdateTrialComponent(ctx, driver.TrialComponentUpdate{Name: "ghost"})
	assert.True(t, errors.IsNotFound(err))

	got.Parameters["lr"] = driver.TrialComponentParameter{}

	again, _ := m.DescribeTrialComponent(ctx, "run-1")
	assert.NotContains(t, again.Parameters, "lr", "reads are deep copies")
}

func TestTrialComponent_AssociationsAndDeleteGuards(t *testing.T) { // T92, T93
	m := newTestMock()
	ctx := context.Background()
	seedTrial(t, m, "exp-1", "trial-1")
	seedTrial(t, m, "exp-1", "trial-2")

	_, err := m.CreateTrialComponent(ctx, driver.TrialComponentSpec{Name: "comp-1"})
	require.NoError(t, err)

	compARN, trialARN, err := m.AssociateTrialComponent(ctx, "comp-1", "trial-1")
	require.NoError(t, err)
	assert.Contains(t, compARN, "experiment-trial-component/comp-1")
	assert.Contains(t, trialARN, "experiment-trial/trial-1")

	_, _, err = m.AssociateTrialComponent(ctx, "comp-1", "trial-1")
	require.NoError(t, err, "re-associating succeeds")

	_, _, err = m.AssociateTrialComponent(ctx, "COMP-1", "trial-2")
	require.NoError(t, err, "a component can be in several trials")

	tc, _ := m.DescribeTrialComponent(ctx, "comp-1")
	assert.Equal(t, []string{"trial-1", "trial-2"}, tc.TrialNames)

	tr, _ := m.DescribeTrial(ctx, "trial-1")
	assert.Equal(t, []string{"comp-1"}, tr.TrialComponentNames, "both ends are kept in step")

	for _, c := range [][2]string{{"ghost", "trial-1"}, {"comp-1", "ghost"}} {
		_, _, err = m.AssociateTrialComponent(ctx, c[0], c[1])
		assert.True(t, errors.IsNotFound(err), c)
	}

	_, err = m.DeleteTrialComponent(ctx, "comp-1")
	require.Error(t, err)
	assert.True(t, errors.IsFailedPrecondition(err), "disassociate before deleting")

	err = m.DeleteTrial(ctx, "trial-1")
	require.Error(t, err, "a trial with components cannot be deleted")

	err = m.DeleteExperiment(ctx, "exp-1")
	require.Error(t, err, "an experiment with trials cannot be deleted")

	_, _, err = m.DisassociateTrialComponent(ctx, "comp-1", "trial-1")
	require.NoError(t, err)
	_, _, err = m.DisassociateTrialComponent(ctx, "comp-1", "trial-1")
	require.NoError(t, err, "removing a missing link is a no-op")
	_, _, err = m.DisassociateTrialComponent(ctx, "comp-1", "trial-2")
	require.NoError(t, err)

	tr, _ = m.DescribeTrial(ctx, "trial-1")
	assert.Empty(t, tr.TrialComponentNames)

	_, err = m.DeleteTrialComponent(ctx, "comp-1")
	require.NoError(t, err)

	_, err = m.DescribeTrialComponent(ctx, "comp-1")
	assert.True(t, errors.IsNotFound(err))

	require.NoError(t, m.DeleteTrial(ctx, "trial-1"))
	require.NoError(t, m.DeleteTrial(ctx, "trial-2"))
	require.NoError(t, m.DeleteExperiment(ctx, "exp-1"))

	_, err = m.CreateTrial(ctx, driver.TrialSpec{TrialName: "orphan", ExperimentName: "gone"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "CreateTrial under a missing experiment is NotFound")
}

func TestTrialComponent_ListFilters(t *testing.T) { // T90
	m, fc := newTestMockClock()
	ctx := context.Background()
	seedTrial(t, m, "exp-1", "trial-1")
	seedTrial(t, m, "exp-2", "trial-2")

	for _, n := range []string{"bravo", "alpha", "charlie"} {
		_, err := m.CreateTrialComponent(ctx, driver.TrialComponentSpec{Name: n})
		require.NoError(t, err)
		fc.Advance(time.Minute)
	}

	_, _, _ = m.AssociateTrialComponent(ctx, "bravo", "trial-1")
	_, _, _ = m.AssociateTrialComponent(ctx, "charlie", "trial-2")

	names := func(o driver.ListOptions) []string {
		page, err := m.ListTrialComponents(ctx, o)
		require.NoError(t, err)

		out := make([]string, 0, len(page.Items))
		for _, c := range page.Items {
			out = append(out, c.Name)
		}

		return out
	}

	assert.Equal(t, []string{"charlie", "alpha", "bravo"}, names(driver.ListOptions{}), "default: newest first")
	assert.Equal(t, []string{"alpha", "bravo", "charlie"}, names(driver.ListOptions{SortBy: driver.SortByName, SortOrder: driver.SortAscending}))
	assert.Equal(t, []string{"bravo"}, names(driver.ListOptions{Filters: map[string]string{"TrialName": "trial-1"}}))
	assert.Equal(t, []string{"charlie"}, names(driver.ListOptions{Filters: map[string]string{"ExperimentName": "exp-2"}}))

	_, err := m.ListTrialComponents(ctx, driver.ListOptions{Filters: map[string]string{"TrialName": "trial-1", "ExperimentName": "exp-1"}})
	require.Error(t, err, "the filters are mutually exclusive")
	assert.True(t, errors.IsInvalidArgument(err))

	for _, f := range []map[string]string{{"TrialName": "ghost"}, {"ExperimentName": "ghost"}} {
		_, err = m.ListTrialComponents(ctx, driver.ListOptions{Filters: f})
		assert.True(t, errors.IsNotFound(err), f)
	}

	assert.Empty(t, names(driver.ListOptions{Filters: map[string]string{"SourceArn": "arn:aws:sagemaker:us-east-1:123456789012:training-job/x"}}))

	page, err := m.ListTrialComponents(ctx, driver.ListOptions{MaxResults: 2})
	require.NoError(t, err)
	assert.Len(t, page.Items, 2)
	assert.NotEmpty(t, page.NextToken)
}

func TestTrialComponent_ConcurrentLinks(t *testing.T) { // T92
	m := newTestMock()
	ctx := context.Background()
	seedTrial(t, m, "exp", "trial")

	_, err := m.CreateTrialComponent(ctx, driver.TrialComponentSpec{Name: "comp"})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := 0; i < 30; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _, _ = m.AssociateTrialComponent(ctx, "comp", "trial")
			_, _, _ = m.DisassociateTrialComponent(ctx, "comp", "trial")
			_, _ = m.DeleteTrialComponent(ctx, "comp")
		}()
	}

	wg.Wait()

	tc, cerr := m.DescribeTrialComponent(ctx, "comp")
	tr, _ := m.DescribeTrial(ctx, "trial")

	if cerr == nil {
		assert.Equal(t, len(tc.TrialNames), len(tr.TrialComponentNames), "both ends agree")
	} else {
		assert.Empty(t, tr.TrialComponentNames, "a deleted component leaves no link behind")
	}
}

func artifactSpec(name, uri string) driver.LineageEntitySpec {
	return driver.LineageEntitySpec{
		Name: name, Type: "Dataset", Source: driver.LineageSource{SourceURI: uri, SourceTypes: []driver.LineageSourceType{{IDType: "S3ETag", Value: "abc"}}},
		Properties: map[string]string{"owner": "ml"},
	}
}

func TestLineage_EntitiesCRUD(t *testing.T) { // T94, T95, T97
	m, fc := newTestMockClock()
	ctx := context.Background()

	a, err := m.CreateArtifact(ctx, artifactSpec("data-1", "s3://b/data"))
	require.NoError(t, err)
	assert.Regexp(t, `^arn:aws:sagemaker:us-east-1:123456789012:artifact/[0-9a-f]+$`, a.ARN)

	for name, spec := range map[string]driver.LineageEntitySpec{
		"same source uri": artifactSpec("other", "s3://b/data"),
		"same name":       artifactSpec("data-1", "s3://b/other"),
	} {
		_, err = m.CreateArtifact(ctx, spec)
		require.Error(t, err, name)
		assert.True(t, errors.IsAlreadyExists(err), name)
	}

	unnamed, err := m.CreateArtifact(ctx, driver.LineageEntitySpec{Type: "Model", Source: driver.LineageSource{SourceURI: "s3://b/m"}})
	require.NoError(t, err, "an artifact name is optional")
	assert.Empty(t, unnamed.Name)

	for name, spec := range map[string]driver.LineageEntitySpec{
		"no uri":        {Name: "x", Type: "t"},
		"bad name":      {Name: "-x", Type: "t", Source: driver.LineageSource{SourceURI: "u"}},
		"bad id type":   {Name: "x", Type: "t", Source: driver.LineageSource{SourceURI: "u2", SourceTypes: []driver.LineageSourceType{{IDType: "Nope"}}}},
		"31 properties": {Name: "x", Type: "t", Source: driver.LineageSource{SourceURI: "u3"}, Properties: manyProps(31)},
		"long desc":     {Name: "x", Type: "t", Source: driver.LineageSource{SourceURI: "u4"}, Description: strings.Repeat("d", 3073)},
	} {
		_, err = m.CreateArtifact(ctx, spec)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	fc.Advance(time.Minute)

	up, err := m.UpdateArtifact(ctx, driver.LineageEntityUpdate{
		Key: a.ARN, Properties: map[string]string{"stage": "prod", "tmp": "x"}, PropertiesToRemove: []string{"tmp"},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"stage": "prod"}, up.Properties, "Properties replace the set; PropertiesToRemove then drops keys")
	assert.Greater(t, parseTime(up.LastModifiedTime).UnixNano(), parseTime(up.CreationTime).UnixNano())

	up.Properties["stage"] = "mutated"

	got, _ := m.DescribeArtifact(ctx, a.ARN)
	assert.Equal(t, "prod", got.Properties["stage"], "reads are deep copies")

	_, err = m.DescribeArtifact(ctx, "arn:aws:sagemaker:us-east-1:123456789012:artifact/ghost")
	assert.True(t, errors.IsNotFound(err))

	// actions and contexts
	act, err := m.CreateAction(ctx, driver.LineageEntitySpec{Name: "train", Type: "ModelTraining", Source: driver.LineageSource{SourceURI: "s3://a"}, Status: "InProgress", Description: "d"})
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:action/train", act.ARN)

	_, err = m.CreateAction(ctx, driver.LineageEntitySpec{Name: "train", Type: "t", Source: driver.LineageSource{SourceURI: "u"}})
	assert.True(t, errors.IsAlreadyExists(err))

	_, err = m.CreateAction(ctx, driver.LineageEntitySpec{Name: "bad", Type: "t", Source: driver.LineageSource{SourceURI: "u"}, Status: "Sleeping"})
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.CreateContext(ctx, driver.LineageEntitySpec{Name: "my_ctx", Type: "Endpoint", Source: driver.LineageSource{SourceURI: "arn:x"}})
	require.NoError(t, err, "context names may contain underscores")

	_, err = m.CreateAction(ctx, driver.LineageEntitySpec{Name: "has_under", Type: "t", Source: driver.LineageSource{SourceURI: "u"}})
	require.Error(t, err, "action names may not")

	st := "Completed"
	ua, err := m.UpdateAction(ctx, driver.LineageEntityUpdate{Key: "train", Status: &st})
	require.NoError(t, err)
	assert.Equal(t, "Completed", ua.Status)

	lst, err := m.ListActions(ctx, driver.ListOptions{Filters: map[string]string{"ActionType": "ModelTraining"}})
	require.NoError(t, err)
	assert.Len(t, lst.Items, 1)

	lst, err = m.ListArtifacts(ctx, driver.ListOptions{Filters: map[string]string{"SourceUri": "s3://b/data"}})
	require.NoError(t, err)
	assert.Len(t, lst.Items, 1)

	all, err := m.ListArtifacts(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all.Items, 2, "only artifacts")

	// delete by Source and by ARN
	arn, err := m.DeleteArtifact(ctx, "", "s3://b/m")
	require.NoError(t, err)
	assert.Equal(t, unnamed.ARN, arn)

	_, err = m.DeleteArtifact(ctx, "", "")
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.DeleteArtifact(ctx, "", "s3://none")
	assert.True(t, errors.IsNotFound(err))
}

func manyProps(n int) map[string]string {
	out := map[string]string{}
	for i := 0; i < n; i++ {
		out["k"+strconv.Itoa(i)] = "v"
	}

	return out
}

func TestLineage_ArtifactSourceURIConcurrent(t *testing.T) { // T100
	m := newTestMock()
	ctx := context.Background()

	var (
		wg   sync.WaitGroup
		wins atomic.Int32
	)

	for i := 0; i < 40; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			if _, err := m.CreateArtifact(ctx, artifactSpec("n"+strconv.Itoa(i), "s3://same")); err == nil {
				wins.Add(1)
			}
		}(i)
	}

	wg.Wait()
	assert.Equal(t, int32(1), wins.Load(), "exactly one artifact per source URI")
}

func TestLineage_AssociationsAndDeleteGuard(t *testing.T) { // T96, T97
	m, fc := newTestMockClock()
	ctx := context.Background()
	seedTrial(t, m, "exp", "trial")

	data, _ := m.CreateArtifact(ctx, artifactSpec("data", "s3://data"))
	model, _ := m.CreateArtifact(ctx, artifactSpec("model", "s3://model"))
	act, _ := m.CreateAction(ctx, driver.LineageEntitySpec{Name: "train", Type: "ModelTraining", Source: driver.LineageSource{SourceURI: "s3://a"}})
	_, err := m.CreateTrialComponent(ctx, driver.TrialComponentSpec{Name: "comp"})
	require.NoError(t, err)

	expARN := "arn:aws:sagemaker:us-east-1:123456789012:experiment/exp"
	compARN := "arn:aws:sagemaker:us-east-1:123456789012:experiment-trial-component/comp"

	require.NoError(t, m.AddAssociation(ctx, data.ARN, act.ARN, driver.AssociationContributedTo))
	fc.Advance(time.Minute)
	require.NoError(t, m.AddAssociation(ctx, act.ARN, model.ARN, driver.AssociationProduced))
	require.NoError(t, m.AddAssociation(ctx, act.ARN, model.ARN, driver.AssociationSameAs), "re-adding an edge succeeds and keeps the first")
	fc.Advance(time.Minute)
	require.NoError(t, m.AddAssociation(ctx, expARN, compARN, ""))

	for name, c := range map[string][3]string{
		"trial endpoint": {data.ARN, "arn:aws:sagemaker:us-east-1:123456789012:experiment-trial/trial", ""},
		"lineage group":  {data.ARN, "arn:aws:sagemaker:us-east-1:123456789012:lineage-group/x", ""},
		"foreign ARN":    {data.ARN, "arn:aws:s3:::bucket", ""},
		"bad type":       {data.ARN, act.ARN, "Teleports"},
	} {
		err = m.AddAssociation(ctx, c[0], c[1], c[2])
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	err = m.AddAssociation(ctx, data.ARN, "arn:aws:sagemaker:us-east-1:123456789012:artifact/ghost", "")
	assert.True(t, errors.IsNotFound(err))

	page, err := m.ListAssociations(ctx, driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	assert.Equal(t, expARN, page.Items[0].SourceARN, "newest first")

	byType, err := m.ListAssociations(ctx, driver.ListOptions{Filters: map[string]string{"AssociationType": driver.AssociationProduced}})
	require.NoError(t, err)
	require.Len(t, byType.Items, 1)
	assert.Equal(t, "train", byType.Items[0].SourceName)
	assert.Equal(t, "model", byType.Items[0].DestinationName)
	assert.Equal(t, "ModelTraining", byType.Items[0].SourceType)
	assert.Equal(t, "Dataset", byType.Items[0].DestinationType)

	bySrcType, err := m.ListAssociations(ctx, driver.ListOptions{Filters: map[string]string{"DestinationType": "TrialComponent"}})
	require.NoError(t, err)
	assert.Len(t, bySrcType.Items, 1)

	sorted, err := m.ListAssociations(ctx, driver.ListOptions{SortBy: "SourceArn", SortOrder: driver.SortAscending})
	require.NoError(t, err)
	assert.Equal(t, sorted.Items[0].SourceARN < sorted.Items[1].SourceARN, true)

	_, err = m.ListAssociations(ctx, driver.ListOptions{SortBy: "Bogus"})
	assert.True(t, errors.IsInvalidArgument(err))

	// an entity with associations cannot be deleted
	_, err = m.DeleteArtifact(ctx, data.ARN, "")
	require.Error(t, err)
	assert.True(t, errors.IsFailedPrecondition(err))

	_, err = m.DeleteAction(ctx, "train")
	require.Error(t, err)

	require.NoError(t, m.DeleteAssociation(ctx, data.ARN, act.ARN))
	assert.True(t, errors.IsNotFound(m.DeleteAssociation(ctx, data.ARN, act.ARN)))

	_, err = m.DeleteArtifact(ctx, data.ARN, "")
	require.NoError(t, err)

	// deleting a trial component removes its lineage edges
	_, err = m.DeleteTrialComponent(ctx, "comp")
	require.NoError(t, err)

	left, _ := m.ListAssociations(ctx, driver.ListOptions{})
	assert.Len(t, left.Items, 1, "no dangling edge to the deleted component")
}

func TestLineage_QueryLineage(t *testing.T) { // T98
	m := newTestMock()
	ctx := context.Background()

	mk := func(name, typ string) string {
		a, err := m.CreateArtifact(ctx, driver.LineageEntitySpec{Name: name, Type: typ, Source: driver.LineageSource{SourceURI: "s3://" + name}, Properties: map[string]string{"stage": typ}})
		require.NoError(t, err)

		return a.ARN
	}

	a, b, c, d := mk("a", "raw"), mk("b", "clean"), mk("c", "feature"), mk("d", "model")
	act, _ := m.CreateAction(ctx, driver.LineageEntitySpec{Name: "step", Type: "Step", Source: driver.LineageSource{SourceURI: "x"}})

	for _, e := range [][2]string{{a, b}, {b, c}, {c, d}, {act.ARN, b}} {
		require.NoError(t, m.AddAssociation(ctx, e[0], e[1], driver.AssociationDerivedFrom))
	}

	arns := func(q driver.LineageQuery) []string {
		res, err := m.QueryLineage(ctx, q)
		require.NoError(t, err)

		out := make([]string, 0, len(res.Vertices))
		for _, v := range res.Vertices {
			out = append(out, v.ARN)
		}

		return out
	}

	assert.ElementsMatch(t, []string{c, d}, arns(driver.LineageQuery{StartARNs: []string{b}, Direction: driver.LineageDescendants}))
	assert.ElementsMatch(t, []string{a, act.ARN}, arns(driver.LineageQuery{StartARNs: []string{b}, Direction: driver.LineageAscendants}))
	assert.ElementsMatch(t, []string{a, act.ARN, c, d}, arns(driver.LineageQuery{StartARNs: []string{b}}), "Both is the default")
	assert.Equal(t, []string{c}, arns(driver.LineageQuery{StartARNs: []string{b}, Direction: driver.LineageDescendants, MaxDepth: 1}))
	assert.ElementsMatch(t, []string{b, c, d}, arns(driver.LineageQuery{StartARNs: []string{a}, Direction: driver.LineageDescendants}), "depth defaults to 10")

	// a cycle terminates
	require.NoError(t, m.AddAssociation(ctx, d, a, driver.AssociationAssociatedWith))
	assert.ElementsMatch(t, []string{b, c, d, act.ARN}, arns(driver.LineageQuery{StartARNs: []string{a}}))

	res, err := m.QueryLineage(ctx, driver.LineageQuery{StartARNs: []string{b}, Direction: driver.LineageDescendants, IncludeEdges: true, MaxDepth: 1})
	require.NoError(t, err)
	require.Len(t, res.Edges, 1)
	assert.Equal(t, b, res.Edges[0].SourceARN)
	assert.Equal(t, c, res.Edges[0].DestinationARN)
	assert.Equal(t, driver.AssociationDerivedFrom, res.Edges[0].AssociationType)
	assert.Equal(t, "Artifact", res.Vertices[0].LineageType)
	assert.Equal(t, "feature", res.Vertices[0].Type)

	assert.Equal(t, []string{act.ARN}, arns(driver.LineageQuery{StartARNs: []string{b}, Direction: driver.LineageAscendants, LineageTypes: []string{"Action"}}))
	assert.Equal(t, []string{d}, arns(driver.LineageQuery{StartARNs: []string{b}, Direction: driver.LineageDescendants, Types: []string{"model"}}))
	assert.Equal(t, []string{c}, arns(driver.LineageQuery{StartARNs: []string{b}, Direction: driver.LineageDescendants, Properties: map[string]string{"stage": "feature"}}))

	page1, err := m.QueryLineage(ctx, driver.LineageQuery{StartARNs: []string{a}, MaxResults: 2})
	require.NoError(t, err)
	require.Len(t, page1.Vertices, 2)
	require.NotEmpty(t, page1.NextToken)

	page2, err := m.QueryLineage(ctx, driver.LineageQuery{StartARNs: []string{a}, MaxResults: 2, NextToken: page1.NextToken})
	require.NoError(t, err)
	assert.Len(t, page2.Vertices, 2)
	assert.NotEqual(t, page1.Vertices[0].ARN, page2.Vertices[0].ARN, "the token resumes after the first page")

	for name, q := range map[string]driver.LineageQuery{
		"no start":   {},
		"two starts": {StartARNs: []string{a, b}},
		"depth 11":   {StartARNs: []string{a}, MaxDepth: 11},
		"results 51": {StartARNs: []string{a}, MaxResults: 51},
		"bad dir":    {StartARNs: []string{a}, Direction: "Sideways"},
		"bad token":  {StartARNs: []string{a}, NextToken: "garbage"},
	} {
		_, err = m.QueryLineage(ctx, q)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	_, err = m.QueryLineage(ctx, driver.LineageQuery{StartARNs: []string{"arn:aws:sagemaker:us-east-1:123456789012:artifact/ghost"}})
	assert.True(t, errors.IsNotFound(err))
}

func TestLineage_GroupLifecycle(t *testing.T) { // T99
	m := newTestMock()
	ctx := context.Background()

	_, err := m.DescribeLineageGroup(ctx, driver.DefaultLineageGroupName)
	require.Error(t, err, "no group before the first lineage entity")
	assert.True(t, errors.IsNotFound(err))

	list, err := m.ListLineageGroups(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, list.Items)

	_, err = m.CreateAction(ctx, driver.LineageEntitySpec{Name: "a", Type: "t", Source: driver.LineageSource{SourceURI: "u"}})
	require.NoError(t, err)

	g, err := m.DescribeLineageGroup(ctx, driver.DefaultLineageGroupName)
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:lineage-group/sagemaker-default-lineage-group", g.ARN)

	byARN, err := m.DescribeLineageGroup(ctx, g.ARN)
	require.NoError(t, err, "the ARN works as the name")
	assert.Equal(t, g.Name, byARN.Name)

	_, err = m.DeleteAction(ctx, "a")
	require.NoError(t, err)

	_, err = m.DescribeLineageGroup(ctx, driver.DefaultLineageGroupName)
	require.NoError(t, err, "the group outlives its entities")

	arn, policy, err := m.GetLineageGroupPolicy(ctx, driver.DefaultLineageGroupName)
	require.NoError(t, err)
	assert.Equal(t, g.ARN, arn)
	assert.Empty(t, policy, "a policy can only be attached through RAM, which is not modeled")

	_, err = m.DescribeLineageGroup(ctx, "some-other-group")
	assert.True(t, errors.IsNotFound(err))

	list, _ = m.ListLineageGroups(ctx, driver.ListOptions{})
	assert.Len(t, list.Items, 1)

	// snapshot keeps the group and the graph
	src := newTestMock()
	_, err = src.CreateArtifact(ctx, artifactSpec("n", "s3://n"))
	require.NoError(t, err)
	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))
	_, err = dst.DescribeLineageGroup(ctx, driver.DefaultLineageGroupName)
	require.NoError(t, err)
}

func TestLineage_ConcurrentAssociateDelete(t *testing.T) { // T100
	m := newTestMock()
	ctx := context.Background()

	a, _ := m.CreateArtifact(ctx, artifactSpec("a", "s3://a"))
	b, _ := m.CreateArtifact(ctx, artifactSpec("b", "s3://b"))

	var wg sync.WaitGroup

	for i := 0; i < 40; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = m.AddAssociation(ctx, a.ARN, b.ARN, "")
			_, _ = m.DeleteArtifact(ctx, b.ARN, "")
			_ = m.DeleteAssociation(ctx, a.ARN, b.ARN)
			_, _ = m.QueryLineage(ctx, driver.LineageQuery{StartARNs: []string{a.ARN}})
		}()
	}

	wg.Wait()

	// no edge may reference a deleted artifact
	for _, as := range m.associations.All() {
		assert.True(t, m.lineageEntities.Has(as.SourceARN) && m.lineageEntities.Has(as.DestinationARN))
	}
}

func defSpec(kind, name string) driver.MonitoringJobDefinitionSpec {
	return driver.MonitoringJobDefinitionSpec{
		Kind: kind, Name: name, RoleARN: testRole,
		Echo: map[string]json.RawMessage{kind + "AppSpecification": json.RawMessage(`{"ImageUri":"img:1"}`)},
		Tags: []driver.Tag{{Key: "k", Value: "v"}},
	}
}

func TestMonitoringJobDefinitions_AllKinds(t *testing.T) { // T80, T81
	m := newTestMock()
	ctx := context.Background()

	for _, kind := range driver.MonitoringKinds {
		d, err := m.CreateMonitoringJobDefinition(ctx, defSpec(kind, "def-1"))
		require.NoError(t, err, kind)
		assert.Equal(t, kind, d.Kind)
		assert.Contains(t, d.ARN, "job-definition/def-1")

		_, err = m.CreateMonitoringJobDefinition(ctx, defSpec(kind, "def-1"))
		require.Error(t, err, kind)
		assert.Equal(t, driver.ExResourceInUse, exceptionOf(err), "a duplicate definition is ResourceInUse")

		got, err := m.DescribeMonitoringJobDefinition(ctx, kind, "def-1")
		require.NoError(t, err)
		assert.JSONEq(t, `{"ImageUri":"img:1"}`, string(got.Echo[kind+"AppSpecification"]))

		got.Echo["x"] = json.RawMessage(`1`)

		again, _ := m.DescribeMonitoringJobDefinition(ctx, kind, "def-1")
		assert.NotContains(t, again.Echo, "x", "reads are deep copies")

		tags, err := m.ListTags(ctx, d.ARN)
		require.NoError(t, err)
		assert.Len(t, tags, 1)
	}

	// the four kinds are separate namespaces
	_, err := m.DescribeMonitoringJobDefinition(ctx, driver.MonitoringDataQuality, "def-1")
	require.NoError(t, err)

	spec := defSpec(driver.MonitoringDataQuality, "ep-def")
	spec.EndpointName = "ep-1"
	_, err = m.CreateMonitoringJobDefinition(ctx, spec)
	require.NoError(t, err)

	page, err := m.ListMonitoringJobDefinitions(ctx, driver.MonitoringDataQuality, driver.ListOptions{Filters: map[string]string{"EndpointName": "ep-1"}})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "ep-def", page.Items[0].Name)

	all, err := m.ListMonitoringJobDefinitions(ctx, driver.MonitoringModelBias, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all.Items, 1, "only this kind")

	for name, bad := range map[string]driver.MonitoringJobDefinitionSpec{
		"empty name": {Kind: driver.MonitoringDataQuality, RoleARN: testRole},
		"dash name":  {Kind: driver.MonitoringDataQuality, Name: "-x", RoleARN: testRole},
		"64 chars":   {Kind: driver.MonitoringDataQuality, Name: strings.Repeat("a", 64), RoleARN: testRole},
		"bad role":   {Kind: driver.MonitoringDataQuality, Name: "x", RoleARN: "role"},
		"bad kind":   {Kind: "Other", Name: "x", RoleARN: testRole},
	} {
		_, err = m.CreateMonitoringJobDefinition(ctx, bad)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	require.NoError(t, m.DeleteMonitoringJobDefinition(ctx, driver.MonitoringDataQuality, "def-1"))
	assert.True(t, errors.IsNotFound(m.DeleteMonitoringJobDefinition(ctx, driver.MonitoringDataQuality, "def-1")))

	_, err = m.DescribeMonitoringJobDefinition(ctx, driver.MonitoringDataQuality, "def-1")
	assert.True(t, errors.IsNotFound(err))
}

func schedSpec(name, expr string) driver.MonitoringScheduleSpec {
	return driver.MonitoringScheduleSpec{
		Name: name, ScheduleExpression: expr, Config: json.RawMessage(`{"ScheduleConfig":{"ScheduleExpression":"` + expr + `"}}`),
	}
}

func hourly() string { return "cron(0 * ? * * *)" }

func execTimes(t *testing.T, m *Mock, schedule string) []string {
	t.Helper()

	page, err := m.ListMonitoringExecutions(context.Background(), driver.ListOptions{
		Filters: map[string]string{"MonitoringScheduleName": schedule}, SortBy: "ScheduledTime", SortOrder: driver.SortAscending,
	})
	require.NoError(t, err)

	out := make([]string, 0, len(page.Items))
	for _, e := range page.Items {
		out = append(out, parseTime(e.ScheduledTime).UTC().Format("15:04"))
	}

	return out
}

func TestMonitoringSchedule_CreateValidationAndStates(t *testing.T) { // T82, T83, T84, T85
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateMonitoringJobDefinition(ctx, defSpec(driver.MonitoringDataQuality, "dq"))
	require.NoError(t, err)

	s, err := m.CreateMonitoringSchedule(ctx, schedSpec("sched-1", hourly()))
	require.NoError(t, err)
	assert.Equal(t, driver.ScheduleScheduled, s.Status)
	assert.Equal(t, "arn:aws:sagemaker:us-east-1:123456789012:monitoring-schedule/sched-1", s.ARN)

	_, err = m.CreateMonitoringSchedule(ctx, schedSpec("sched-1", hourly()))
	require.Error(t, err)
	assert.Equal(t, driver.ExResourceInUse, exceptionOf(err))

	for _, expr := range []string{"cron(0 * ? * * *)", "cron(0 12 ? * * *)", "cron(0 17/12 ? * * *)", "cron(0 00 ? * * *)", "cron(0 23/1 ? * * *)"} {
		_, err = m.CreateMonitoringSchedule(ctx, schedSpec("ok-"+strings.NewReplacer(" ", "", "(", "", ")", "", "*", "s", "/", "d", "?", "q").Replace(expr), expr))
		require.NoError(t, err, expr)
	}

	for _, expr := range []string{"cron(0/5 * ? * * *)", "cron(30 * ? * * *)", "cron(0 25 ? * * *)", "cron(0 12 ? * MON *)", "rate(1 hour)", "cron(0 12/0 ? * * *)", "cron(0 12/25 ? * * *)", "daily"} {
		_, err = m.CreateMonitoringSchedule(ctx, schedSpec("bad", expr))
		require.Error(t, err, expr)
		assert.True(t, errors.IsInvalidArgument(err), expr)
	}

	now := schedSpec("now-1", "NOW")
	_, err = m.CreateMonitoringSchedule(ctx, now)
	require.Error(t, err, "NOW needs the analysis window")
	now.DataAnalysisStartTime, now.DataAnalysisEndTime = "-PT1H", "PT0S"
	_, err = m.CreateMonitoringSchedule(ctx, now)
	require.NoError(t, err)

	for name, w := range map[string][2]string{
		"over 24h": {"-PT25H", "PT0S"},
		"reversed": {"PT1H", "-PT1H"},
		"not iso":  {"1 hour", "0"},
		"empty P":  {"P", "PT0S"},
	} {
		spec := schedSpec("w-"+strings.ReplaceAll(name, " ", ""), "NOW")
		spec.DataAnalysisStartTime, spec.DataAnalysisEndTime = w[0], w[1]
		_, err = m.CreateMonitoringSchedule(ctx, spec)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	for name, spec := range map[string]driver.MonitoringScheduleSpec{
		"missing definition": {Name: "r1", Type: driver.MonitoringDataQuality, JobDefinitionName: "ghost", Config: json.RawMessage(`{}`)},
		"no type":            {Name: "r2", JobDefinitionName: "dq", Config: json.RawMessage(`{}`)},
		"missing endpoint":   {Name: "r3", EndpointName: "ghost", Config: json.RawMessage(`{}`)},
		"no config":          {Name: "r4"},
		"bad name":           {Name: "-x", Config: json.RawMessage(`{}`)},
	} {
		_, err = m.CreateMonitoringSchedule(ctx, spec)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	ref, err := m.CreateMonitoringSchedule(ctx, driver.MonitoringScheduleSpec{
		Name: "by-def", Type: driver.MonitoringDataQuality, JobDefinitionName: "dq", Config: json.RawMessage(`{"MonitoringJobDefinitionName":"dq"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "dq", ref.JobDefinitionName)

	// state machine
	require.NoError(t, m.StopMonitoringSchedule(ctx, "sched-1"))
	require.NoError(t, m.StopMonitoringSchedule(ctx, "sched-1"), "stopping a stopped schedule is a no-op")

	got, _ := m.DescribeMonitoringSchedule(ctx, "sched-1")
	assert.Equal(t, driver.ScheduleStopped, got.Status)

	require.NoError(t, m.StartMonitoringSchedule(ctx, "sched-1"))
	require.NoError(t, m.StartMonitoringSchedule(ctx, "sched-1"))

	got, _ = m.DescribeMonitoringSchedule(ctx, "sched-1")
	assert.Equal(t, driver.ScheduleScheduled, got.Status)

	for _, op := range []func(context.Context, string) error{m.StartMonitoringSchedule, m.StopMonitoringSchedule, m.DeleteMonitoringSchedule} {
		assert.True(t, errors.IsNotFound(op(ctx, "ghost")))
	}

	require.NoError(t, m.DeleteMonitoringJobDefinition(ctx, driver.MonitoringDataQuality, "dq"), "a referenced definition can be deleted")

	list, err := m.ListMonitoringSchedules(ctx, driver.ListOptions{Filters: map[string]string{"MonitoringTypeEquals": driver.MonitoringDataQuality}})
	require.NoError(t, err)
	assert.Len(t, list.Items, 1)
}

func TestMonitoringExecutions_LazyAndDeterministic(t *testing.T) { // T86, T87
	m, fc := newTestMockClock() // 2025-01-01 00:00:00 UTC
	ctx := context.Background()

	_, err := m.CreateMonitoringSchedule(ctx, schedSpec("hourly", hourly()))
	require.NoError(t, err)
	_, err = m.CreateMonitoringSchedule(ctx, schedSpec("noon", "cron(0 12 ? * * *)"))
	require.NoError(t, err)
	_, err = m.CreateMonitoringSchedule(ctx, schedSpec("every12", "cron(0 17/12 ? * * *)"))
	require.NoError(t, err)

	now := schedSpec("once", "NOW")
	now.DataAnalysisStartTime, now.DataAnalysisEndTime = "-PT1H", "PT0S"
	_, err = m.CreateMonitoringSchedule(ctx, now)
	require.NoError(t, err)

	assert.Equal(t, []string{"00:00"}, execTimes(t, m, "once"), "NOW runs exactly once, at creation")
	assert.Empty(t, execTimes(t, m, "hourly"), "nothing before the first top of the hour")

	fc.Advance(5 * time.Hour)

	assert.Equal(t, []string{"01:00", "02:00", "03:00", "04:00", "05:00"}, execTimes(t, m, "hourly"))
	assert.Equal(t, execTimes(t, m, "hourly"), execTimes(t, m, "hourly"), "repeated reads are identical")
	assert.Empty(t, execTimes(t, m, "noon"))
	assert.Equal(t, []string{"00:00"}, execTimes(t, m, "once"), "NOW does not repeat")

	fc.Advance(20 * time.Hour) // 01:00 next day

	assert.Equal(t, []string{"12:00"}, execTimes(t, m, "noon"))
	assert.Equal(t, []string{"17:00"}, execTimes(t, m, "every12"), "17/12 fires at 17 only (the next, 05, is tomorrow)")

	// stop: no new executions, existing ones kept
	require.NoError(t, m.StopMonitoringSchedule(ctx, "hourly"))

	kept := execTimes(t, m, "hourly")
	fc.Advance(6 * time.Hour)
	assert.Equal(t, kept, execTimes(t, m, "hourly"), "a stopped schedule runs nothing and keeps its history")

	// start re-anchors at now
	require.NoError(t, m.StartMonitoringSchedule(ctx, "hourly"))
	fc.Advance(2 * time.Hour)
	assert.Len(t, execTimes(t, m, "hourly"), len(kept)+2)

	d, _ := m.DescribeMonitoringSchedule(ctx, "hourly")
	require.NotNil(t, d.LastExecution, "Describe summarises the last execution")
	assert.Equal(t, "hourly", d.LastExecution.ScheduleName)
	assert.Equal(t, driver.MonitorExecCompleted, d.LastExecution.Status)

	// delete keeps the history
	before := execTimes(t, m, "noon")
	require.NoError(t, m.DeleteMonitoringSchedule(ctx, "noon"))
	assert.Equal(t, before, execTimes(t, m, "noon"), "deleting a schedule does not delete its execution history")

	page, err := m.ListMonitoringExecutions(ctx, driver.ListOptions{MaxResults: 2})
	require.NoError(t, err)
	assert.Len(t, page.Items, 2)
	assert.NotEmpty(t, page.NextToken)
}

func TestMonitoringExecutions_UpdateAndCap(t *testing.T) { // T86
	m, fc := newTestMockClock()
	ctx := context.Background()

	_, err := m.CreateMonitoringSchedule(ctx, schedSpec("s", hourly()))
	require.NoError(t, err)

	fc.Advance(3 * time.Hour)
	assert.Len(t, execTimes(t, m, "s"), 3)

	_, err = m.UpdateMonitoringSchedule(ctx, schedSpec("s", "cron(0 12 ? * * *)"))
	require.NoError(t, err)
	assert.Len(t, execTimes(t, m, "s"), 3, "executions that already ran are kept")

	fc.Advance(24 * time.Hour)
	assert.Len(t, execTimes(t, m, "s"), 4, "the new cadence applies from the update: one noon run")

	_, err = m.UpdateMonitoringSchedule(ctx, schedSpec("ghost", hourly()))
	assert.True(t, errors.IsNotFound(err))

	// a huge clock jump is bounded
	_, err = m.CreateMonitoringSchedule(ctx, schedSpec("long", hourly()))
	require.NoError(t, err)

	fc.Advance(24 * 365 * time.Hour)

	page, err := m.ListMonitoringExecutions(ctx, driver.ListOptions{Filters: map[string]string{"MonitoringScheduleName": "long"}, MaxResults: driver.MaxListResults})
	require.NoError(t, err)
	assert.Equal(t, driver.MaxListResults, len(page.Items))

	var n int

	for token := ""; ; {
		p, perr := m.ListMonitoringExecutions(ctx, driver.ListOptions{Filters: map[string]string{"MonitoringScheduleName": "long"}, MaxResults: driver.MaxListResults, NextToken: token})
		require.NoError(t, perr)

		n += len(p.Items)

		if p.NextToken == "" {
			break
		}

		token = p.NextToken
	}

	assert.Equal(t, driver.MaxMonitoringExecutionsPerSchedule, n, "at most 1000 executions are produced per schedule")
}

func TestMonitoringAlerts(t *testing.T) { // T88
	m, fc := newTestMockClock()
	ctx := context.Background()

	_, err := m.CreateMonitoringSchedule(ctx, schedSpec("s", hourly()))
	require.NoError(t, err)

	_, err = m.ListMonitoringAlerts(ctx, "ghost", driver.ListOptions{})
	assert.True(t, errors.IsNotFound(err))

	require.NoError(t, m.PutMonitoringAlert("s", "alert-1", 2, 3))
	assert.True(t, errors.IsNotFound(m.PutMonitoringAlert("ghost", "a", 1, 1)))
	assert.True(t, errors.IsInvalidArgument(m.PutMonitoringAlert("s", "a2", 0, 3)))

	alerts, err := m.ListMonitoringAlerts(ctx, "s", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, alerts.Items, 1)
	assert.Equal(t, driver.AlertOK, alerts.Items[0].Status)

	fc.Advance(6 * time.Hour)

	hist, err := m.ListMonitoringAlertHistory(ctx, driver.ListOptions{Filters: map[string]string{"MonitoringScheduleName": "s"}})
	require.NoError(t, err)
	assert.Empty(t, hist.Items, "all executions Completed: no alert history")

	times := execTimes(t, m, "s")
	require.Len(t, times, 6)

	runs, _ := m.ListMonitoringExecutions(ctx, driver.ListOptions{Filters: map[string]string{"MonitoringScheduleName": "s"}, SortBy: "ScheduledTime", SortOrder: driver.SortAscending})

	for _, i := range []int{2, 3} {
		require.NoError(t, m.SetMonitoringExecutionStatus("s", runs.Items[i].ScheduledTime, driver.MonitorExecCompletedViolations, ""))
	}

	alerts, _ = m.ListMonitoringAlerts(ctx, "s", driver.ListOptions{})
	assert.Equal(t, driver.AlertOK, alerts.Items[0].Status, "after the window slides past the violations the alert recovers")

	hist, err = m.ListMonitoringAlertHistory(ctx, driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, hist.Items, 2, "one InAlert and one OK transition")
	assert.Equal(t, driver.AlertOK, hist.Items[0].Status, "newest first")
	assert.Equal(t, driver.AlertInAlert, hist.Items[1].Status)

	inAlert, err := m.ListMonitoringAlertHistory(ctx, driver.ListOptions{StatusEquals: driver.AlertInAlert})
	require.NoError(t, err)
	assert.Len(t, inAlert.Items, 1)

	s, err := m.UpdateMonitoringAlert(ctx, "s", "alert-1", 1, 1)
	require.NoError(t, err)
	assert.Equal(t, "s", s.Name)

	for _, b := range [][2]int{{0, 1}, {1, 0}, {101, 1}, {1, 101}} {
		_, err = m.UpdateMonitoringAlert(ctx, "s", "alert-1", b[0], b[1])
		assert.True(t, errors.IsInvalidArgument(err), b)
	}

	_, err = m.UpdateMonitoringAlert(ctx, "s", "ghost", 1, 1)
	assert.True(t, errors.IsNotFound(err))

	_, err = m.UpdateMonitoringAlert(ctx, "ghost", "alert-1", 1, 1)
	assert.True(t, errors.IsNotFound(err))
}

func TestMonitoring_ConcurrentCreateAndSnapshot(t *testing.T) { // T89
	m, fc := newTestMockClock()
	ctx := context.Background()

	assert.Equal(t, 1, raceOnce(func() error {
		_, err := m.CreateMonitoringSchedule(ctx, schedSpec("same", hourly()))
		return err
	}))
	assert.Equal(t, 1, raceOnce(func() error {
		_, err := m.CreateMonitoringJobDefinition(ctx, defSpec(driver.MonitoringModelBias, "same"))
		return err
	}))

	fc.Advance(3 * time.Hour)
	require.NoError(t, m.StopMonitoringSchedule(ctx, "same"))
	require.NoError(t, m.PutMonitoringAlert("same", "a", 1, 1))

	raw, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, dstClock := newTestMockClock()
	dstClock.Set(fc.Now())
	require.NoError(t, dst.Restore(ctx, raw))

	assert.Equal(t, execTimes(t, m, "same"), execTimes(t, dst, "same"), "executions survive a snapshot")

	got, err := dst.DescribeMonitoringSchedule(ctx, "same")
	require.NoError(t, err)
	assert.Equal(t, driver.ScheduleStopped, got.Status)

	alerts, err := dst.ListMonitoringAlerts(ctx, "same", driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, alerts.Items, 1)

	_, err = dst.DescribeMonitoringJobDefinition(ctx, driver.MonitoringModelBias, "same")
	require.NoError(t, err)
}

func searchNames(t *testing.T, m *Mock, req driver.SearchRequest) []string {
	t.Helper()

	res, err := m.Search(context.Background(), req)
	require.NoError(t, err)

	out := make([]string, 0, len(res.Hits))

	for _, h := range res.Hits {
		for _, key := range []string{"TrainingJobName", "EndpointName", "ExperimentName", "TrialName", "TrialComponentName", "ProjectName", "PipelineName", "ModelPackageGroupName", "FeatureGroupName", "HyperParameterTuningJobName"} {
			if v, ok := h.Record[key].(string); ok {
				out = append(out, v)

				break
			}
		}
	}

	return out
}

func seedSearchJobs(t *testing.T, m *Mock, fc *config.FakeClock) {
	t.Helper()

	ctx := context.Background()

	for i, c := range []struct {
		name, eta string
		tags      []driver.Tag
	}{
		{"alpha", "0.1", []driver.Tag{{Key: "team", Value: "ml"}}},
		{"beta", "0.3", []driver.Tag{{Key: "team", Value: "data"}}},
		{"gamma", "0.25", []driver.Tag{{Key: "team", Value: "ml"}, {Key: "env", Value: "prod"}}},
	} {
		_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{
			JobName: c.name, OutputS3URI: "s3://b", HyperParameters: map[string]string{"eta": c.eta, "algo": "xgb"},
			InputChannels: []driver.Channel{{Name: "train", S3URI: "s3://t", ContentType: "text/csv"}, {Name: "val", S3URI: "s3://v", ContentType: "text/json"}},
			Tags:          c.tags,
		})
		require.NoError(t, err, i)
		fc.Advance(time.Minute)
	}
}

func TestSearch_OperatorsAndProperties(t *testing.T) { // T114
	m, fc := newTestMockClock()
	seedSearchJobs(t, m, fc)

	search := func(f driver.SearchFilter) []string {
		return searchNames(t, m, driver.SearchRequest{
			Resource: driver.SearchTrainingJob, SortBy: "TrainingJobName", SortOrder: driver.SortAscending,
			Expression: &driver.SearchExpression{Filters: []driver.SearchFilter{f}},
		})
	}

	assert.Equal(t, []string{"alpha"}, search(driver.SearchFilter{Name: "TrainingJobName", Value: "alpha"}), "Equals is the default operator")
	assert.Equal(t, []string{"beta", "gamma"}, search(driver.SearchFilter{Name: "TrainingJobName", Operator: "NotEquals", Value: "alpha"}))
	assert.Equal(t, []string{"alpha", "gamma"}, search(driver.SearchFilter{Name: "Tags.team", Value: "ml"}))
	assert.Equal(t, []string{"gamma"}, search(driver.SearchFilter{Name: "Tags.env", Operator: "Exists"}))
	assert.Equal(t, []string{"alpha", "beta"}, search(driver.SearchFilter{Name: "Tags.env", Operator: "NotExists"}))
	assert.Equal(t, []string{"alpha", "gamma"}, search(driver.SearchFilter{Name: "TrainingJobName", Operator: "In", Value: "alpha, gamma ,ghost"}))
	assert.Equal(t, []string{"gamma"}, search(driver.SearchFilter{Name: "TrainingJobName", Operator: "Contains", Value: "mm"}))
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, search(driver.SearchFilter{Name: "TrainingJobName", Operator: "Contains", Value: "a"}))
	assert.Equal(t, []string{"beta", "gamma"}, search(driver.SearchFilter{Name: "HyperParameters.eta", Operator: "GreaterThan", Value: "0.2"}), "hyper-parameters compare numerically")
	assert.Equal(t, []string{"alpha"}, search(driver.SearchFilter{Name: "HyperParameters.eta", Operator: "LessThanOrEqualTo", Value: "0.1"}))
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, search(driver.SearchFilter{Name: "HyperParameters.algo", Value: "xgb"}))
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, search(driver.SearchFilter{Name: "TrainingJobStatus", Value: driver.JobCompleted}))

	cutoff := time.Date(2025, 1, 1, 0, 1, 30, 0, time.UTC).Format("2006-01-02T15:04:05")
	assert.Equal(t, []string{"gamma"}, search(driver.SearchFilter{Name: "CreationTime", Operator: "GreaterThan", Value: cutoff}), "timestamps compare as times")
	assert.Equal(t, []string{"alpha", "beta"}, search(driver.SearchFilter{Name: "CreationTime", Operator: "LessThan", Value: cutoff}))

	for name, f := range map[string]driver.SearchFilter{
		"ordering on text": {Name: "TrainingJobName", Operator: "GreaterThan", Value: "a"},
		"contains on num":  {Name: "HyperParameters.eta", Operator: "Contains", Value: "0"},
		"in on time":       {Name: "CreationTime", Operator: "In", Value: "x"},
		"not a number":     {Name: "HyperParameters.eta", Operator: "GreaterThan", Value: "big"},
		"not a timestamp":  {Name: "CreationTime", Operator: "GreaterThan", Value: "yesterday"},
		"unknown property": {Name: "Nonsense", Value: "x"},
		"bad operator":     {Name: "TrainingJobName", Operator: "Like", Value: "x"},
		"no value":         {Name: "TrainingJobName"},
		"empty name":       {Value: "x"},
	} {
		_, err := m.Search(context.Background(), driver.SearchRequest{Resource: driver.SearchTrainingJob, Expression: &driver.SearchExpression{Filters: []driver.SearchFilter{f}}})
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}
}

func TestSearch_BooleanLogicNestedAndLimits(t *testing.T) { // T114, T115
	m, fc := newTestMockClock()
	seedSearchJobs(t, m, fc)
	ctx := context.Background()

	req := func(e driver.SearchExpression) driver.SearchRequest {
		return driver.SearchRequest{Resource: driver.SearchTrainingJob, SortBy: "TrainingJobName", SortOrder: driver.SortAscending, Expression: &e}
	}

	ml := driver.SearchFilter{Name: "Tags.team", Value: "ml"}
	prod := driver.SearchFilter{Name: "Tags.env", Value: "prod"}
	alpha := driver.SearchFilter{Name: "TrainingJobName", Value: "alpha"}

	assert.Equal(t, []string{"gamma"}, searchNames(t, m, req(driver.SearchExpression{Filters: []driver.SearchFilter{ml, prod}})), "And by default")
	assert.Equal(t, []string{"alpha", "gamma"}, searchNames(t, m, req(driver.SearchExpression{Filters: []driver.SearchFilter{prod, alpha}, Operator: "Or"})))
	assert.Equal(t, []string{"alpha"}, searchNames(t, m, req(driver.SearchExpression{
		Filters:        []driver.SearchFilter{ml},
		SubExpressions: []driver.SearchExpression{{Filters: []driver.SearchFilter{alpha, prod}, Operator: "Or"}, {Filters: []driver.SearchFilter{{Name: "Tags.env", Operator: "NotExists"}}}},
	})), "sub-expressions combine with the outer And")

	assert.Equal(t, []string{"alpha", "beta", "gamma"}, searchNames(t, m, req(driver.SearchExpression{NestedFilters: []driver.SearchNested{{
		PropertyName: "InputDataConfig", Filters: []driver.SearchFilter{{Name: "InputDataConfig.ChannelName", Value: "val"}, {Name: "InputDataConfig.ContentType", Value: "text/json"}},
	}}})), "one channel satisfies both filters")
	assert.Empty(t, searchNames(t, m, req(driver.SearchExpression{NestedFilters: []driver.SearchNested{{
		PropertyName: "InputDataConfig", Filters: []driver.SearchFilter{{Name: "InputDataConfig.ChannelName", Value: "val"}, {Name: "InputDataConfig.ContentType", Value: "text/csv"}},
	}}})), "the filters must match the same item")
	assert.Equal(t, []string{"gamma"}, searchNames(t, m, req(driver.SearchExpression{NestedFilters: []driver.SearchNested{{
		PropertyName: "Tags", Filters: []driver.SearchFilter{{Name: "Tags.Key", Value: "env"}, {Name: "Tags.Value", Value: "prod"}},
	}}})))

	// limits
	filters := func(n int) []driver.SearchFilter {
		out := make([]driver.SearchFilter, n)
		for i := range out {
			out[i] = alpha
		}

		return out
	}

	_, err := m.Search(ctx, req(driver.SearchExpression{Filters: filters(20)}))
	require.NoError(t, err, "20 filters is allowed")

	for name, e := range map[string]driver.SearchExpression{
		"21 filters": {Filters: filters(21)},
		"empty":      {},
		"bad op":     {Filters: filters(1), Operator: "Xor"},
		"51 total":   {Filters: filters(20), SubExpressions: []driver.SearchExpression{{Filters: filters(20)}, {Filters: filters(20)}}},
		"empty nest": {NestedFilters: []driver.SearchNested{{PropertyName: "Tags"}}},
	} {
		_, err = m.Search(ctx, req(e))
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), name)
	}

	// CONTAINS is limited to one use, except for the documented properties
	two := driver.SearchExpression{Filters: []driver.SearchFilter{
		{Name: "TrainingJobName", Operator: "Contains", Value: "a"}, {Name: "RoleArn", Operator: "Contains", Value: "r"},
	}}
	_, err = m.Search(ctx, req(two))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'CONTAINS' operator usage limit of 1 exceeded.")

	_, err = m.CreateExperiment(ctx, driver.ExperimentSpec{ExperimentName: "exp-a"})
	require.NoError(t, err)

	exempt := driver.SearchExpression{Filters: []driver.SearchFilter{
		{Name: "ExperimentName", Operator: "Contains", Value: "exp"}, {Name: "DisplayName", Operator: "Contains", Value: "x"}, {Name: "Tags.k", Operator: "Contains", Value: "v"},
	}}
	_, err = m.Search(ctx, driver.SearchRequest{Resource: driver.SearchExperiment, Expression: &exempt})
	require.NoError(t, err, "Experiment name, display name and tags may use CONTAINS repeatedly")

	_, err = m.Search(ctx, driver.SearchRequest{Resource: "Spaceship"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestSearch_SortPageAndResources(t *testing.T) { // T116, T117
	m, fc := newTestMockClock()
	seedSearchJobs(t, m, fc)
	ctx := context.Background()

	assert.Equal(t, []string{"gamma", "beta", "alpha"}, searchNames(t, m, driver.SearchRequest{Resource: driver.SearchTrainingJob}), "default: LastModifiedTime descending")
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, searchNames(t, m, driver.SearchRequest{Resource: driver.SearchTrainingJob, SortOrder: driver.SortAscending}))
	assert.Equal(t, []string{"beta", "gamma", "alpha"}, searchNames(t, m, driver.SearchRequest{Resource: driver.SearchTrainingJob, SortBy: "HyperParameters.eta"}), "sorted numerically, descending")

	_, err := m.Search(ctx, driver.SearchRequest{Resource: driver.SearchTrainingJob, SortBy: "Nope"})
	assert.True(t, errors.IsInvalidArgument(err))

	for _, bad := range []int{-1, 101} {
		_, err = m.Search(ctx, driver.SearchRequest{Resource: driver.SearchTrainingJob, MaxResults: bad})
		assert.True(t, errors.IsInvalidArgument(err), bad)
	}

	_, err = m.Search(ctx, driver.SearchRequest{Resource: driver.SearchTrainingJob, NextToken: "garbage"})
	assert.True(t, errors.IsInvalidArgument(err))

	var (
		seen  []string
		token string
	)

	for {
		res, serr := m.Search(ctx, driver.SearchRequest{Resource: driver.SearchTrainingJob, MaxResults: 2, NextToken: token, SortOrder: driver.SortAscending})
		require.NoError(t, serr)
		assert.Equal(t, 3, res.TotalHits)

		for _, h := range res.Hits {
			seen = append(seen, h.Record["TrainingJobName"].(string))
		}

		if res.NextToken == "" {
			break
		}

		token = res.NextToken
	}

	assert.Equal(t, []string{"alpha", "beta", "gamma"}, seen)

	// every backed resource type is searchable and shapes its record
	_, _ = m.CreateModel(ctx, driver.ModelConfig{ModelName: "model-1", Tags: []driver.Tag{{Key: "t", Value: "v"}}})
	cfg := seedConfig(t, m, "s", 1, 1)
	_, _ = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep-1", ConfigName: cfg})
	_, _ = m.CreateExperiment(ctx, driver.ExperimentSpec{ExperimentName: "exp-1"})
	_, _ = m.CreateTrial(ctx, driver.TrialSpec{TrialName: "trial-1", ExperimentName: "exp-1"})
	_, _ = m.CreateTrialComponent(ctx, driver.TrialComponentSpec{Name: "tc-1", Parameters: map[string]driver.TrialComponentParameter{"lr": {NumberValue: ptr(0.5)}}, Status: &driver.TrialComponentStatus{PrimaryStatus: "Completed"}})
	_, _ = m.CreateModelPackageGroup(ctx, driver.ModelPackageGroupSpec{GroupName: "grp"})
	_, _ = m.CreateModelPackage(ctx, driver.ModelPackageSpec{GroupName: "grp"})
	_, _ = m.CreatePipeline(ctx, driver.PipelineSpec{PipelineName: "pipe-1"})
	_, _ = m.CreateFeatureGroup(ctx, driver.FeatureGroupSpec{GroupName: "fg-1", RecordIdentifierName: "id", EventTimeFeatureName: "ts"})
	_, _ = m.CreateProject(ctx, scSpec("proj-1"))
	_, _ = m.CreateHyperParameterTuningJob(ctx, driver.HyperParameterTuningJobConfig{JobName: "hpo-1"})

	for resource, want := range map[string]int{
		driver.SearchModel: 2, driver.SearchEndpoint: 1, driver.SearchExperiment: 1, driver.SearchExperimentTrial: 1,
		driver.SearchExperimentTrialComponent: 1, driver.SearchModelPackageGroup: 1, driver.SearchModelPackage: 1,
		driver.SearchPipeline: 1, driver.SearchFeatureGroup: 1, driver.SearchProject: 1, driver.SearchHyperParameterTuningJob: 1,
	} {
		res, serr := m.Search(ctx, driver.SearchRequest{Resource: resource})
		require.NoError(t, serr, resource)
		assert.Equal(t, want, res.TotalHits, resource)
	}

	assert.Equal(t, []string{"tc-1"}, searchNames(t, m, driver.SearchRequest{Resource: driver.SearchExperimentTrialComponent, Expression: &driver.SearchExpression{
		Filters: []driver.SearchFilter{{Name: "Parameters.lr", Operator: "GreaterThan", Value: "0.1"}, {Name: "Status.PrimaryStatus", Value: "Completed"}},
	}}))

	// resource types that are valid but have no data search as empty
	for _, r := range []string{driver.SearchFeatureMetadata, driver.SearchModelCard, driver.SearchPipelineVersion, driver.SearchJob, driver.SearchImage, driver.SearchImageVersion} {
		res, serr := m.Search(ctx, driver.SearchRequest{Resource: r})
		require.NoError(t, serr, r)
		assert.Zero(t, res.TotalHits, r)
		assert.NotNil(t, res.Hits)
	}
}

func TestGetSearchSuggestions(t *testing.T) { // T118
	m, fc := newTestMockClock()
	seedSearchJobs(t, m, fc)
	ctx := context.Background()

	all, err := m.GetSearchSuggestions(ctx, driver.SearchTrainingJob, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"HyperParameters.algo", "HyperParameters.eta", "Tags.env", "Tags.team"}, all, "deduped, sorted")

	got, err := m.GetSearchSuggestions(ctx, driver.SearchTrainingJob, "tags.")
	require.NoError(t, err)
	assert.Equal(t, []string{"Tags.env", "Tags.team"}, got, "prefix match, case-insensitive")

	got, err = m.GetSearchSuggestions(ctx, driver.SearchTrainingJob, "HyperParameters.e")
	require.NoError(t, err)
	assert.Equal(t, []string{"HyperParameters.eta"}, got)

	_, err = m.GetSearchSuggestions(ctx, driver.SearchTrainingJob, strings.Repeat("h", 101))
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.GetSearchSuggestions(ctx, "Spaceship", "")
	assert.True(t, errors.IsInvalidArgument(err))

	none, err := m.GetSearchSuggestions(ctx, driver.SearchModelCard, "")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestSearch_ConcurrentWithCreates(t *testing.T) { // T119
	m := newTestMock()
	ctx := context.Background()

	var wg sync.WaitGroup

	for i := 0; i < 30; i++ {
		wg.Add(2)

		go func(i int) {
			defer wg.Done()

			_, _ = m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "job-" + strconv.Itoa(i), Tags: []driver.Tag{{Key: "k", Value: "v"}}})
		}(i)
		go func() {
			defer wg.Done()

			_, _ = m.Search(ctx, driver.SearchRequest{Resource: driver.SearchTrainingJob, Expression: &driver.SearchExpression{Filters: []driver.SearchFilter{{Name: "Tags.k", Value: "v"}}}})
			_, _ = m.GetSearchSuggestions(ctx, driver.SearchTrainingJob, "")
		}()
	}

	wg.Wait()

	res, err := m.Search(ctx, driver.SearchRequest{Resource: driver.SearchTrainingJob, MaxResults: 100})
	require.NoError(t, err)
	assert.Equal(t, 30, res.TotalHits)
}

// scribble mutates every map, slice and pointer reachable from v in place, the
// way a careless caller could. A Describe that returns shared storage would
// show the damage on the next read.
func scribble(v reflect.Value) {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			scribble(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				scribble(v.Field(i))
			}
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			v.SetMapIndex(k, reflect.Value{})
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			scribble(v.Index(i))
		}

		if v.Type().Elem().Kind() == reflect.Uint8 {
			for i := 0; i < v.Len(); i++ {
				v.Index(i).SetUint('!')
			}
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString("mutated")
		}
	default:
	}
}

func TestDeepCopy_AllDescribes(t *testing.T) { // T17
	m, fc := newTestMockClock()
	ctx := context.Background()
	tags := []driver.Tag{{Key: "k", Value: "v"}}

	_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "mdl", Containers: []driver.ContainerDefinition{{Image: "i", Environment: map[string]string{"A": "b"}}}, Tags: tags})
	require.NoError(t, err)
	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{
		ConfigName: "cfg", Tags: tags,
		ProductionVariants: []driver.ProductionVariant{{VariantName: "v", ModelName: "mdl", InstanceType: "ml.m5.large", InitialInstanceCount: 1, InitialVariantWeight: ptr(1.0)}},
	})
	require.NoError(t, err)
	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: "cfg", Tags: tags})
	require.NoError(t, err)
	_, err = m.CreateTrainingJob(ctx, driver.TrainingJobConfig{
		JobName: "tj", HyperParameters: map[string]string{"a": "b"}, InputChannels: []driver.Channel{{Name: "c"}}, Tags: tags,
		Echo: map[string]json.RawMessage{"X": json.RawMessage(`{"a":1}`)},
	})
	require.NoError(t, err)
	_, err = m.CreateProcessingJob(ctx, driver.ProcessingJobConfig{JobName: "pj", Tags: tags})
	require.NoError(t, err)
	_, err = m.CreateTransformJob(ctx, driver.TransformJobConfig{JobName: "xf", ModelName: "mdl", Tags: tags})
	require.NoError(t, err)
	_, err = m.CreateHyperParameterTuningJob(ctx, driver.HyperParameterTuningJobConfig{JobName: "hp", Tags: tags})
	require.NoError(t, err)
	_, err = m.CreateProject(ctx, scSpec("proj"))
	require.NoError(t, err)
	_, err = m.CreateWorkforce(ctx, cognitoWorkforce("default"))
	require.NoError(t, err)
	_, err = m.CreateWorkteam(ctx, driver.WorkteamSpec{Name: "wt", Description: "d", Members: []driver.MemberDefinition{cognitoMember("g")}, Tags: tags})
	require.NoError(t, err)
	_, err = m.CreateImage(ctx, driver.ImageSpec{Name: "img", RoleARN: testRole, Tags: tags})
	require.NoError(t, err)
	_, err = m.CreateImageVersion(ctx, versionSpec("img", "tok", "stable"))
	require.NoError(t, err)
	_, err = m.CreateAppImageConfig(ctx, driver.AppImageConfigSpec{Name: "cfg-1", Tags: tags})
	require.NoError(t, err)
	_, err = m.CreateExperiment(ctx, driver.ExperimentSpec{ExperimentName: "exp", Tags: tags})
	require.NoError(t, err)
	_, err = m.CreateTrial(ctx, driver.TrialSpec{TrialName: "tr", ExperimentName: "exp", Tags: tags})
	require.NoError(t, err)
	_, err = m.CreateTrialComponent(ctx, driver.TrialComponentSpec{Name: "run", Tags: tags})
	require.NoError(t, err)
	art, err := m.CreateArtifact(ctx, artifactSpec("data", "s3://b/d"))
	require.NoError(t, err)
	_, err = m.CreateAction(ctx, driver.LineageEntitySpec{Name: "act", Type: "t", Source: driver.LineageSource{SourceURI: "u"}, Properties: map[string]string{"a": "b"}})
	require.NoError(t, err)
	_, err = m.CreateContext(ctx, driver.LineageEntitySpec{Name: "ctx", Type: "t", Source: driver.LineageSource{SourceURI: "u"}})
	require.NoError(t, err)
	_, err = m.CreateMonitoringJobDefinition(ctx, defSpec(driver.MonitoringKinds[0], "def"))
	require.NoError(t, err)
	_, err = m.CreateMonitoringSchedule(ctx, schedSpec("sched", hourly()))
	require.NoError(t, err)
	fc.Advance(2 * time.Hour)

	_, err = m.CreateModelPackageGroup(ctx, driver.ModelPackageGroupSpec{GroupName: "grp", Tags: tags})
	require.NoError(t, err)
	pkg, err := m.CreateModelPackage(ctx, driver.ModelPackageSpec{GroupName: "grp", Tags: tags})
	require.NoError(t, err)

	describes := map[string]func() (any, error){
		"model":          func() (any, error) { return m.DescribeModel(ctx, "mdl") },
		"endpointConfig": func() (any, error) { return m.DescribeEndpointConfig(ctx, "cfg") },
		"endpoint":       func() (any, error) { return m.DescribeEndpoint(ctx, "ep") },
		"training":       func() (any, error) { return m.DescribeTrainingJob(ctx, "tj") },
		"processing":     func() (any, error) { return m.DescribeProcessingJob(ctx, "pj") },
		"transform":      func() (any, error) { return m.DescribeTransformJob(ctx, "xf") },
		"tuning":         func() (any, error) { return m.DescribeHyperParameterTuningJob(ctx, "hp") },
		"project":        func() (any, error) { return m.DescribeProject(ctx, "proj") },
		"workforce":      func() (any, error) { return m.DescribeWorkforce(ctx, "default") },
		"workteam":       func() (any, error) { return m.DescribeWorkteam(ctx, "wt") },
		"image":          func() (any, error) { return m.DescribeImage(ctx, "img") },
		"imageVersion": func() (any, error) {
			return m.DescribeImageVersion(ctx, driver.ImageVersionRef{ImageName: "img", Alias: "stable"})
		},
		"appImageConfig": func() (any, error) { return m.DescribeAppImageConfig(ctx, "cfg-1") },
		"experiment":     func() (any, error) { return m.DescribeExperiment(ctx, "exp") },
		"trial":          func() (any, error) { return m.DescribeTrial(ctx, "tr") },
		"trialComponent": func() (any, error) { return m.DescribeTrialComponent(ctx, "run") },
		"artifact":       func() (any, error) { return m.DescribeArtifact(ctx, art.ARN) },
		"action":         func() (any, error) { return m.DescribeAction(ctx, "act") },
		"context":        func() (any, error) { return m.DescribeContext(ctx, "ctx") },
		"monDef": func() (any, error) {
			return m.DescribeMonitoringJobDefinition(ctx, driver.MonitoringKinds[0], "def")
		},
		"monSchedule":  func() (any, error) { return m.DescribeMonitoringSchedule(ctx, "sched") },
		"packageGroup": func() (any, error) { return m.DescribeModelPackageGroup(ctx, "grp") },
		"package":      func() (any, error) { return m.DescribeModelPackage(ctx, pkg.PackageARN) },
	}

	for name, describe := range describes {
		before, err := describe()
		require.NoError(t, err, name)

		want, err := json.Marshal(before)
		require.NoError(t, err, name)

		scribble(reflect.ValueOf(before))

		after, err := describe()
		require.NoError(t, err, name)

		got, err := json.Marshal(after)
		require.NoError(t, err, name)
		assert.JSONEq(t, string(want), string(got), "%s: Describe must return an independent copy", name)
	}
}

// TestValidation_LivesInProvider pins that the checks sit in the provider, so a
// Go library user calling it directly gets the same typed errors the wire maps.
func TestValidation_LivesInProvider(t *testing.T) { // T25
	m := newTestMock()
	ctx := context.Background()

	_, err := m.ListTrainingJobs(ctx, driver.ListOptions{NextToken: "garbage"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err), "bad list token: %v", err)

	_, err = m.CreateNotebookInstance(ctx, driver.NotebookInstanceSpec{Name: "nb", InstanceType: "ml.t3.medium", VolumeSizeInGB: 10})
	require.NoError(t, err)
	err = m.UpdateNotebookInstance(ctx, driver.NotebookInstanceUpdate{Name: "nb", VolumeSizeInGB: ptr(5)})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err) || errors.IsFailedPrecondition(err), "volume shrink: %v", err)

	_, err = m.CreateMonitoringSchedule(ctx, schedSpec("bad-cron", "every day"))
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err), "cron: %v", err)

	_, err = m.CreateWorkforce(ctx, driver.WorkforceSpec{
		Name: "wf", Cognito: &driver.CognitoConfig{UserPool: "p", ClientID: "c"}, SourceCIDRs: []string{"not-a-cidr"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err), "SourceIp CIDR: %v", err)

	_, err = m.DescribeArtifact(ctx, "garbage")
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err), "malformed ARN: %v", err)

	for i := 0; i < driver.MaxProjects; i++ {
		_, err = m.CreateProject(ctx, scSpec("p"+strconv.Itoa(i)))
		require.NoError(t, err)
	}

	_, err = m.CreateProject(ctx, scSpec("one-too-many"))
	require.Error(t, err)
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err), "project cap: %v", err)
}

func TestAppImageConfig_FileSystemDefaults(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	c, err := m.CreateAppImageConfig(ctx, driver.AppImageConfigSpec{
		Name: "cfg", JupyterLab: &driver.AppImageConfigBody{FileSystemConfig: &driver.FileSystemConfig{MountPath: "/x"}},
	})
	require.NoError(t, err)
	require.NotNil(t, c.JupyterLab.FileSystemConfig.DefaultUID)
	assert.Equal(t, 1000, *c.JupyterLab.FileSystemConfig.DefaultUID)
	assert.Equal(t, 100, *c.JupyterLab.FileSystemConfig.DefaultGID)

	c, err = m.CreateAppImageConfig(ctx, driver.AppImageConfigSpec{
		Name: "root", JupyterLab: &driver.AppImageConfigBody{FileSystemConfig: &driver.FileSystemConfig{MountPath: "/x", DefaultUID: ptr(0)}},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, *c.JupyterLab.FileSystemConfig.DefaultUID, "an explicit 0 is kept")
}

func listNodes(t *testing.T, m *Mock, cluster string) []driver.ClusterNode {
	t.Helper()

	page, err := m.ListClusterNodes(context.Background(), cluster, driver.ListOptions{})
	require.NoError(t, err)

	return page.Items
}

func TestListClusterNodes_FiltersAndPaging(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.ClusterSpec{ClusterName: "hp", InstanceGroups: []driver.ClusterInstanceGroupSpec{
		{GroupName: "workers", InstanceType: "ml.p4d.24xlarge", InstanceCount: 3},
		{GroupName: "head", InstanceType: "ml.m5.xlarge", InstanceCount: 1},
	}})
	require.NoError(t, err)

	page, err := m.ListClusterNodes(ctx, "hp", driver.ListOptions{MaxResults: 2, SortBy: driver.SortByName})
	require.NoError(t, err)
	assert.Len(t, page.Items, 2)
	assert.NotEmpty(t, page.NextToken)

	page, err = m.ListClusterNodes(ctx, "hp", driver.ListOptions{Filters: map[string]string{"InstanceGroupNameContains": "head"}})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "head", page.Items[0].GroupName)
}

func TestCreateEndpointConfig_Validation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "m"})
	require.NoError(t, err)

	v := func(name, model string) driver.ProductionVariant {
		return driver.ProductionVariant{VariantName: name, ModelName: model, InstanceType: "ml.m5.large", InitialInstanceCount: 1, InitialVariantWeight: ptr(1.0)}
	}

	for name, variants := range map[string][]driver.ProductionVariant{
		"no variants":      nil,
		"missing model":    {v("a", "ghost")},
		"duplicate name":   {v("a", "m"), v("a", "m")},
		"negative count":   {{VariantName: "a", ModelName: "m", InitialInstanceCount: -1}},
		"negative weight":  {{VariantName: "a", ModelName: "m", InitialVariantWeight: ptr(-1.0)}},
		"too many":         {v("1", "m"), v("2", "m"), v("3", "m"), v("4", "m"), v("5", "m"), v("6", "m"), v("7", "m"), v("8", "m"), v("9", "m"), v("10", "m"), v("11", "m")},
		"unnamed":          {{ModelName: "m"}},
		"huge count":       {{VariantName: "a", ModelName: "m", InitialInstanceCount: 100000}},
		"missing model #2": {v("a", "m"), v("b", "ghost")},
	} {
		_, err := m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{ConfigName: "c", ProductionVariants: variants})
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), "%s: %v", name, err)
	}

	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{ConfigName: "ok", ProductionVariants: []driver.ProductionVariant{v("a", "m")}})
	require.NoError(t, err)

	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{
		ConfigName: "ic", ProductionVariants: []driver.ProductionVariant{{VariantName: "a", InstanceType: "ml.m5.large"}},
	})
	require.NoError(t, err, "an inference-component variant names no model")
}

func TestCreateInferenceComponent_References(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "m"})
	require.NoError(t, err)
	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{
		ConfigName: "c", ProductionVariants: []driver.ProductionVariant{{VariantName: "v", ModelName: "m", InitialVariantWeight: ptr(1.0)}},
	})
	require.NoError(t, err)
	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "e", ConfigName: "c"})
	require.NoError(t, err)

	_, err = m.CreateInferenceComponent(ctx, driver.InferenceComponentSpec{Name: "ic", EndpointName: "e", ModelName: "ghost", VariantName: "v"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.CreateInferenceComponent(ctx, driver.InferenceComponentSpec{Name: "ic", EndpointName: "e", ModelName: "m", VariantName: "nope"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.CreateInferenceComponent(ctx, driver.InferenceComponentSpec{Name: "ic", EndpointName: "e", ModelName: "m", VariantName: "v"})
	require.NoError(t, err)
}

func TestInvokeEndpointAsync_UsesConfiguredOutputPath(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "m"})
	require.NoError(t, err)

	variants := []driver.ProductionVariant{{VariantName: "v", ModelName: "m", InitialVariantWeight: ptr(1.0)}}
	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{ConfigName: "sync", ProductionVariants: variants})
	require.NoError(t, err)
	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{ConfigName: "async", ProductionVariants: variants, AsyncOutputS3URI: "s3://out/results/"})
	require.NoError(t, err)
	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "sync-ep", ConfigName: "sync"})
	require.NoError(t, err)
	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "async-ep", ConfigName: "async"})
	require.NoError(t, err)

	_, err = m.InvokeEndpointAsync(ctx, driver.InvokeEndpointAsyncInput{EndpointName: "sync-ep", InputS3URI: "s3://in/x"})
	require.Error(t, err, "an endpoint without async configuration cannot be invoked asynchronously")
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.InvokeEndpointAsync(ctx, driver.InvokeEndpointAsyncInput{EndpointName: "async-ep"})
	require.Error(t, err, "InputLocation is required")

	out, err := m.InvokeEndpointAsync(ctx, driver.InvokeEndpointAsyncInput{EndpointName: "async-ep", InputS3URI: "s3://in/x"})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out.OutputS3URI, "s3://out/results/"+out.InferenceID), out.OutputS3URI)

	// A caller-supplied InferenceId is kept (AWS only generates one when none is given).
	out, err = m.InvokeEndpointAsync(ctx, driver.InvokeEndpointAsyncInput{EndpointName: "async-ep", InputS3URI: "s3://in/x", InferenceID: "myid-1"})
	require.NoError(t, err)
	assert.Equal(t, "myid-1", out.InferenceID)
	assert.Equal(t, "s3://out/results/myid-1.out", out.OutputS3URI)

	_, err = m.InvokeEndpointAsync(ctx, driver.InvokeEndpointAsyncInput{EndpointName: "async-ep", InputS3URI: "s3://in/x", InferenceID: strings.Repeat("a", 65)})
	require.Error(t, err, "InferenceId is 1-64 characters")
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestJobInstanceCount_Bounded(t *testing.T) {
	r := newMetricsRig()

	for name, create := range map[string]func(n int) error{
		"training": func(n int) error {
			_, err := r.m.CreateTrainingJob(r.ctx, driver.TrainingJobConfig{JobName: "t", Resources: driver.ResourceConfig{InstanceCount: n}})
			return err
		},
		"processing": func(n int) error {
			_, err := r.m.CreateProcessingJob(r.ctx, driver.ProcessingJobConfig{JobName: "p", Resources: driver.ResourceConfig{InstanceCount: n}})
			return err
		},
		"transform": func(n int) error {
			_, err := r.m.CreateTransformJob(r.ctx, driver.TransformJobConfig{JobName: "x", ModelName: "m", InstanceCount: n})
			return err
		},
	} {
		for _, n := range []int{-1, driver.MaxJobInstances + 1, 3_000_000} {
			err := create(n)
			require.Error(t, err, name)
			assert.True(t, errors.IsInvalidArgument(err), "%s %d: %v", name, n, err)
		}

		require.NoError(t, create(driver.MaxJobInstances), name)
	}
}

func TestCreate_TagLimitEnforced(t *testing.T) {
	m := newTestMock()

	tags := make([]driver.Tag, 51)
	for i := range tags {
		tags[i] = driver.Tag{Key: "k" + strconv.Itoa(i), Value: "v"}
	}

	_, err := m.CreateModel(context.Background(), driver.ModelConfig{ModelName: "m", Tags: tags})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.CreateTrainingJob(context.Background(), driver.TrainingJobConfig{JobName: "j", Tags: tags})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestDeleteWithDependents(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateDomain(ctx, driver.DomainSpec{DomainName: "d"})
	require.NoError(t, err)

	doms, err := m.ListDomains(ctx, driver.ListOptions{})
	require.NoError(t, err)
	id := doms.Items[0].DomainID

	_, err = m.CreateUserProfile(ctx, driver.UserProfileSpec{DomainID: id, UserProfileName: "u"})
	require.NoError(t, err)
	require.Error(t, m.DeleteDomain(ctx, id), "a domain with a user profile is in use")
	require.NoError(t, m.DeleteUserProfile(ctx, id, "u"))
	require.NoError(t, m.DeleteDomain(ctx, id))

	_, err = m.CreatePipeline(ctx, driver.PipelineSpec{PipelineName: "p"})
	require.NoError(t, err)
	ex, err := m.StartPipelineExecution(ctx, "p")
	require.NoError(t, err)
	require.NoError(t, m.DeletePipeline(ctx, "p"))
	_, err = m.DescribePipelineExecution(ctx, ex.ExecutionARN)
	require.Error(t, err, "deleting a pipeline deletes its executions")

	_, err = m.CreateFeatureGroup(ctx, driver.FeatureGroupSpec{GroupName: "fg", RecordIdentifierName: "id", EventTimeFeatureName: "t",
		Features: []driver.FeatureDefinition{{Name: "id", Type: "String"}, {Name: "t", Type: "String"}}})
	require.NoError(t, err)
	require.NoError(t, m.PutRecord(ctx, "fg", []driver.FeatureValue{{Name: "id", Value: "1"}, {Name: "t", Value: "2026-01-01T00:00:00Z"}}))
	require.NoError(t, m.DeleteFeatureGroup(ctx, "fg"))
	_, err = m.CreateFeatureGroup(ctx, driver.FeatureGroupSpec{GroupName: "fg", RecordIdentifierName: "id", EventTimeFeatureName: "t",
		Features: []driver.FeatureDefinition{{Name: "id", Type: "String"}, {Name: "t", Type: "String"}}})
	require.NoError(t, err)
	_, err = m.GetRecord(ctx, "fg", "1")
	require.Error(t, err, "a re-created feature group starts with no records")
}

func TestNotebookDelete_RequiresStoppedOrFailed(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateNotebookInstance(ctx, driver.NotebookInstanceSpec{Name: "nb", InstanceType: "ml.t3.medium"})
	require.NoError(t, err)

	err = m.DeleteNotebookInstance(ctx, "nb")
	require.Error(t, err)
	assert.True(t, errors.IsFailedPrecondition(err), "an InService notebook cannot be deleted")

	require.NoError(t, m.StopNotebookInstance(ctx, "nb"))
	require.NoError(t, m.DeleteNotebookInstance(ctx, "nb"))
}

func TestEndpointNameReusableAfterDelete_AsyncSettle(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc), config.WithAsyncSettle(), config.WithAccountID("123456789012")))
	ctx := context.Background()

	_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "m"})
	require.NoError(t, err)
	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{
		ConfigName: "c", ProductionVariants: []driver.ProductionVariant{{VariantName: "v", ModelName: "m", InitialVariantWeight: ptr(1.0)}},
	})
	require.NoError(t, err)
	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "e", ConfigName: "c"})
	require.NoError(t, err)
	fc.Advance(time.Minute)
	require.NoError(t, m.DeleteEndpoint(ctx, "e"))
	fc.Advance(time.Minute)

	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "e", ConfigName: "c"})
	require.NoError(t, err, "a name whose delete window elapsed is free")
}

func TestMonitoringExecutions_InFlightFinishOnStop(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc), config.WithAsyncSettle(), config.WithAccountID("123456789012")))
	ctx := context.Background()

	_, err := m.CreateMonitoringSchedule(ctx, schedSpec("s", hourly()))
	require.NoError(t, err)
	fc.Advance(time.Hour + 100*time.Millisecond)
	require.NoError(t, m.StopMonitoringSchedule(ctx, "s"))
	fc.Advance(time.Hour)

	for _, e := range execTimes2(t, m, "s") {
		assert.NotEqual(t, driver.MonitorExecInProgress, e.Status, "a stopped schedule's runs finish")
	}
}

func execTimes2(t *testing.T, m *Mock, schedule string) []driver.MonitoringExecution {
	t.Helper()

	page, err := m.ListMonitoringExecutions(context.Background(), driver.ListOptions{Filters: map[string]string{"MonitoringScheduleName": schedule}})
	require.NoError(t, err)

	return page.Items
}

// TestDuplicateCreates_ExceptionPerOp pins which duplicate creates are
// ResourceInUse (the SDK models it on those operations) and which stay a plain
// already-exists ValidationException (no ResourceInUse in the operation's error list).
func TestDuplicateCreates_ExceptionPerOp(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	dom, err := m.CreateDomain(ctx, driver.DomainSpec{DomainName: "dupdom", AuthMode: "IAM"})
	require.NoError(t, err)

	_, err = m.CreateUserProfile(ctx, driver.UserProfileSpec{DomainID: dom.DomainID, UserProfileName: "bob"})
	require.NoError(t, err)

	inUse := map[string]func() error{
		"training job":   func() error { _, e := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "dup"}); return e },
		"processing job": func() error { _, e := m.CreateProcessingJob(ctx, driver.ProcessingJobConfig{JobName: "dup"}); return e },
		"transform job": func() error {
			_, e := m.CreateTransformJob(ctx, driver.TransformJobConfig{JobName: "dup", ModelName: "m1"})
			return e
		},
		"tuning job": func() error {
			_, e := m.CreateHyperParameterTuningJob(ctx, driver.HyperParameterTuningJobConfig{JobName: "dup", MaxJobs: 4})
			return e
		},
		"AutoML job":   func() error { _, e := m.CreateAutoMLJobV2(ctx, driver.AutoMLJobConfig{JobName: "dup"}); return e },
		"labeling job": func() error { _, e := m.CreateLabelingJob(ctx, driver.LabelingJobConfig{JobName: "dup"}); return e },
		"compilation job": func() error {
			_, e := m.CreateCompilationJob(ctx, driver.CompilationJobConfig{JobName: "dup"})
			return e
		},
		"feature group": func() error {
			_, e := m.CreateFeatureGroup(ctx, driver.FeatureGroupSpec{
				GroupName: "dup", RecordIdentifierName: "id", EventTimeFeatureName: "ts",
				Features: []driver.FeatureDefinition{{Name: "id", Type: "String"}, {Name: "ts", Type: "String"}},
			})
			return e
		},
		"cluster": func() error {
			_, e := m.CreateCluster(ctx, driver.ClusterSpec{ClusterName: "dup", InstanceGroups: []driver.ClusterInstanceGroupSpec{groupSpec("w", 1)}})
			return e
		},
		"user profile": func() error {
			_, e := m.CreateUserProfile(ctx, driver.UserProfileSpec{DomainID: dom.DomainID, UserProfileName: "alice"})
			return e
		},
		"space": func() error {
			_, e := m.CreateSpace(ctx, driver.SpaceSpec{DomainID: dom.DomainID, SpaceName: "dup"})
			return e
		},
		"app": func() error {
			_, e := m.CreateApp(ctx, driver.AppSpec{DomainID: dom.DomainID, UserProfileName: "bob", AppType: "JupyterServer", AppName: "dup"})
			return e
		},
	}

	for name, create := range inUse {
		require.NoError(t, createOnce(name, create), name+": first create")
		assert.Equal(t, driver.ExResourceInUse, exceptionOf(create()), name+": duplicate is ResourceInUse")
	}

	// No ResourceInUse in the SDK error list: stays a validation-style duplicate.
	_, err = m.CreateModel(ctx, driver.ModelConfig{ModelName: "dupm", RoleARN: "role"})
	require.NoError(t, err)

	_, err = m.CreateModel(ctx, driver.ModelConfig{ModelName: "dupm", RoleARN: "role"})
	assert.True(t, errors.IsAlreadyExists(err))
	assert.Empty(t, exceptionOf(err))
}

// createOnce runs a create function the first time (the duplicate follows).
func createOnce(_ string, create func() error) error { return create() }

// A deleted endpoint is purged lazily under async settle. It must not come back
// InService after a persist restart, and a tag on it must not succeed once its
// Deleting window has elapsed.
func TestEndpoint_DeletingNotPersisted(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()
	cfg := seedConfig(t, m, "a", 1, 1)

	_, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "pe", ConfigName: cfg, Tags: []driver.Tag{{Key: "k", Value: "v"}}})
	require.NoError(t, err)
	fc.Advance(3 * time.Second)

	err = m.DeleteEndpoint(ctx, "pe")
	require.NoError(t, err)

	raw, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	restored, _ := newAsyncMock()
	require.NoError(t, restored.Restore(ctx, raw))

	_, err = restored.DescribeEndpoint(ctx, "pe")
	assert.True(t, errors.IsNotFound(err), "a deleted endpoint is gone after restore")

	list, err := restored.ListEndpoints(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, list.Items)

	_, err = restored.ListTags(ctx, restored.arn("endpoint/pe"))
	assert.True(t, errors.IsNotFound(err), "its tags are not restored either")

	for _, tags := range restored.tags.All() {
		assert.Empty(t, tags, "no orphan tag entry for the deleted endpoint")
	}

	// The live mock is untouched by the snapshot: still Deleting, then gone.
	got, err := m.DescribeEndpoint(ctx, "pe")
	require.NoError(t, err)
	assert.Equal(t, driver.EndpointDeleting, got.Status)
}

func TestEndpoint_TagLivenessAfterDelete(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()
	cfg := seedConfig(t, m, "a", 1, 1)

	_, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "pe", ConfigName: cfg})
	require.NoError(t, err)
	fc.Advance(3 * time.Second)

	err = m.DeleteEndpoint(ctx, "pe")
	require.NoError(t, err)

	arn := m.arn("endpoint/pe")

	// Still Deleting: the resource exists, so tagging works.
	_, err = m.AddTags(ctx, arn, []driver.Tag{{Key: "a", Value: "1"}})
	require.NoError(t, err)

	// Its Deleting window has elapsed but nobody read it yet: it is gone.
	fc.Advance(5 * time.Second)

	_, err = m.AddTags(ctx, arn, []driver.Tag{{Key: "b", Value: "2"}})
	assert.True(t, errors.IsNotFound(err), "tagging an elapsed-Deleting endpoint is not found")
}

// AWS documents SortOrder default Descending for ListEndpoints, ListModels,
// ListEndpointConfigs and ListTransformJobs.
func TestList_DefaultSortOrderDescending(t *testing.T) {
	m, fc := newTestMockClock()
	ctx := context.Background()

	for _, n := range []string{"ord-a", "ord-b", "ord-c"} {
		_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: n, RoleARN: "role", Containers: []driver.ContainerDefinition{{Image: "i"}}})
		require.NoError(t, err)
		cfg := seedConfig(t, m, n, 1, 1)
		_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: n, ConfigName: cfg})
		require.NoError(t, err)
		_, err = m.CreateTransformJob(ctx, driver.TransformJobConfig{JobName: n, ModelName: n})
		require.NoError(t, err)

		fc.Advance(time.Second)
	}

	names := func(items any) []string {
		var out []string

		switch v := items.(type) {
		case []driver.Model:
			for i := range v {
				out = append(out, v[i].ModelName)
			}
		case []driver.EndpointConfig:
			for i := range v {
				out = append(out, v[i].ConfigName)
			}
		case []driver.Endpoint:
			for i := range v {
				out = append(out, v[i].EndpointName)
			}
		case []driver.TransformJob:
			for i := range v {
				out = append(out, v[i].JobName)
			}
		}

		return out
	}

	newestFirst := []string{"ord-c", "ord-b", "ord-a"}
	oldestFirst := []string{"ord-a", "ord-b", "ord-c"}

	mp, _ := m.ListModels(ctx, driver.ListOptions{NamePrefix: "ord-"}) // seedConfig also creates model-a
	cp, _ := m.ListEndpointConfigs(ctx, driver.ListOptions{})
	ep, _ := m.ListEndpoints(ctx, driver.ListOptions{})
	tp, _ := m.ListTransformJobs(ctx, driver.ListOptions{})

	assert.Equal(t, newestFirst, names(mp.Items), "ListModels")
	assert.Equal(t, []string{"cfg-ord-c", "cfg-ord-b", "cfg-ord-a"}, names(cp.Items), "ListEndpointConfigs")
	assert.Equal(t, newestFirst, names(ep.Items), "ListEndpoints")
	assert.Equal(t, newestFirst, names(tp.Items), "ListTransformJobs")

	// An explicit order still wins, and a page of one is the newest item.
	ap, _ := m.ListEndpoints(ctx, driver.ListOptions{SortOrder: driver.SortAscending})
	assert.Equal(t, oldestFirst, names(ap.Items))

	one, _ := m.ListEndpoints(ctx, driver.ListOptions{MaxResults: 1})
	assert.Equal(t, []string{"ord-c"}, names(one.Items), "--max-results 1 returns the latest endpoint")

	// Training and processing jobs keep their documented ascending default.
	for _, n := range []string{"t-a", "t-b"} {
		_, err := m.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: n})
		require.NoError(t, err)

		fc.Advance(time.Second)
	}

	tj, _ := m.ListTrainingJobs(ctx, driver.ListOptions{})
	require.Len(t, tj.Items, 2)
	assert.Equal(t, "t-a", tj.Items[0].JobName)
}

func TestList_NotebookFamilySortAndFilters(t *testing.T) {
	m, fc := newTestMockClock()
	ctx := context.Background()

	for _, n := range []string{"zeta", "alpha"} { // created zeta first, so alpha is newer
		_, err := m.CreateNotebookInstanceLifecycleConfig(ctx, driver.NotebookLifecycleConfigSpec{Name: n, OnStart: "echo"})
		require.NoError(t, err)
		_, err = m.CreateCodeRepository(ctx, driver.CodeRepositorySpec{Name: n, GitURL: "https://git/" + n})
		require.NoError(t, err)
		_, err = m.CreateNotebookInstance(ctx, driver.NotebookInstanceSpec{Name: n, InstanceType: "ml.t3.medium"})
		require.NoError(t, err)

		fc.Advance(time.Hour)
	}

	// ListCodeRepositories and ListNotebookInstances default SortBy to Name.
	repos, err := m.ListCodeRepositories(ctx, driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, repos.Items, 2)
	assert.Equal(t, "alpha", repos.Items[0].Name, "code repositories default to Name order")

	nbs, err := m.ListNotebookInstances(ctx, driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, nbs.Items, 2)
	assert.Equal(t, "alpha", nbs.Items[0].Name, "notebook instances default to Name order")

	// LastModifiedTime is a documented SortBy value and filter on both.
	lcs, err := m.ListNotebookInstanceLifecycleConfigs(ctx, driver.ListOptions{SortBy: driver.SortByLastModifiedTime, SortOrder: driver.SortDescending})
	require.NoError(t, err)
	require.Len(t, lcs.Items, 2)
	assert.Equal(t, "alpha", lcs.Items[0].Name)

	repos, err = m.ListCodeRepositories(ctx, driver.ListOptions{SortBy: driver.SortByLastModifiedTime, SortOrder: driver.SortDescending})
	require.NoError(t, err)
	assert.Equal(t, "alpha", repos.Items[0].Name)
	assert.NotEmpty(t, repos.Items[0].LastModifiedTime)

	for name, list := range map[string]func(string) (int, error){
		"lifecycle configs": func(after string) (int, error) {
			p, e := m.ListNotebookInstanceLifecycleConfigs(ctx, driver.ListOptions{LastModifiedTimeAfter: after})
			return len(p.Items), e
		},
		"code repositories": func(after string) (int, error) {
			p, e := m.ListCodeRepositories(ctx, driver.ListOptions{LastModifiedTimeAfter: after})
			return len(p.Items), e
		},
	} {
		n, e := list("2000-01-01T00:00:00Z")
		require.NoError(t, e, name)
		assert.Equal(t, 2, n, name+": a past LastModifiedTimeAfter matches everything")

		n, e = list("2999-01-01T00:00:00Z")
		require.NoError(t, e, name)
		assert.Equal(t, 0, n, name+": a future bound matches nothing")
	}
}

// A filter on a field the operation does not have must be an error, never a
// silently empty result.
func TestList_UnsupportedFilterIsAnError(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "m", RoleARN: "role", Containers: []driver.ContainerDefinition{{Image: "i"}}})
	require.NoError(t, err)

	_, err = m.ListModels(ctx, driver.ListOptions{LastModifiedTimeAfter: "2000-01-01T00:00:00Z"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.ListModels(ctx, driver.ListOptions{StatusEquals: "InService"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
}

// ModelPackageGroupName takes a name or an ARN (CreateModelPackage).
func TestCreateModelPackage_AcceptsGroupARN(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	g, err := m.CreateModelPackageGroup(ctx, driver.ModelPackageGroupSpec{GroupName: "grp1"})
	require.NoError(t, err)

	byARN, err := m.CreateModelPackage(ctx, driver.ModelPackageSpec{GroupName: g.GroupARN})
	require.NoError(t, err)
	assert.Equal(t, "grp1", byARN.GroupName, "the group is stored by name")
	assert.Equal(t, 1, byARN.Version)

	byName, err := m.CreateModelPackage(ctx, driver.ModelPackageSpec{GroupName: "grp1"})
	require.NoError(t, err)
	assert.Equal(t, 2, byName.Version, "name and ARN refer to the same group")

	_, err = m.CreateModelPackage(ctx, driver.ModelPackageSpec{GroupName: m.arn("model-package-group/ghost")})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err) || errors.IsNotFound(err))
}

// A library caller who omits InitialVariantWeight gets SageMaker's default of
// 1.0 in the provider, the same as `serve`. An explicit 0 is kept.
func TestEndpointConfig_VariantWeightDefaultsInProvider(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateModel(ctx, driver.ModelConfig{ModelName: "m", RoleARN: "role", Containers: []driver.ContainerDefinition{{Image: "i"}}})
	require.NoError(t, err)

	zero := 0.0
	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{ConfigName: "c", ProductionVariants: []driver.ProductionVariant{
		{VariantName: "omitted", ModelName: "m", InstanceType: "ml.m5.large", InitialInstanceCount: 1},
		{VariantName: "zero", ModelName: "m", InstanceType: "ml.m5.large", InitialInstanceCount: 1, InitialVariantWeight: &zero},
	}})
	require.NoError(t, err)

	got, err := m.DescribeEndpointConfig(ctx, "c")
	require.NoError(t, err)
	require.NotNil(t, got.ProductionVariants[0].InitialVariantWeight)
	assert.InDelta(t, 1.0, *got.ProductionVariants[0].InitialVariantWeight, 0.001, "an omitted weight is stored as 1.0")
	require.NotNil(t, got.ProductionVariants[1].InitialVariantWeight)
	assert.InDelta(t, 0.0, *got.ProductionVariants[1].InitialVariantWeight, 0.001, "an explicit 0 is kept")

	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "e", ConfigName: "c"})
	require.NoError(t, err)

	ep, err := m.DescribeEndpoint(ctx, "e")
	require.NoError(t, err)
	assert.InDelta(t, 1.0, ep.VariantSummaries[0].CurrentWeight, 0.001, "DescribeEndpoint shows CurrentWeight 1, not 0")
	assert.InDelta(t, 0.0, ep.VariantSummaries[1].CurrentWeight, 0.001)

	// Reads are deep copies: mutating the returned weight does not change the stored one.
	*got.ProductionVariants[0].InitialVariantWeight = 9

	again, _ := m.DescribeEndpointConfig(ctx, "c")
	assert.InDelta(t, 1.0, *again.ProductionVariants[0].InitialVariantWeight, 0.001)

	neg := -1.0
	_, err = m.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{ConfigName: "bad", ProductionVariants: []driver.ProductionVariant{
		{VariantName: "v", ModelName: "m", InitialVariantWeight: &neg},
	}})
	assert.True(t, errors.IsInvalidArgument(err))
}

// A model failure seeded on one endpoint must not apply to a re-created
// endpoint that happens to reuse the name.
func TestInvocationFailure_ClearedOnEndpointDelete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	cfg := seedConfig(t, m, "a", 1, 1)

	_, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfg})
	require.NoError(t, err)

	m.SetInvocationFailure("ep", 500)

	_, err = m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep"})
	require.Error(t, err, "the seeded failure applies while the endpoint exists")

	require.NoError(t, m.DeleteEndpoint(ctx, "ep"))

	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfg})
	require.NoError(t, err)

	_, err = m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep"})
	assert.NoError(t, err, "a re-created endpoint starts clean")
}

// DesiredWeightAndCapacity allows DesiredInstanceCount down to 0 (scale to zero).
func TestUpdateEndpointWeightsAndCapacities_ScaleToZero(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	cfg := seedConfig(t, m, "a", 2, 1)

	_, err := m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfg})
	require.NoError(t, err)

	_, err = m.UpdateEndpointWeightsAndCapacities(ctx, "ep", []driver.VariantWeight{{VariantName: "v1", DesiredInstanceCount: ptr(0)}})
	require.NoError(t, err)

	got, err := m.DescribeEndpoint(ctx, "ep")
	require.NoError(t, err)
	assert.Equal(t, 0, got.VariantSummaries[0].DesiredInstanceCount)
	assert.Equal(t, 0, got.VariantSummaries[0].CurrentInstanceCount)
}

// A schedule keeps producing executions: once more than the cap have fired, the
// newest ones are kept, not the oldest.
func TestScheduleFires_KeepsNewest(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(3000 * time.Hour)

	fires := scheduleSpec{hourly: true}.fires(from, to, driver.MaxMonitoringExecutionsPerSchedule)

	require.Len(t, fires, driver.MaxMonitoringExecutionsPerSchedule)
	assert.Equal(t, to, fires[len(fires)-1], "the most recent fire is kept")
	assert.Equal(t, to.Add(-999*time.Hour), fires[0], "the oldest kept fire is 999 hours before it")

	for i := 1; i < len(fires); i++ {
		require.True(t, fires[i].After(fires[i-1]), "oldest first, strictly increasing")
	}

	// Below the cap nothing changes: every fire in (from, to], oldest first.
	few := scheduleSpec{hourly: true}.fires(from, from.Add(3*time.Hour), driver.MaxMonitoringExecutionsPerSchedule)
	assert.Equal(t, []time.Time{from.Add(time.Hour), from.Add(2 * time.Hour), from.Add(3 * time.Hour)}, few)

	// A daily schedule far in the future still reports its latest fire.
	daily := scheduleSpec{hour: 3}.fires(from, to, 5)
	require.Len(t, daily, 5)
	assert.Equal(t, 3, daily[4].Hour())
	assert.True(t, daily[4].After(to.Add(-24*time.Hour)), "within the last day of the window")
}

func TestInvocationsPerInstance_ScalesWithInstanceCount(t *testing.T) {
	r := newMetricsRig()
	r.endpoint(t, "ep") // 2 instances

	_, err := r.m.UpdateEndpointWeightsAndCapacities(r.ctx, "ep", []driver.VariantWeight{{VariantName: "AllTraffic", DesiredInstanceCount: ptr(4)}})
	require.NoError(t, err)

	for i := 0; i < 4; i++ {
		_, err = r.m.InvokeEndpoint(r.ctx, driver.InvokeEndpointInput{EndpointName: "ep", Body: []byte("x")})
		require.NoError(t, err)
	}

	dims := map[string]string{"EndpointName": "ep", "VariantName": "AllTraffic"}
	assert.InDelta(t, 4, r.sum(t, "AWS/SageMaker", "Invocations", dims), 0)
	assert.InDelta(t, 1.0, r.sum(t, "AWS/SageMaker", "InvocationsPerInstance", dims), 0.0001, "4 invocations over 4 instances")

	// A scaled-to-zero variant reports no per-instance value rather than dividing by zero.
	_, err = r.m.UpdateEndpointWeightsAndCapacities(r.ctx, "ep", []driver.VariantWeight{{VariantName: "AllTraffic", DesiredInstanceCount: ptr(0)}})
	require.NoError(t, err)

	_, err = r.m.InvokeEndpoint(r.ctx, driver.InvokeEndpointInput{EndpointName: "ep", Body: []byte("x")})
	require.NoError(t, err)
	assert.InDelta(t, 1.0, r.sum(t, "AWS/SageMaker", "InvocationsPerInstance", dims), 0.0001, "no datapoint at zero instances")
}

// A describe / list / delete sweep over the resource families, so every thin
// shim is exercised in this package and each family's list reports what the
// create stored.
func TestFamilies_DescribeListDeleteSweep(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	count := func(name string, n int, err error, want int) {
		t.Helper()
		require.NoError(t, err, name)
		assert.Equal(t, want, n, name)
	}

	// Jobs: every kind lists what was created.
	_, err := m.CreateProcessingJob(ctx, driver.ProcessingJobConfig{JobName: "p1"})
	require.NoError(t, err)
	pj, err := m.ListProcessingJobs(ctx, driver.ListOptions{})
	count("processing jobs", len(pj.Items), err, 1)

	_, err = m.CreateHyperParameterTuningJob(ctx, driver.HyperParameterTuningJobConfig{JobName: "h1", MaxJobs: 2})
	require.NoError(t, err)
	hj, err := m.ListHyperParameterTuningJobs(ctx, driver.ListOptions{})
	count("tuning jobs", len(hj.Items), err, 1)

	_, err = m.CreateAutoMLJobV2(ctx, driver.AutoMLJobConfig{JobName: "a1"})
	require.NoError(t, err)
	aj, err := m.ListAutoMLJobs(ctx, driver.ListOptions{})
	count("automl jobs", len(aj.Items), err, 1)

	_, err = m.CreateLabelingJob(ctx, driver.LabelingJobConfig{JobName: "l1"})
	require.NoError(t, err)
	lj, err := m.ListLabelingJobs(ctx, driver.ListOptions{})
	count("labeling jobs", len(lj.Items), err, 1)

	_, err = m.CreateCompilationJob(ctx, driver.CompilationJobConfig{JobName: "c1"})
	require.NoError(t, err)
	cj, err := m.ListCompilationJobs(ctx, driver.ListOptions{})
	count("compilation jobs", len(cj.Items), err, 1)

	// Pipelines and experiments.
	_, err = m.CreatePipeline(ctx, driver.PipelineSpec{PipelineName: "pl", Definition: "{}"})
	require.NoError(t, err)
	_, err = m.DescribePipeline(ctx, "pl")
	require.NoError(t, err)
	up, err := m.UpdatePipeline(ctx, "pl", `{"Version":"2020-12-01"}`)
	require.NoError(t, err)
	assert.Contains(t, up.Definition, "2020-12-01")
	pl, err := m.ListPipelines(ctx, driver.ListOptions{})
	count("pipelines", len(pl.Items), err, 1)

	ex, err := m.StartPipelineExecution(ctx, "pl")
	require.NoError(t, err)
	exs, err := m.ListPipelineExecutions(ctx, "pl", driver.ListOptions{})
	count("pipeline executions", len(exs.Items), err, 1)
	assert.Equal(t, ex.ExecutionARN, exs.Items[0].ExecutionARN)

	_, err = m.CreateExperiment(ctx, driver.ExperimentSpec{ExperimentName: "exp"})
	require.NoError(t, err)
	es, err := m.ListExperiments(ctx, driver.ListOptions{})
	count("experiments", len(es.Items), err, 1)

	// Feature groups and clusters.
	_, err = m.CreateFeatureGroup(ctx, driver.FeatureGroupSpec{
		GroupName: "fg", RecordIdentifierName: "id", EventTimeFeatureName: "ts",
		Features: []driver.FeatureDefinition{{Name: "id", Type: "String"}, {Name: "ts", Type: "String"}},
	})
	require.NoError(t, err)
	_, err = m.DescribeFeatureGroup(ctx, "fg")
	require.NoError(t, err)
	fgs, err := m.ListFeatureGroups(ctx, driver.ListOptions{})
	count("feature groups", len(fgs.Items), err, 1)

	_, err = m.CreateCluster(ctx, driver.ClusterSpec{ClusterName: "cl", InstanceGroups: []driver.ClusterInstanceGroupSpec{groupSpec("w", 1)}})
	require.NoError(t, err)
	cls, err := m.ListClusters(ctx, driver.ListOptions{})
	count("clusters", len(cls.Items), err, 1)
	require.NoError(t, m.DeleteCluster(ctx, "cl"))
	cls, err = m.ListClusters(ctx, driver.ListOptions{})
	count("clusters after delete", len(cls.Items), err, 0)

	// Notebook support resources and the registry.
	_, err = m.CreateNotebookInstanceLifecycleConfig(ctx, driver.NotebookLifecycleConfigSpec{Name: "lc", OnStart: "echo"})
	require.NoError(t, err)
	require.NoError(t, m.DeleteNotebookInstanceLifecycleConfig(ctx, "lc"))

	_, err = m.CreateCodeRepository(ctx, driver.CodeRepositorySpec{Name: "repo", GitURL: "https://git/r"})
	require.NoError(t, err)
	repo, err := m.DescribeCodeRepository(ctx, "repo")
	require.NoError(t, err)
	assert.NotEmpty(t, repo.LastModifiedTime)
	require.NoError(t, m.DeleteCodeRepository(ctx, "repo"))

	g, err := m.CreateModelPackageGroup(ctx, driver.ModelPackageGroupSpec{GroupName: "grp"})
	require.NoError(t, err)
	gs, err := m.ListModelPackageGroups(ctx, driver.ListOptions{})
	count("package groups", len(gs.Items), err, 1)

	pkg, err := m.CreateModelPackage(ctx, driver.ModelPackageSpec{GroupName: g.GroupARN})
	require.NoError(t, err)
	require.NoError(t, m.DeleteModelPackage(ctx, pkg.PackageARN))

	// Studio: domain, user profile, space, app.
	dom, err := m.CreateDomain(ctx, driver.DomainSpec{DomainName: "dom", AuthMode: "IAM"})
	require.NoError(t, err)
	_, err = m.DescribeDomain(ctx, dom.DomainID)
	require.NoError(t, err)

	_, err = m.CreateUserProfile(ctx, driver.UserProfileSpec{DomainID: dom.DomainID, UserProfileName: "alice"})
	require.NoError(t, err)
	_, err = m.DescribeUserProfile(ctx, dom.DomainID, "alice")
	require.NoError(t, err)
	ups, err := m.ListUserProfiles(ctx, dom.DomainID, driver.ListOptions{})
	count("user profiles", len(ups.Items), err, 1)

	_, err = m.CreateSpace(ctx, driver.SpaceSpec{DomainID: dom.DomainID, SpaceName: "sp"})
	require.NoError(t, err)
	_, err = m.DescribeSpace(ctx, dom.DomainID, "sp")
	require.NoError(t, err)
	sps, err := m.ListSpaces(ctx, dom.DomainID, driver.ListOptions{})
	count("spaces", len(sps.Items), err, 1)

	app := driver.AppSpec{DomainID: dom.DomainID, UserProfileName: "alice", AppType: "JupyterServer", AppName: "default"}
	_, err = m.CreateApp(ctx, app)
	require.NoError(t, err)
	_, err = m.DescribeApp(ctx, app)
	require.NoError(t, err)
	require.NoError(t, m.DeleteApp(ctx, app))
	require.NoError(t, m.DeleteSpace(ctx, dom.DomainID, "sp"))

	// An endpoint config deletes once nothing uses it.
	cfg := seedConfig(t, m, "sweep", 1, 1)
	require.NoError(t, m.DeleteEndpointConfig(ctx, cfg))
}

// The remaining families' list and delete shims, plus tag liveness for the
// resource types whose ARNs are not keyed by a plain name.
func TestFamilies_ListSweepAndTagLiveness(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	img, err := m.CreateImage(ctx, driver.ImageSpec{Name: "img-1", RoleARN: testRole})
	require.NoError(t, err)
	imgs, err := m.ListImages(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, imgs.Items, 1)

	_, err = m.CreateAppImageConfig(ctx, driver.AppImageConfigSpec{
		Name: "cfg-1", KernelGateway: &driver.AppImageConfigBody{KernelSpecs: []driver.KernelSpec{{Name: "python3"}}, FileSystemConfig: &driver.FileSystemConfig{}},
	})
	require.NoError(t, err)
	acs, err := m.ListAppImageConfigs(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, acs.Items, 1)

	// Lineage contexts.
	c, err := m.CreateContext(ctx, driver.LineageEntitySpec{Name: "ctx-1", Type: "Experiment", Source: driver.LineageSource{SourceURI: "s3://b/c"}})
	require.NoError(t, err)
	cs, err := m.ListContexts(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, cs.Items, 1)

	desc := "updated"
	up, err := m.UpdateContext(ctx, driver.LineageEntityUpdate{Key: "ctx-1", Description: &desc})
	require.NoError(t, err)
	assert.Equal(t, "updated", up.Description)

	arn, err := m.DeleteContext(ctx, "ctx-1")
	require.NoError(t, err)
	assert.Equal(t, c.ARN, arn)

	// Workforce and workteam lists.
	_, err = m.CreateWorkforce(ctx, cognitoWorkforce("default"))
	require.NoError(t, err)
	_, err = m.CreateWorkteam(ctx, driver.WorkteamSpec{Name: "t1", Description: "d", Members: []driver.MemberDefinition{cognitoMember("g")}})
	require.NoError(t, err)

	wfs, err := m.ListWorkforces(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, wfs.Items, 1)

	wts, err := m.ListWorkteams(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, wts.Items, 1)

	// Inference components.
	cfg := seedConfig(t, m, "ic", 1, 1)
	_, err = m.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: cfg})
	require.NoError(t, err)

	_, err = m.CreateInferenceComponent(ctx, driver.InferenceComponentSpec{Name: "ic-1", EndpointName: "ep", VariantName: "v1"})
	require.NoError(t, err)
	_, err = m.DescribeInferenceComponent(ctx, "ic-1")
	require.NoError(t, err)
	ics, err := m.ListInferenceComponents(ctx, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, ics.Items, 1)
	require.NoError(t, m.DeleteInferenceComponent(ctx, "ic-1"))

	// Tag liveness for pipeline and app ARNs: a live resource is taggable, a missing one is not found.
	_, err = m.CreatePipeline(ctx, driver.PipelineSpec{PipelineName: "pl", Definition: "{}"})
	require.NoError(t, err)

	_, err = m.AddTags(ctx, m.arn("pipeline/pl"), []driver.Tag{{Key: "k", Value: "v"}})
	require.NoError(t, err)

	_, err = m.AddTags(ctx, m.arn("pipeline/ghost"), []driver.Tag{{Key: "k", Value: "v"}})
	assert.True(t, errors.IsNotFound(err))

	_, err = m.AddTags(ctx, img.ImageARN, []driver.Tag{{Key: "k", Value: "v"}})
	require.NoError(t, err)

	// The event publisher can be unwired (nil) without effect.
	m.SetEventPublisher(nil)
}
