package sagemaker_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	fsrt "github.com/aws/aws-sdk-go-v2/service/sagemakerfeaturestoreruntime"
	fsrttypes "github.com/aws/aws-sdk-go-v2/service/sagemakerfeaturestoreruntime/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func smClient(t *testing.T) *awssm.Client {
	t.Helper()
	url := newServer(t)

	return awssm.NewFromConfig(newCfg(t), func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })
}

func TestSDKProcessingJob(t *testing.T) {
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateProcessingJob(ctx, &awssm.CreateProcessingJobInput{
		ProcessingJobName: aws.String("pj-1"),
		RoleArn:           aws.String("arn:aws:iam::123456789012:role/svc"),
		AppSpecification:  &smtypes.AppSpecification{ImageUri: aws.String("proc:latest")},
		ProcessingResources: &smtypes.ProcessingResources{
			ClusterConfig: &smtypes.ProcessingClusterConfig{
				InstanceCount:  aws.Int32(1),
				InstanceType:   smtypes.ProcessingInstanceTypeMlM5Large,
				VolumeSizeInGB: aws.Int32(10),
			},
		},
	})
	require.NoError(t, err)

	d, err := c.DescribeProcessingJob(ctx, &awssm.DescribeProcessingJobInput{ProcessingJobName: aws.String("pj-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.ProcessingJobStatusCompleted, d.ProcessingJobStatus)

	l, err := c.ListProcessingJobs(ctx, &awssm.ListProcessingJobsInput{})
	require.NoError(t, err)
	assert.Len(t, l.ProcessingJobSummaries, 1)
}

func TestSDKTransformJob(t *testing.T) {
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateModel(ctx, &awssm.CreateModelInput{
		ModelName:        aws.String("m-x"),
		ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
		PrimaryContainer: &smtypes.ContainerDefinition{Image: aws.String("img:latest")},
	})
	require.NoError(t, err)

	_, err = c.CreateTransformJob(ctx, &awssm.CreateTransformJobInput{
		TransformJobName: aws.String("tj-1"),
		ModelName:        aws.String("m-x"),
		TransformInput: &smtypes.TransformInput{
			DataSource: &smtypes.TransformDataSource{
				S3DataSource: &smtypes.TransformS3DataSource{
					S3DataType: smtypes.S3DataTypeS3Prefix,
					S3Uri:      aws.String("s3://in/"),
				},
			},
		},
		TransformOutput:    &smtypes.TransformOutput{S3OutputPath: aws.String("s3://out/")},
		TransformResources: &smtypes.TransformResources{InstanceType: smtypes.TransformInstanceTypeMlM5Large, InstanceCount: aws.Int32(1)},
	})
	require.NoError(t, err)

	d, err := c.DescribeTransformJob(ctx, &awssm.DescribeTransformJobInput{TransformJobName: aws.String("tj-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.TransformJobStatusCompleted, d.TransformJobStatus)
}

func TestSDKTuningJob(t *testing.T) {
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateHyperParameterTuningJob(ctx, &awssm.CreateHyperParameterTuningJobInput{
		HyperParameterTuningJobName: aws.String("hpo-1"),
		HyperParameterTuningJobConfig: &smtypes.HyperParameterTuningJobConfig{
			Strategy: smtypes.HyperParameterTuningJobStrategyTypeBayesian,
			ResourceLimits: &smtypes.ResourceLimits{
				MaxNumberOfTrainingJobs: aws.Int32(5),
				MaxParallelTrainingJobs: aws.Int32(2),
			},
			HyperParameterTuningJobObjective: &smtypes.HyperParameterTuningJobObjective{
				Type:       smtypes.HyperParameterTuningJobObjectiveTypeMaximize,
				MetricName: aws.String("accuracy"),
			},
		},
	})
	require.NoError(t, err)

	d, err := c.DescribeHyperParameterTuningJob(ctx, &awssm.DescribeHyperParameterTuningJobInput{
		HyperParameterTuningJobName: aws.String("hpo-1"),
	})
	require.NoError(t, err)
	assert.Equal(t, smtypes.HyperParameterTuningJobStatusCompleted, d.HyperParameterTuningJobStatus)
}

func TestSDKLabelingAndCompilationJobs(t *testing.T) {
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateLabelingJob(ctx, &awssm.CreateLabelingJobInput{
		LabelingJobName:    aws.String("lj-1"),
		LabelAttributeName: aws.String("label"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/svc"),
		InputConfig:        &smtypes.LabelingJobInputConfig{DataSource: &smtypes.LabelingJobDataSource{}},
		OutputConfig:       &smtypes.LabelingJobOutputConfig{S3OutputPath: aws.String("s3://out/")},
		HumanTaskConfig: &smtypes.HumanTaskConfig{
			WorkteamArn:                       aws.String("arn:aws:sagemaker:us-east-1:123456789012:workteam/x"),
			UiConfig:                          &smtypes.UiConfig{UiTemplateS3Uri: aws.String("s3://tmpl/ui.html")},
			TaskTitle:                         aws.String("Label images"),
			TaskDescription:                   aws.String("Draw boxes"),
			NumberOfHumanWorkersPerDataObject: aws.Int32(1),
			TaskTimeLimitInSeconds:            aws.Int32(600),
		},
	})
	require.NoError(t, err)

	dl, err := c.DescribeLabelingJob(ctx, &awssm.DescribeLabelingJobInput{LabelingJobName: aws.String("lj-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.LabelingJobStatusCompleted, dl.LabelingJobStatus)

	_, err = c.CreateCompilationJob(ctx, &awssm.CreateCompilationJobInput{
		CompilationJobName: aws.String("cj-1"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/svc"),
		InputConfig:        &smtypes.InputConfig{S3Uri: aws.String("s3://in/model.tar.gz"), Framework: smtypes.FrameworkPytorch, DataInputConfig: aws.String(`{"input":[1,3,224,224]}`)},
		OutputConfig:       &smtypes.OutputConfig{S3OutputLocation: aws.String("s3://out/")},
		StoppingCondition:  &smtypes.StoppingCondition{MaxRuntimeInSeconds: aws.Int32(900)},
	})
	require.NoError(t, err)

	dc, err := c.DescribeCompilationJob(ctx, &awssm.DescribeCompilationJobInput{CompilationJobName: aws.String("cj-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.CompilationJobStatusCompleted, dc.CompilationJobStatus)
}

func TestSDKModelRegistry(t *testing.T) {
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateModelPackageGroup(ctx, &awssm.CreateModelPackageGroupInput{
		ModelPackageGroupName: aws.String("grp"),
	})
	require.NoError(t, err)

	p, err := c.CreateModelPackage(ctx, &awssm.CreateModelPackageInput{
		ModelPackageGroupName: aws.String("grp"),
		InferenceSpecification: &smtypes.InferenceSpecification{
			Containers:                 []smtypes.ModelPackageContainerDefinition{{Image: aws.String("img:latest")}},
			SupportedContentTypes:      []string{"application/json"},
			SupportedResponseMIMETypes: []string{"application/json"},
		},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, aws.ToString(p.ModelPackageArn))

	list, err := c.ListModelPackages(ctx, &awssm.ListModelPackagesInput{ModelPackageGroupName: aws.String("grp")})
	require.NoError(t, err)
	assert.Len(t, list.ModelPackageSummaryList, 1)
}

func TestSDKStudio(t *testing.T) {
	c := smClient(t)
	ctx := context.Background()

	dom, err := c.CreateDomain(ctx, &awssm.CreateDomainInput{
		DomainName:          aws.String("team"),
		AuthMode:            smtypes.AuthModeIam,
		VpcId:               aws.String("vpc-1"),
		SubnetIds:           []string{"subnet-1"},
		DefaultUserSettings: &smtypes.UserSettings{ExecutionRole: aws.String("arn:aws:iam::123456789012:role/studio")},
	})
	require.NoError(t, err)
	domainID := aws.ToString(dom.DomainId)
	assert.Contains(t, domainID, "d-")

	_, err = c.CreateUserProfile(ctx, &awssm.CreateUserProfileInput{
		DomainId:        aws.String(domainID),
		UserProfileName: aws.String("alice"),
	})
	require.NoError(t, err)

	_, err = c.CreateApp(ctx, &awssm.CreateAppInput{
		DomainId:        aws.String(domainID),
		UserProfileName: aws.String("alice"),
		AppType:         smtypes.AppTypeJupyterServer,
		AppName:         aws.String("default"),
	})
	require.NoError(t, err)

	da, err := c.DescribeApp(ctx, &awssm.DescribeAppInput{
		DomainId:        aws.String(domainID),
		UserProfileName: aws.String("alice"),
		AppType:         smtypes.AppTypeJupyterServer,
		AppName:         aws.String("default"),
	})
	require.NoError(t, err)
	assert.Equal(t, smtypes.AppStatusInService, da.Status)
}

func TestSDKNotebookInstance(t *testing.T) {
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateNotebookInstance(ctx, &awssm.CreateNotebookInstanceInput{
		NotebookInstanceName: aws.String("nb-1"),
		InstanceType:         smtypes.InstanceTypeMlT3Medium,
		RoleArn:              aws.String("arn:aws:iam::123456789012:role/nb"),
	})
	require.NoError(t, err)

	d, err := c.DescribeNotebookInstance(ctx, &awssm.DescribeNotebookInstanceInput{NotebookInstanceName: aws.String("nb-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.NotebookInstanceStatusInService, d.NotebookInstanceStatus)

	_, err = c.StopNotebookInstance(ctx, &awssm.StopNotebookInstanceInput{NotebookInstanceName: aws.String("nb-1")})
	require.NoError(t, err)
}

func TestSDKHyperPodCluster(t *testing.T) {
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateCluster(ctx, &awssm.CreateClusterInput{
		ClusterName: aws.String("cl-1"),
		InstanceGroups: []smtypes.ClusterInstanceGroupSpecification{{
			InstanceGroupName: aws.String("workers"),
			InstanceType:      smtypes.ClusterInstanceTypeMlP4d24xlarge,
			InstanceCount:     aws.Int32(2),
			LifeCycleConfig:   &smtypes.ClusterLifeCycleConfig{SourceS3Uri: aws.String("s3://b/"), OnCreate: aws.String("on_create.sh")},
			ExecutionRole:     aws.String("arn:aws:iam::123456789012:role/hp"),
		}},
	})
	require.NoError(t, err)

	d, err := c.DescribeCluster(ctx, &awssm.DescribeClusterInput{ClusterName: aws.String("cl-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.ClusterStatusInservice, d.ClusterStatus)

	nodes, err := c.ListClusterNodes(ctx, &awssm.ListClusterNodesInput{ClusterName: aws.String("cl-1")})
	require.NoError(t, err)
	assert.Len(t, nodes.ClusterNodeSummaries, 2)
}

func TestSDKFeatureStore(t *testing.T) {
	url := newServer(t)
	cfg := newCfg(t)
	c := awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })
	rt := fsrt.NewFromConfig(cfg, func(o *fsrt.Options) { o.BaseEndpoint = aws.String(url) })
	ctx := context.Background()

	_, err := c.CreateFeatureGroup(ctx, &awssm.CreateFeatureGroupInput{
		FeatureGroupName:            aws.String("fg-1"),
		RecordIdentifierFeatureName: aws.String("id"),
		EventTimeFeatureName:        aws.String("ts"),
		FeatureDefinitions: []smtypes.FeatureDefinition{
			{FeatureName: aws.String("id"), FeatureType: smtypes.FeatureTypeString},
			{FeatureName: aws.String("ts"), FeatureType: smtypes.FeatureTypeString},
			{FeatureName: aws.String("score"), FeatureType: smtypes.FeatureTypeFractional},
		},
		OnlineStoreConfig: &smtypes.OnlineStoreConfig{EnableOnlineStore: aws.Bool(true)},
		RoleArn:           aws.String("arn:aws:iam::123456789012:role/fs"),
	})
	require.NoError(t, err)

	d, err := c.DescribeFeatureGroup(ctx, &awssm.DescribeFeatureGroupInput{FeatureGroupName: aws.String("fg-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.FeatureGroupStatusCreated, d.FeatureGroupStatus)

	_, err = rt.PutRecord(ctx, &fsrt.PutRecordInput{
		FeatureGroupName: aws.String("fg-1"),
		Record: []fsrttypes.FeatureValue{
			{FeatureName: aws.String("id"), ValueAsString: aws.String("u1")},
			{FeatureName: aws.String("ts"), ValueAsString: aws.String("2025-01-01T00:00:00Z")},
			{FeatureName: aws.String("score"), ValueAsString: aws.String("0.9")},
		},
	})
	require.NoError(t, err)

	got, err := rt.GetRecord(ctx, &fsrt.GetRecordInput{
		FeatureGroupName:              aws.String("fg-1"),
		RecordIdentifierValueAsString: aws.String("u1"),
	})
	require.NoError(t, err)
	assert.Len(t, got.Record, 3)
}

func TestSDKPipelinesAndExperiments(t *testing.T) {
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreatePipeline(ctx, &awssm.CreatePipelineInput{
		PipelineName:       aws.String("pl-1"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/pipe"),
		PipelineDefinition: aws.String(`{"Version":"2020-12-01","Steps":[]}`),
	})
	require.NoError(t, err)

	exec, err := c.StartPipelineExecution(ctx, &awssm.StartPipelineExecutionInput{PipelineName: aws.String("pl-1")})
	require.NoError(t, err)
	assert.NotEmpty(t, aws.ToString(exec.PipelineExecutionArn))

	de, err := c.DescribePipelineExecution(ctx, &awssm.DescribePipelineExecutionInput{
		PipelineExecutionArn: exec.PipelineExecutionArn,
	})
	require.NoError(t, err)
	assert.Equal(t, smtypes.PipelineExecutionStatusSucceeded, de.PipelineExecutionStatus)

	_, err = c.CreateExperiment(ctx, &awssm.CreateExperimentInput{ExperimentName: aws.String("exp-1")})
	require.NoError(t, err)
	_, err = c.CreateTrial(ctx, &awssm.CreateTrialInput{TrialName: aws.String("tr-1"), ExperimentName: aws.String("exp-1")})
	require.NoError(t, err)

	trials, err := c.ListTrials(ctx, &awssm.ListTrialsInput{ExperimentName: aws.String("exp-1")})
	require.NoError(t, err)
	assert.Len(t, trials.TrialSummaries, 1)
}

func TestSDKInferenceComponent(t *testing.T) {
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateModel(ctx, &awssm.CreateModelInput{
		ModelName:        aws.String("ic-model"),
		ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
		PrimaryContainer: &smtypes.ContainerDefinition{Image: aws.String("img:latest")},
	})
	require.NoError(t, err)
	_, err = c.CreateEndpointConfig(ctx, &awssm.CreateEndpointConfigInput{
		EndpointConfigName: aws.String("ic-cfg"),
		ProductionVariants: []smtypes.ProductionVariant{{
			VariantName:          aws.String("v1"),
			InstanceType:         smtypes.ProductionVariantInstanceTypeMlM5Large,
			InitialInstanceCount: aws.Int32(1),
		}},
	})
	require.NoError(t, err)
	_, err = c.CreateEndpoint(ctx, &awssm.CreateEndpointInput{
		EndpointName: aws.String("ic-ep"), EndpointConfigName: aws.String("ic-cfg"),
	})
	require.NoError(t, err)

	_, err = c.CreateInferenceComponent(ctx, &awssm.CreateInferenceComponentInput{
		InferenceComponentName: aws.String("ic-1"),
		EndpointName:           aws.String("ic-ep"),
		VariantName:            aws.String("v1"),
		Specification: &smtypes.InferenceComponentSpecification{
			ModelName: aws.String("ic-model"),
			ComputeResourceRequirements: &smtypes.InferenceComponentComputeResourceRequirements{
				MinMemoryRequiredInMb: aws.Int32(1024),
			},
		},
		RuntimeConfig: &smtypes.InferenceComponentRuntimeConfig{CopyCount: aws.Int32(1)},
	})
	require.NoError(t, err)

	d, err := c.DescribeInferenceComponent(ctx, &awssm.DescribeInferenceComponentInput{
		InferenceComponentName: aws.String("ic-1"),
	})
	require.NoError(t, err)
	assert.Equal(t, smtypes.InferenceComponentStatusInService, d.InferenceComponentStatus)
}

func createTrainingJobs(t *testing.T, c *awssm.Client, names ...string) {
	t.Helper()

	for _, n := range names {
		_, err := c.CreateTrainingJob(context.Background(), &awssm.CreateTrainingJobInput{
			TrainingJobName: aws.String(n),
			RoleArn:         aws.String("arn:aws:iam::123456789012:role/svc"),
			AlgorithmSpecification: &smtypes.AlgorithmSpecification{
				TrainingImage: aws.String("img:latest"), TrainingInputMode: smtypes.TrainingInputModeFile,
			},
			OutputDataConfig: &smtypes.OutputDataConfig{S3OutputPath: aws.String("s3://b/out")},
			ResourceConfig: &smtypes.ResourceConfig{
				InstanceType: smtypes.TrainingInstanceTypeMlM5Large, InstanceCount: aws.Int32(1), VolumeSizeInGB: aws.Int32(5),
			},
			StoppingCondition: &smtypes.StoppingCondition{MaxRuntimeInSeconds: aws.Int32(3600)},
		})
		require.NoError(t, err)
	}
}

func TestSDKList_PagingAndFilters(t *testing.T) { // T6
	c := smClient(t)
	ctx := context.Background()
	createTrainingJobs(t, c, "job-b", "job-a", "other-c")

	// size-1 paginator walks every job exactly once.
	pager := awssm.NewListTrainingJobsPaginator(c, &awssm.ListTrainingJobsInput{MaxResults: aws.Int32(1)})
	seen := map[string]int{}

	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		require.NoError(t, err)
		require.Len(t, page.TrainingJobSummaries, 1)
		seen[aws.ToString(page.TrainingJobSummaries[0].TrainingJobName)]++
	}

	assert.Equal(t, map[string]int{"job-b": 1, "job-a": 1, "other-c": 1}, seen)

	// filters and sort are honored.
	l, err := c.ListTrainingJobs(ctx, &awssm.ListTrainingJobsInput{
		NameContains: aws.String("job-"), SortBy: smtypes.SortByName, SortOrder: smtypes.SortOrderDescending,
	})
	require.NoError(t, err)
	require.Len(t, l.TrainingJobSummaries, 2)
	assert.Equal(t, "job-b", aws.ToString(l.TrainingJobSummaries[0].TrainingJobName))
	assert.Equal(t, "job-a", aws.ToString(l.TrainingJobSummaries[1].TrainingJobName))

	l, err = c.ListTrainingJobs(ctx, &awssm.ListTrainingJobsInput{StatusEquals: smtypes.TrainingJobStatusStopped})
	require.NoError(t, err)
	assert.Empty(t, l.TrainingJobSummaries)
	assert.Nil(t, l.NextToken)
}

func TestSDKList_BadTokenValidationException(t *testing.T) { // T7
	c := smClient(t)
	_, err := c.ListTrainingJobs(context.Background(), &awssm.ListTrainingJobsInput{NextToken: aws.String("garbage")})
	require.Error(t, err)

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())

	_, err = c.ListTrainingJobs(context.Background(), &awssm.ListTrainingJobsInput{MaxResults: aws.Int32(0)})
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())

	_, err = c.ListTrainingJobs(context.Background(), &awssm.ListTrainingJobsInput{MaxResults: aws.Int32(101)})
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())
}

func TestSDKDuplicateCreate_ValidationException(t *testing.T) { // T11
	c := smClient(t)
	ctx := context.Background()
	createTrainingJobs(t, c, "dup-1")

	_, err := c.CreateModel(ctx, &awssm.CreateModelInput{
		ModelName:        aws.String("dup-model"),
		ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
		PrimaryContainer: &smtypes.ContainerDefinition{Image: aws.String("img:latest")},
	})
	require.NoError(t, err)

	_, err = c.CreateModel(ctx, &awssm.CreateModelInput{
		ModelName:        aws.String("dup-model"),
		ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
		PrimaryContainer: &smtypes.ContainerDefinition{Image: aws.String("img:latest")},
	})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())

	var inUse *smtypes.ResourceInUse
	assert.False(t, errors.As(err, &inUse), "a duplicate create is not ResourceInUse")
}

func newServerOpts(t *testing.T, opts ...config.Option) string {
	t.Helper()

	cloud := cloudemu.NewAWS(opts...)
	srv := awsserver.New(awsserver.Drivers{SageMaker: cloud.SageMaker, S3: cloud.S3})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts.URL
}

func seedSDKEndpoint(t *testing.T, c *awssm.Client, endpoint, config string, count int32, weight float32) {
	t.Helper()

	ctx := context.Background()
	if _, err := c.DescribeModel(ctx, &awssm.DescribeModelInput{ModelName: aws.String("model-a")}); err != nil {
		_, err = c.CreateModel(ctx, &awssm.CreateModelInput{
			ModelName:        aws.String("model-a"),
			ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
			PrimaryContainer: &smtypes.ContainerDefinition{Image: aws.String("inference:latest")},
		})
		require.NoError(t, err)
	}

	_, err := c.CreateEndpointConfig(ctx, &awssm.CreateEndpointConfigInput{
		EndpointConfigName: aws.String(config),
		ProductionVariants: []smtypes.ProductionVariant{{
			VariantName:          aws.String("v1"),
			ModelName:            aws.String("model-a"),
			InstanceType:         smtypes.ProductionVariantInstanceTypeMlM5Large,
			InitialInstanceCount: aws.Int32(count),
			InitialVariantWeight: aws.Float32(weight),
		}},
	})
	require.NoError(t, err)

	if endpoint != "" {
		_, err = c.CreateEndpoint(ctx, &awssm.CreateEndpointInput{
			EndpointName: aws.String(endpoint), EndpointConfigName: aws.String(config),
		})
		require.NoError(t, err)
	}
}

func TestSDKDescribeEndpoint_VariantSummary(t *testing.T) { // T30, T31
	c := smClient(t)
	ctx := context.Background()
	seedSDKEndpoint(t, c, "ep-1", "cfg-1", 2, 1)

	d, err := c.DescribeEndpoint(ctx, &awssm.DescribeEndpointInput{EndpointName: aws.String("ep-1")})
	require.NoError(t, err)
	require.Len(t, d.ProductionVariants, 1)

	v := d.ProductionVariants[0]
	assert.Equal(t, "v1", aws.ToString(v.VariantName))
	require.NotNil(t, v.CurrentInstanceCount)
	assert.Equal(t, int32(2), *v.CurrentInstanceCount)
	require.NotNil(t, v.DesiredInstanceCount)
	assert.Equal(t, int32(2), *v.DesiredInstanceCount)
	require.NotNil(t, v.CurrentWeight)
	assert.InDelta(t, 1.0, *v.CurrentWeight, 0.001)
	require.NotNil(t, v.DesiredWeight)
	require.Len(t, v.DeployedImages, 1)
	assert.Equal(t, "inference:latest", aws.ToString(v.DeployedImages[0].SpecifiedImage))
	assert.Nil(t, d.PendingDeploymentSummary)

	l, err := c.ListEndpoints(ctx, &awssm.ListEndpointsInput{StatusEquals: smtypes.EndpointStatusInService, NameContains: aws.String("ep-")})
	require.NoError(t, err)
	require.Len(t, l.Endpoints, 1)
	assert.Equal(t, smtypes.EndpointStatusInService, l.Endpoints[0].EndpointStatus)
	assert.NotNil(t, l.Endpoints[0].LastModifiedTime)

	// the config echoes the default weight of 1 when the request omits it
	_, err = c.CreateEndpointConfig(ctx, &awssm.CreateEndpointConfigInput{
		EndpointConfigName: aws.String("cfg-noweight"),
		ProductionVariants: []smtypes.ProductionVariant{{
			VariantName: aws.String("v1"), ModelName: aws.String("model-a"),
			InstanceType: smtypes.ProductionVariantInstanceTypeMlM5Large, InitialInstanceCount: aws.Int32(1),
		}},
	})
	require.NoError(t, err)

	cfg, err := c.DescribeEndpointConfig(ctx, &awssm.DescribeEndpointConfigInput{EndpointConfigName: aws.String("cfg-noweight")})
	require.NoError(t, err)
	require.NotNil(t, cfg.ProductionVariants[0].InitialVariantWeight)
	assert.InDelta(t, 1.0, *cfg.ProductionVariants[0].InitialVariantWeight, 0.001)
}

func TestSDKUpdateEndpoint_Routed(t *testing.T) { // T32
	c := smClient(t)
	ctx := context.Background()
	seedSDKEndpoint(t, c, "ep-1", "cfg-1", 1, 1)
	seedSDKEndpoint(t, c, "", "cfg-2", 3, 1)

	out, err := c.UpdateEndpoint(ctx, &awssm.UpdateEndpointInput{
		EndpointName: aws.String("ep-1"), EndpointConfigName: aws.String("cfg-2"),
		DeploymentConfig: &smtypes.DeploymentConfig{
			RollingUpdatePolicy: &smtypes.RollingUpdatePolicy{
				MaximumBatchSize:      &smtypes.CapacitySize{Type: smtypes.CapacitySizeTypeCapacityPercent, Value: aws.Int32(50)},
				WaitIntervalInSeconds: aws.Int32(60),
			},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(out.EndpointArn), "endpoint/ep-1")

	d, err := c.DescribeEndpoint(ctx, &awssm.DescribeEndpointInput{EndpointName: aws.String("ep-1")})
	require.NoError(t, err)
	assert.Equal(t, "cfg-2", aws.ToString(d.EndpointConfigName))
	assert.Equal(t, int32(3), *d.ProductionVariants[0].CurrentInstanceCount)
	require.NotNil(t, d.LastDeploymentConfig)
	assert.Equal(t, int32(60), *d.LastDeploymentConfig.RollingUpdatePolicy.WaitIntervalInSeconds)

	_, err = c.UpdateEndpoint(ctx, &awssm.UpdateEndpointInput{
		EndpointName: aws.String("missing"), EndpointConfigName: aws.String("cfg-2"),
	})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode(), "UpdateEndpoint does not model ResourceNotFound")
	assert.Contains(t, ae.ErrorMessage(), "Could not find endpoint")
}

func TestSDKUpdateEndpointWeightsAndCapacities(t *testing.T) { // T33
	c := smClient(t)
	ctx := context.Background()
	seedSDKEndpoint(t, c, "ep-1", "cfg-1", 1, 1)

	_, err := c.UpdateEndpointWeightsAndCapacities(ctx, &awssm.UpdateEndpointWeightsAndCapacitiesInput{
		EndpointName: aws.String("ep-1"),
		DesiredWeightsAndCapacities: []smtypes.DesiredWeightAndCapacity{
			{VariantName: aws.String("v1"), DesiredWeight: aws.Float32(0.25), DesiredInstanceCount: aws.Int32(4)},
		},
	})
	require.NoError(t, err)

	d, err := c.DescribeEndpoint(ctx, &awssm.DescribeEndpointInput{EndpointName: aws.String("ep-1")})
	require.NoError(t, err)
	assert.InDelta(t, 0.25, *d.ProductionVariants[0].DesiredWeight, 0.001)
	assert.Equal(t, int32(4), *d.ProductionVariants[0].DesiredInstanceCount)

	cfg, err := c.DescribeEndpointConfig(ctx, &awssm.DescribeEndpointConfigInput{EndpointConfigName: aws.String("cfg-1")})
	require.NoError(t, err)
	assert.InDelta(t, 1.0, *cfg.ProductionVariants[0].InitialVariantWeight, 0.001, "the config keeps its initial values")
	assert.Equal(t, int32(1), *cfg.ProductionVariants[0].InitialInstanceCount)

	_, err = c.UpdateEndpointWeightsAndCapacities(ctx, &awssm.UpdateEndpointWeightsAndCapacitiesInput{
		EndpointName:                aws.String("ep-1"),
		DesiredWeightsAndCapacities: []smtypes.DesiredWeightAndCapacity{{VariantName: aws.String("ghost"), DesiredWeight: aws.Float32(1)}},
	})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())
}

func TestSDKEndpoint_AsyncSettleWaiter(t *testing.T) { // T36 over the wire
	url := newServerOpts(t, config.WithAsyncSettle())
	c := awssm.NewFromConfig(newCfg(t), func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })
	ctx := context.Background()
	seedSDKEndpoint(t, c, "ep-1", "cfg-1", 1, 1)

	d, err := c.DescribeEndpoint(ctx, &awssm.DescribeEndpointInput{EndpointName: aws.String("ep-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.EndpointStatusCreating, d.EndpointStatus)
	require.NotNil(t, d.PendingDeploymentSummary)
	assert.Equal(t, "cfg-1", aws.ToString(d.PendingDeploymentSummary.EndpointConfigName))
	require.Len(t, d.ProductionVariants[0].VariantStatus, 1)
	assert.Equal(t, smtypes.VariantStatusCreating, d.ProductionVariants[0].VariantStatus[0].Status)

	waiter := awssm.NewEndpointInServiceWaiter(c, func(o *awssm.EndpointInServiceWaiterOptions) {
		o.MinDelay, o.MaxDelay = 500*time.Millisecond, time.Second
	})
	require.NoError(t, waiter.Wait(ctx, &awssm.DescribeEndpointInput{EndpointName: aws.String("ep-1")}, 30*time.Second))

	_, err = c.DeleteEndpoint(ctx, &awssm.DeleteEndpointInput{EndpointName: aws.String("ep-1")})
	require.NoError(t, err)

	deleted := awssm.NewEndpointDeletedWaiter(c, func(o *awssm.EndpointDeletedWaiterOptions) {
		o.MinDelay, o.MaxDelay = 500*time.Millisecond, time.Second
	})
	require.NoError(t, deleted.Wait(ctx, &awssm.DescribeEndpointInput{EndpointName: aws.String("ep-1")}, 30*time.Second))
}

func TestSDKListEndpointConfigs(t *testing.T) { // was unrouted before; T6
	c := smClient(t)
	ctx := context.Background()
	seedSDKEndpoint(t, c, "", "cfg-b", 1, 1)
	seedSDKEndpoint(t, c, "", "cfg-a", 1, 1)

	pager := awssm.NewListEndpointConfigsPaginator(c, &awssm.ListEndpointConfigsInput{MaxResults: aws.Int32(1)})
	var names []string

	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		require.NoError(t, err)

		for _, e := range page.EndpointConfigs {
			names = append(names, aws.ToString(e.EndpointConfigName))
			assert.NotNil(t, e.CreationTime)
		}
	}

	assert.ElementsMatch(t, []string{"cfg-a", "cfg-b"}, names)
}

func TestSDKUpdateNotebookInstance_Describe(t *testing.T) { // T42
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateNotebookInstanceLifecycleConfig(ctx, &awssm.CreateNotebookInstanceLifecycleConfigInput{
		NotebookInstanceLifecycleConfigName: aws.String("lc-1"),
		OnStart:                             []smtypes.NotebookInstanceLifecycleHook{{Content: aws.String("ZWNobw==")}},
	})
	require.NoError(t, err)

	_, err = c.CreateNotebookInstance(ctx, &awssm.CreateNotebookInstanceInput{
		NotebookInstanceName: aws.String("nb-1"),
		InstanceType:         smtypes.InstanceTypeMlT3Medium,
		RoleArn:              aws.String("arn:aws:iam::123456789012:role/nb"),
		VolumeSizeInGB:       aws.Int32(10),
	})
	require.NoError(t, err)

	// an InService notebook cannot be updated
	_, err = c.UpdateNotebookInstance(ctx, &awssm.UpdateNotebookInstanceInput{
		NotebookInstanceName: aws.String("nb-1"), InstanceType: smtypes.InstanceTypeMlT3Large,
	})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())
	assert.Contains(t, ae.ErrorMessage(), "not in ([Stopped, Failed])")

	_, err = c.StopNotebookInstance(ctx, &awssm.StopNotebookInstanceInput{NotebookInstanceName: aws.String("nb-1")})
	require.NoError(t, err)

	_, err = c.UpdateNotebookInstance(ctx, &awssm.UpdateNotebookInstanceInput{
		NotebookInstanceName:       aws.String("nb-1"),
		InstanceType:               smtypes.InstanceTypeMlT3Large,
		VolumeSizeInGB:             aws.Int32(50),
		LifecycleConfigName:        aws.String("lc-1"),
		DefaultCodeRepository:      aws.String("https://github.com/o/r"),
		AdditionalCodeRepositories: []string{"https://github.com/o/a"},
		RootAccess:                 smtypes.RootAccessDisabled,
	})
	require.NoError(t, err)

	d, err := c.DescribeNotebookInstance(ctx, &awssm.DescribeNotebookInstanceInput{NotebookInstanceName: aws.String("nb-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.NotebookInstanceStatusStopped, d.NotebookInstanceStatus)
	assert.Equal(t, smtypes.InstanceTypeMlT3Large, d.InstanceType)
	assert.Equal(t, int32(50), aws.ToInt32(d.VolumeSizeInGB))
	assert.Equal(t, "lc-1", aws.ToString(d.NotebookInstanceLifecycleConfigName))
	assert.Equal(t, "https://github.com/o/r", aws.ToString(d.DefaultCodeRepository))
	assert.Equal(t, []string{"https://github.com/o/a"}, d.AdditionalCodeRepositories)
	assert.Equal(t, smtypes.RootAccessDisabled, d.RootAccess)
	assert.NotNil(t, d.LastModifiedTime)

	_, err = c.UpdateNotebookInstance(ctx, &awssm.UpdateNotebookInstanceInput{
		NotebookInstanceName: aws.String("nb-1"), DisassociateLifecycleConfig: aws.Bool(true), DisassociateAdditionalCodeRepositories: aws.Bool(true),
	})
	require.NoError(t, err)

	d, err = c.DescribeNotebookInstance(ctx, &awssm.DescribeNotebookInstanceInput{NotebookInstanceName: aws.String("nb-1")})
	require.NoError(t, err)
	assert.Nil(t, d.NotebookInstanceLifecycleConfigName)
	assert.Empty(t, d.AdditionalCodeRepositories)

	_, err = c.StartNotebookInstance(ctx, &awssm.StartNotebookInstanceInput{NotebookInstanceName: aws.String("nb-1")})
	require.NoError(t, err)

	_, err = c.UpdateNotebookInstance(ctx, &awssm.UpdateNotebookInstanceInput{NotebookInstanceName: aws.String("missing"), VolumeSizeInGB: aws.Int32(20)})
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())
	assert.Contains(t, ae.ErrorMessage(), "RecordNotFound")
}

func TestSDKUpdateNotebookInstanceLifecycleConfig(t *testing.T) { // T43
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateNotebookInstanceLifecycleConfig(ctx, &awssm.CreateNotebookInstanceLifecycleConfigInput{
		NotebookInstanceLifecycleConfigName: aws.String("lc-1"),
		OnCreate:                            []smtypes.NotebookInstanceLifecycleHook{{Content: aws.String("b25jcmVhdGU=")}},
		OnStart:                             []smtypes.NotebookInstanceLifecycleHook{{Content: aws.String("b25zdGFydA==")}},
	})
	require.NoError(t, err)

	_, err = c.UpdateNotebookInstanceLifecycleConfig(ctx, &awssm.UpdateNotebookInstanceLifecycleConfigInput{
		NotebookInstanceLifecycleConfigName: aws.String("lc-1"),
		OnStart:                             []smtypes.NotebookInstanceLifecycleHook{{Content: aws.String("bmV3c3RhcnQ=")}},
	})
	require.NoError(t, err)

	d, err := c.DescribeNotebookInstanceLifecycleConfig(ctx, &awssm.DescribeNotebookInstanceLifecycleConfigInput{
		NotebookInstanceLifecycleConfigName: aws.String("lc-1"),
	})
	require.NoError(t, err)
	assert.Equal(t, "bmV3c3RhcnQ=", aws.ToString(d.OnStart[0].Content))
	assert.Equal(t, "b25jcmVhdGU=", aws.ToString(d.OnCreate[0].Content), "an omitted script is unchanged")
	assert.NotNil(t, d.LastModifiedTime)

	_, err = c.UpdateNotebookInstanceLifecycleConfig(ctx, &awssm.UpdateNotebookInstanceLifecycleConfigInput{
		NotebookInstanceLifecycleConfigName: aws.String("ghost"),
		OnStart:                             []smtypes.NotebookInstanceLifecycleHook{{Content: aws.String("eA==")}},
	})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())
}

func TestSDKDescribeCluster_InstanceGroupFields(t *testing.T) { // T47
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateCluster(ctx, &awssm.CreateClusterInput{
		ClusterName: aws.String("hp-1"),
		InstanceGroups: []smtypes.ClusterInstanceGroupSpecification{{
			InstanceGroupName: aws.String("workers"),
			InstanceType:      smtypes.ClusterInstanceTypeMlG5Xlarge,
			InstanceCount:     aws.Int32(3),
			MinInstanceCount:  aws.Int32(1),
			ThreadsPerCore:    aws.Int32(2),
			ExecutionRole:     aws.String("arn:aws:iam::123456789012:role/hp"),
			LifeCycleConfig:   &smtypes.ClusterLifeCycleConfig{SourceS3Uri: aws.String("s3://b/lc"), OnCreate: aws.String("on_create.sh")},
		}},
	})
	require.NoError(t, err)

	d, err := c.DescribeCluster(ctx, &awssm.DescribeClusterInput{ClusterName: aws.String("hp-1")})
	require.NoError(t, err)
	require.Len(t, d.InstanceGroups, 1)

	g := d.InstanceGroups[0]
	assert.Equal(t, int32(3), aws.ToInt32(g.CurrentCount))
	assert.Equal(t, int32(3), aws.ToInt32(g.TargetCount))
	assert.Equal(t, int32(1), aws.ToInt32(g.MinCount))
	assert.Equal(t, int32(2), aws.ToInt32(g.ThreadsPerCore))
	assert.Equal(t, smtypes.InstanceGroupStatusInservice, g.Status)
	require.NotNil(t, g.LifeCycleConfig)
	assert.Equal(t, "s3://b/lc", aws.ToString(g.LifeCycleConfig.SourceS3Uri))

	_, err = c.UpdateCluster(ctx, &awssm.UpdateClusterInput{
		ClusterName: aws.String("hp-1"),
		InstanceGroups: []smtypes.ClusterInstanceGroupSpecification{{
			InstanceGroupName: aws.String("extra"),
			InstanceType:      smtypes.ClusterInstanceTypeMlG5Xlarge,
			InstanceCount:     aws.Int32(2),
			ExecutionRole:     aws.String("arn:aws:iam::123456789012:role/hp"),
			LifeCycleConfig:   &smtypes.ClusterLifeCycleConfig{SourceS3Uri: aws.String("s3://b/lc"), OnCreate: aws.String("x.sh")},
		}},
	})
	require.NoError(t, err)

	d, err = c.DescribeCluster(ctx, &awssm.DescribeClusterInput{ClusterName: aws.String("hp-1")})
	require.NoError(t, err)
	assert.Len(t, d.InstanceGroups, 2, "UpdateCluster merges by name: the existing group is kept")

	_, err = c.UpdateCluster(ctx, &awssm.UpdateClusterInput{
		ClusterName: aws.String("hp-1"), InstanceGroupsToDelete: []string{"workers"},
		InstanceGroups: []smtypes.ClusterInstanceGroupSpecification{},
	})
	require.NoError(t, err)

	d, err = c.DescribeCluster(ctx, &awssm.DescribeClusterInput{ClusterName: aws.String("hp-1")})
	require.NoError(t, err)
	require.Len(t, d.InstanceGroups, 1)
	assert.Equal(t, "extra", aws.ToString(d.InstanceGroups[0].InstanceGroupName))

	_, err = c.DescribeCluster(ctx, &awssm.DescribeClusterInput{ClusterName: aws.String("ghost")})

	var nf *smtypes.ResourceNotFound

	require.ErrorAs(t, err, &nf, "DescribeCluster models ResourceNotFound")

	nodes, err := c.ListClusterNodes(ctx, &awssm.ListClusterNodesInput{ClusterName: aws.String("hp-1")})
	require.NoError(t, err)
	assert.Len(t, nodes.ClusterNodeSummaries, 2)
}

func TestSDKDescribeTrainingJob_AllMembers(t *testing.T) { // T60
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateTrainingJob(ctx, &awssm.CreateTrainingJobInput{
		TrainingJobName: aws.String("full-1"),
		RoleArn:         aws.String("arn:aws:iam::123456789012:role/svc"),
		AlgorithmSpecification: &smtypes.AlgorithmSpecification{
			TrainingImage: aws.String("xgboost:1"), TrainingInputMode: smtypes.TrainingInputModePipe,
		},
		InputDataConfig: []smtypes.Channel{{
			ChannelName: aws.String("train"),
			ContentType: aws.String("text/csv"),
			DataSource: &smtypes.DataSource{S3DataSource: &smtypes.S3DataSource{
				S3DataType: smtypes.S3DataTypeS3Prefix, S3Uri: aws.String("s3://b/train"),
				S3DataDistributionType: smtypes.S3DataDistributionShardedByS3Key,
			}},
		}},
		OutputDataConfig:       &smtypes.OutputDataConfig{S3OutputPath: aws.String("s3://b/out"), KmsKeyId: aws.String("key-1")},
		ResourceConfig:         &smtypes.ResourceConfig{InstanceType: smtypes.TrainingInstanceTypeMlM5Large, InstanceCount: aws.Int32(2), VolumeSizeInGB: aws.Int32(30)},
		StoppingCondition:      &smtypes.StoppingCondition{MaxRuntimeInSeconds: aws.Int32(7200)},
		VpcConfig:              &smtypes.VpcConfig{SecurityGroupIds: []string{"sg-1"}, Subnets: []string{"subnet-1", "subnet-2"}},
		Environment:            map[string]string{"STAGE": "test"},
		HyperParameters:        map[string]string{"eta": "0.2"},
		EnableNetworkIsolation: aws.Bool(true),
		Tags:                   []smtypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	require.NoError(t, err)

	d, err := c.DescribeTrainingJob(ctx, &awssm.DescribeTrainingJobInput{TrainingJobName: aws.String("full-1")})
	require.NoError(t, err)

	assert.Equal(t, smtypes.TrainingInputModePipe, d.AlgorithmSpecification.TrainingInputMode, "the request's input mode is echoed, not forced to File")
	require.Len(t, d.InputDataConfig, 1)
	assert.Equal(t, "train", aws.ToString(d.InputDataConfig[0].ChannelName))
	assert.Equal(t, "s3://b/train", aws.ToString(d.InputDataConfig[0].DataSource.S3DataSource.S3Uri))
	assert.Equal(t, smtypes.S3DataDistributionShardedByS3Key, d.InputDataConfig[0].DataSource.S3DataSource.S3DataDistributionType)
	assert.Equal(t, int32(7200), aws.ToInt32(d.StoppingCondition.MaxRuntimeInSeconds))
	require.NotNil(t, d.VpcConfig)
	assert.Equal(t, []string{"subnet-1", "subnet-2"}, d.VpcConfig.Subnets)
	assert.Equal(t, map[string]string{"STAGE": "test"}, d.Environment)
	assert.Equal(t, "key-1", aws.ToString(d.OutputDataConfig.KmsKeyId))
	assert.Equal(t, int32(2), aws.ToInt32(d.ResourceConfig.InstanceCount))
	assert.True(t, aws.ToBool(d.EnableNetworkIsolation))
	assert.Equal(t, map[string]string{"eta": "0.2"}, d.HyperParameters)

	// computed members
	assert.Equal(t, smtypes.TrainingJobStatusCompleted, d.TrainingJobStatus)
	assert.Equal(t, smtypes.SecondaryStatusCompleted, d.SecondaryStatus)
	require.NotNil(t, d.TrainingStartTime)
	require.NotNil(t, d.TrainingEndTime)
	assert.True(t, d.TrainingEndTime.After(*d.TrainingStartTime))
	assert.GreaterOrEqual(t, aws.ToInt32(d.TrainingTimeInSeconds), int32(1))
	assert.GreaterOrEqual(t, aws.ToInt32(d.BillableTimeInSeconds), int32(1))
	require.Len(t, d.SecondaryStatusTransitions, 5)

	for i := 1; i < len(d.SecondaryStatusTransitions); i++ {
		prev, cur := d.SecondaryStatusTransitions[i-1], d.SecondaryStatusTransitions[i]
		assert.True(t, cur.StartTime.After(*prev.StartTime), "transition %d starts after %d", i, i-1)
		assert.NotEmpty(t, aws.ToString(cur.StatusMessage))
	}

	assert.NotNil(t, d.LastModifiedTime)
	assert.NotNil(t, d.CreationTime)
	assert.Equal(t, "s3://b/out/output/model.tar.gz", aws.ToString(d.ModelArtifacts.S3ModelArtifacts))

	l, err := c.ListTrainingJobs(ctx, &awssm.ListTrainingJobsInput{})
	require.NoError(t, err)
	require.Len(t, l.TrainingJobSummaries, 1)
	assert.NotNil(t, l.TrainingJobSummaries[0].TrainingEndTime)
	assert.NotNil(t, l.TrainingJobSummaries[0].LastModifiedTime)
	assert.Equal(t, smtypes.SecondaryStatusCompleted, l.TrainingJobSummaries[0].SecondaryStatus)
}

func TestSDKDescribeJobs_RequiredMembers(t *testing.T) { // T61
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateProcessingJob(ctx, &awssm.CreateProcessingJobInput{
		ProcessingJobName: aws.String("pj-1"),
		RoleArn:           aws.String("arn:aws:iam::123456789012:role/svc"),
		AppSpecification:  &smtypes.AppSpecification{ImageUri: aws.String("proc:1"), ContainerEntrypoint: []string{"python3"}},
		ProcessingResources: &smtypes.ProcessingResources{ClusterConfig: &smtypes.ProcessingClusterConfig{
			InstanceCount: aws.Int32(1), InstanceType: smtypes.ProcessingInstanceTypeMlM5Large, VolumeSizeInGB: aws.Int32(10),
		}},
		Environment:       map[string]string{"A": "1"},
		StoppingCondition: &smtypes.ProcessingStoppingCondition{MaxRuntimeInSeconds: aws.Int32(600)},
	})
	require.NoError(t, err)

	p, err := c.DescribeProcessingJob(ctx, &awssm.DescribeProcessingJobInput{ProcessingJobName: aws.String("pj-1")})
	require.NoError(t, err)
	require.NotNil(t, p.AppSpecification)
	assert.Equal(t, []string{"python3"}, p.AppSpecification.ContainerEntrypoint)
	require.NotNil(t, p.ProcessingResources.ClusterConfig)
	assert.Equal(t, int32(10), aws.ToInt32(p.ProcessingResources.ClusterConfig.VolumeSizeInGB))
	assert.Equal(t, map[string]string{"A": "1"}, p.Environment)
	assert.Equal(t, int32(600), aws.ToInt32(p.StoppingCondition.MaxRuntimeInSeconds))
	assert.NotNil(t, p.ProcessingEndTime)
	assert.NotNil(t, p.LastModifiedTime)

	// transform
	_, err = c.CreateModel(ctx, &awssm.CreateModelInput{
		ModelName: aws.String("m-x"), ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
		PrimaryContainer: &smtypes.ContainerDefinition{Image: aws.String("img:latest")},
	})
	require.NoError(t, err)

	_, err = c.CreateTransformJob(ctx, &awssm.CreateTransformJobInput{
		TransformJobName: aws.String("tj-1"), ModelName: aws.String("m-x"),
		TransformInput: &smtypes.TransformInput{
			ContentType: aws.String("text/csv"),
			DataSource:  &smtypes.TransformDataSource{S3DataSource: &smtypes.TransformS3DataSource{S3DataType: smtypes.S3DataTypeS3Prefix, S3Uri: aws.String("s3://in/")}},
		},
		TransformOutput:    &smtypes.TransformOutput{S3OutputPath: aws.String("s3://out/")},
		TransformResources: &smtypes.TransformResources{InstanceType: smtypes.TransformInstanceTypeMlM5Large, InstanceCount: aws.Int32(1)},
		BatchStrategy:      smtypes.BatchStrategyMultiRecord,
		MaxPayloadInMB:     aws.Int32(6),
	})
	require.NoError(t, err)

	x, err := c.DescribeTransformJob(ctx, &awssm.DescribeTransformJobInput{TransformJobName: aws.String("tj-1")})
	require.NoError(t, err)
	require.NotNil(t, x.TransformInput)
	assert.Equal(t, "text/csv", aws.ToString(x.TransformInput.ContentType))
	require.NotNil(t, x.TransformResources)
	assert.Equal(t, smtypes.BatchStrategyMultiRecord, x.BatchStrategy)
	assert.Equal(t, int32(6), aws.ToInt32(x.MaxPayloadInMB))
	assert.NotNil(t, x.TransformEndTime)

	// tuning: required config echoed plus computed counters
	_, err = c.CreateHyperParameterTuningJob(ctx, &awssm.CreateHyperParameterTuningJobInput{
		HyperParameterTuningJobName: aws.String("hpo-1"),
		HyperParameterTuningJobConfig: &smtypes.HyperParameterTuningJobConfig{
			Strategy:                         smtypes.HyperParameterTuningJobStrategyTypeRandom,
			ResourceLimits:                   &smtypes.ResourceLimits{MaxNumberOfTrainingJobs: aws.Int32(5), MaxParallelTrainingJobs: aws.Int32(2)},
			HyperParameterTuningJobObjective: &smtypes.HyperParameterTuningJobObjective{Type: smtypes.HyperParameterTuningJobObjectiveTypeMinimize, MetricName: aws.String("loss")},
			TrainingJobEarlyStoppingType:     smtypes.TrainingJobEarlyStoppingTypeAuto,
		},
	})
	require.NoError(t, err)

	h, err := c.DescribeHyperParameterTuningJob(ctx, &awssm.DescribeHyperParameterTuningJobInput{HyperParameterTuningJobName: aws.String("hpo-1")})
	require.NoError(t, err)
	require.NotNil(t, h.HyperParameterTuningJobConfig)
	assert.Equal(t, smtypes.TrainingJobEarlyStoppingTypeAuto, h.HyperParameterTuningJobConfig.TrainingJobEarlyStoppingType)
	assert.Equal(t, int32(5), aws.ToInt32(h.HyperParameterTuningJobConfig.ResourceLimits.MaxNumberOfTrainingJobs))
	require.NotNil(t, h.TrainingJobStatusCounters)
	assert.Equal(t, int32(5), aws.ToInt32(h.TrainingJobStatusCounters.Completed))
	require.NotNil(t, h.ObjectiveStatusCounters)
	assert.Equal(t, int32(5), aws.ToInt32(h.ObjectiveStatusCounters.Succeeded))
	require.NotNil(t, h.BestTrainingJob)
	assert.NotNil(t, h.HyperParameterTuningEndTime)

	hl, err := c.ListHyperParameterTuningJobs(ctx, &awssm.ListHyperParameterTuningJobsInput{})
	require.NoError(t, err)
	require.Len(t, hl.HyperParameterTuningJobSummaries, 1)
	assert.Equal(t, smtypes.HyperParameterTuningJobStrategyTypeRandom, hl.HyperParameterTuningJobSummaries[0].Strategy)
	assert.NotNil(t, hl.HyperParameterTuningJobSummaries[0].TrainingJobStatusCounters)

	// compilation: FailureReason is required and present even when empty
	_, err = c.CreateCompilationJob(ctx, &awssm.CreateCompilationJobInput{
		CompilationJobName: aws.String("cj-1"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/svc"),
		InputConfig:        &smtypes.InputConfig{S3Uri: aws.String("s3://in/model.tar.gz"), Framework: smtypes.FrameworkPytorch, DataInputConfig: aws.String(`{"input":[1,3,224,224]}`)},
		OutputConfig:       &smtypes.OutputConfig{S3OutputLocation: aws.String("s3://out/"), TargetDevice: smtypes.TargetDeviceMlC5},
		StoppingCondition:  &smtypes.StoppingCondition{MaxRuntimeInSeconds: aws.Int32(300)},
	})
	require.NoError(t, err)

	cj, err := c.DescribeCompilationJob(ctx, &awssm.DescribeCompilationJobInput{CompilationJobName: aws.String("cj-1")})
	require.NoError(t, err)
	require.NotNil(t, cj.FailureReason)
	assert.Empty(t, aws.ToString(cj.FailureReason))
	assert.Equal(t, smtypes.TargetDeviceMlC5, cj.OutputConfig.TargetDevice)
	assert.Equal(t, int32(300), aws.ToInt32(cj.StoppingCondition.MaxRuntimeInSeconds))
	assert.Equal(t, `{"input":[1,3,224,224]}`, aws.ToString(cj.InputConfig.DataInputConfig))
	assert.NotNil(t, cj.ModelArtifacts)
	assert.NotNil(t, cj.CompilationEndTime)
	assert.NotNil(t, cj.LastModifiedTime)

	// labeling: required counters present
	_, err = c.CreateLabelingJob(ctx, &awssm.CreateLabelingJobInput{
		LabelingJobName:    aws.String("lj-1"),
		LabelAttributeName: aws.String("label"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/svc"),
		InputConfig:        &smtypes.LabelingJobInputConfig{DataSource: &smtypes.LabelingJobDataSource{}},
		OutputConfig:       &smtypes.LabelingJobOutputConfig{S3OutputPath: aws.String("s3://out/")},
		HumanTaskConfig: &smtypes.HumanTaskConfig{
			WorkteamArn:                       aws.String("arn:aws:sagemaker:us-east-1:123456789012:workteam/x"),
			UiConfig:                          &smtypes.UiConfig{UiTemplateS3Uri: aws.String("s3://tmpl/ui.html")},
			TaskTitle:                         aws.String("Label images"),
			TaskDescription:                   aws.String("Draw boxes"),
			NumberOfHumanWorkersPerDataObject: aws.Int32(3),
			TaskTimeLimitInSeconds:            aws.Int32(600),
		},
	})
	require.NoError(t, err)

	lj, err := c.DescribeLabelingJob(ctx, &awssm.DescribeLabelingJobInput{LabelingJobName: aws.String("lj-1")})
	require.NoError(t, err)
	require.NotNil(t, lj.LabelCounters)
	assert.NotEmpty(t, aws.ToString(lj.JobReferenceCode))
	assert.Equal(t, int32(3), aws.ToInt32(lj.HumanTaskConfig.NumberOfHumanWorkersPerDataObject), "the request's HumanTaskConfig is echoed")
	assert.NotNil(t, lj.LastModifiedTime)
}

func TestSDKBatchDescribeModelPackage(t *testing.T) { // T69
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateModelPackageGroup(ctx, &awssm.CreateModelPackageGroupInput{ModelPackageGroupName: aws.String("grp")})
	require.NoError(t, err)

	p, err := c.CreateModelPackage(ctx, &awssm.CreateModelPackageInput{
		ModelPackageGroupName: aws.String("grp"),
		InferenceSpecification: &smtypes.InferenceSpecification{
			Containers:                 []smtypes.ModelPackageContainerDefinition{{Image: aws.String("img:1"), ModelDataUrl: aws.String("s3://b/m.tgz")}},
			SupportedContentTypes:      []string{"application/json"},
			SupportedResponseMIMETypes: []string{"application/json"},
		},
	})
	require.NoError(t, err)

	missing := "arn:aws:sagemaker:us-east-1:123456789012:model-package/grp/42"

	out, err := c.BatchDescribeModelPackage(ctx, &awssm.BatchDescribeModelPackageInput{ModelPackageArnList: []string{aws.ToString(p.ModelPackageArn), missing}})
	require.NoError(t, err)
	require.Len(t, out.ModelPackageSummaries, 1)

	s := out.ModelPackageSummaries[aws.ToString(p.ModelPackageArn)]
	assert.Equal(t, "grp", aws.ToString(s.ModelPackageGroupName))
	assert.Equal(t, int32(1), aws.ToInt32(s.ModelPackageVersion))
	require.NotNil(t, s.InferenceSpecification)
	require.Len(t, s.InferenceSpecification.Containers, 1)
	assert.Equal(t, "img:1", aws.ToString(s.InferenceSpecification.Containers[0].Image))
	assert.Equal(t, smtypes.ModelPackageStatusCompleted, s.ModelPackageStatus)
	assert.NotNil(t, s.CreationTime)

	require.Contains(t, out.BatchDescribeModelPackageErrorMap, missing)
	assert.NotEmpty(t, aws.ToString(out.BatchDescribeModelPackageErrorMap[missing].ErrorCode))

	_, err = c.BatchDescribeModelPackage(ctx, &awssm.BatchDescribeModelPackageInput{ModelPackageArnList: []string{"nope"}})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())
}

func TestSDKModelPackageGroupPolicy_CRUD(t *testing.T) { // T71
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateModelPackageGroup(ctx, &awssm.CreateModelPackageGroupInput{ModelPackageGroupName: aws.String("grp")})
	require.NoError(t, err)

	_, err = c.GetModelPackageGroupPolicy(ctx, &awssm.GetModelPackageGroupPolicyInput{ModelPackageGroupName: aws.String("grp")})
	require.Error(t, err, "no policy yet")

	policy := `{"Version":"2012-10-17","Statement":[]}`

	put, err := c.PutModelPackageGroupPolicy(ctx, &awssm.PutModelPackageGroupPolicyInput{
		ModelPackageGroupName: aws.String("grp"), ResourcePolicy: aws.String(policy),
	})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(put.ModelPackageGroupArn), "model-package-group/grp")

	got, err := c.GetModelPackageGroupPolicy(ctx, &awssm.GetModelPackageGroupPolicyInput{ModelPackageGroupName: aws.String("grp")})
	require.NoError(t, err)
	assert.Equal(t, policy, aws.ToString(got.ResourcePolicy))

	_, err = c.DeleteModelPackageGroupPolicy(ctx, &awssm.DeleteModelPackageGroupPolicyInput{ModelPackageGroupName: aws.String("grp")})
	require.NoError(t, err)

	_, err = c.GetModelPackageGroupPolicy(ctx, &awssm.GetModelPackageGroupPolicyInput{ModelPackageGroupName: aws.String("grp")})
	require.Error(t, err)

	_, err = c.PutModelPackageGroupPolicy(ctx, &awssm.PutModelPackageGroupPolicyInput{
		ModelPackageGroupName: aws.String("ghost"), ResourcePolicy: aws.String(policy),
	})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())
	assert.Contains(t, ae.ErrorMessage(), "Could not find model package group")

	_, err = c.PutModelPackageGroupPolicy(ctx, &awssm.PutModelPackageGroupPolicyInput{
		ModelPackageGroupName: aws.String("grp"), ResourcePolicy: aws.String("not json"),
	})
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())
}

func TestSDKProjects_CRUD(t *testing.T) { // T101
	c := smClient(t)
	ctx := context.Background()

	out, err := c.CreateProject(ctx, &awssm.CreateProjectInput{
		ProjectName:        aws.String("proj-1"),
		ProjectDescription: aws.String("first"),
		ServiceCatalogProvisioningDetails: &smtypes.ServiceCatalogProvisioningDetails{
			ProductId: aws.String("prod-1"), ProvisioningArtifactId: aws.String("pa-1"),
			ProvisioningParameters: []smtypes.ProvisioningParameter{{Key: aws.String("k"), Value: aws.String("v")}},
		},
		Tags: []smtypes.Tag{{Key: aws.String("team"), Value: aws.String("ml")}},
	})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(out.ProjectArn), "project/proj-1")
	assert.True(t, strings.HasPrefix(aws.ToString(out.ProjectId), "p-"))

	d, err := c.DescribeProject(ctx, &awssm.DescribeProjectInput{ProjectName: aws.String("proj-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.ProjectStatusCreateCompleted, d.ProjectStatus)
	assert.Equal(t, "first", aws.ToString(d.ProjectDescription))
	assert.Equal(t, "prod-1", aws.ToString(d.ServiceCatalogProvisioningDetails.ProductId))
	require.NotNil(t, d.ServiceCatalogProvisionedProductDetails)
	assert.NotEmpty(t, aws.ToString(d.ServiceCatalogProvisionedProductDetails.ProvisionedProductId))
	assert.NotNil(t, d.CreationTime)

	_, err = c.UpdateProject(ctx, &awssm.UpdateProjectInput{
		ProjectName: aws.String("proj-1"), ProjectDescription: aws.String("second"),
		ServiceCatalogProvisioningUpdateDetails: &smtypes.ServiceCatalogProvisioningUpdateDetails{ProvisioningArtifactId: aws.String("pa-2")},
	})
	require.NoError(t, err)

	d, err = c.DescribeProject(ctx, &awssm.DescribeProjectInput{ProjectName: aws.String("proj-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.ProjectStatusUpdateCompleted, d.ProjectStatus)
	assert.Equal(t, "pa-2", aws.ToString(d.ServiceCatalogProvisioningDetails.ProvisioningArtifactId))

	l, err := c.ListProjects(ctx, &awssm.ListProjectsInput{NameContains: aws.String("proj"), SortBy: smtypes.ProjectSortByName})
	require.NoError(t, err)
	require.Len(t, l.ProjectSummaryList, 1)
	assert.Equal(t, smtypes.ProjectStatusUpdateCompleted, l.ProjectSummaryList[0].ProjectStatus)

	_, err = c.CreateProject(ctx, &awssm.CreateProjectInput{
		ProjectName:                       aws.String("proj-1"),
		ServiceCatalogProvisioningDetails: &smtypes.ServiceCatalogProvisioningDetails{ProductId: aws.String("prod-1")},
	})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode(), "a duplicate create is a ValidationException")

	_, err = c.DeleteProject(ctx, &awssm.DeleteProjectInput{ProjectName: aws.String("proj-1")})
	require.NoError(t, err)

	_, err = c.DescribeProject(ctx, &awssm.DescribeProjectInput{ProjectName: aws.String("proj-1")})
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())
	assert.Contains(t, ae.ErrorMessage(), "does not exist")
}

func TestSDKProjects_ConflictException(t *testing.T) { // T101
	url := newServerOpts(t, config.WithAsyncSettle())
	c := awssm.NewFromConfig(newCfg(t), func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })
	ctx := context.Background()

	_, err := c.CreateProject(ctx, &awssm.CreateProjectInput{
		ProjectName:                       aws.String("proj-1"),
		ServiceCatalogProvisioningDetails: &smtypes.ServiceCatalogProvisioningDetails{ProductId: aws.String("prod-1")},
	})
	require.NoError(t, err)

	_, err = c.DeleteProject(ctx, &awssm.DeleteProjectInput{ProjectName: aws.String("proj-1")})

	var ce *smtypes.ConflictException

	require.ErrorAs(t, err, &ce, "a project that is still being created cannot be deleted")
}

func TestSDKWorkforceAndWorkteam(t *testing.T) { // T103, T106, T108
	c := smClient(t)
	ctx := context.Background()

	wf, err := c.CreateWorkforce(ctx, &awssm.CreateWorkforceInput{
		WorkforceName:  aws.String("default"),
		CognitoConfig:  &smtypes.CognitoConfig{ClientId: aws.String("client-1"), UserPool: aws.String("us-east-1_abc")},
		SourceIpConfig: &smtypes.SourceIpConfig{Cidrs: []string{"10.0.0.0/24"}},
		Tags:           []smtypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(wf.WorkforceArn), "workforce/default")

	d, err := c.DescribeWorkforce(ctx, &awssm.DescribeWorkforceInput{WorkforceName: aws.String("default")})
	require.NoError(t, err)
	require.NotNil(t, d.Workforce)
	assert.Equal(t, smtypes.WorkforceStatusActive, d.Workforce.Status)
	assert.Equal(t, []string{"10.0.0.0/24"}, d.Workforce.SourceIpConfig.Cidrs)
	assert.Equal(t, "client-1", aws.ToString(d.Workforce.CognitoConfig.ClientId))
	assert.NotEmpty(t, aws.ToString(d.Workforce.SubDomain))

	_, err = c.CreateWorkforce(ctx, &awssm.CreateWorkforceInput{
		WorkforceName: aws.String("other"), CognitoConfig: &smtypes.CognitoConfig{ClientId: aws.String("c"), UserPool: aws.String("p")},
	})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode(), "a second workforce is rejected")

	tm, err := c.CreateWorkteam(ctx, &awssm.CreateWorkteamInput{
		WorkteamName: aws.String("team-1"), Description: aws.String("labelers"),
		MemberDefinitions: []smtypes.MemberDefinition{{CognitoMemberDefinition: &smtypes.CognitoMemberDefinition{
			ClientId: aws.String("client-1"), UserPool: aws.String("us-east-1_abc"), UserGroup: aws.String("grp"),
		}}},
		NotificationConfiguration: &smtypes.NotificationConfiguration{NotificationTopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:t")},
	})
	require.NoError(t, err)

	wt, err := c.DescribeWorkteam(ctx, &awssm.DescribeWorkteamInput{WorkteamName: aws.String("team-1")})
	require.NoError(t, err)
	assert.Equal(t, aws.ToString(tm.WorkteamArn), aws.ToString(wt.Workteam.WorkteamArn))
	assert.Equal(t, "grp", aws.ToString(wt.Workteam.MemberDefinitions[0].CognitoMemberDefinition.UserGroup))
	assert.Equal(t, "arn:aws:sns:us-east-1:123456789012:t", aws.ToString(wt.Workteam.NotificationConfiguration.NotificationTopicArn))
	assert.NotEmpty(t, aws.ToString(wt.Workteam.WorkforceArn))
	assert.NotNil(t, wt.Workteam.CreateDate)

	lw, err := c.ListWorkteams(ctx, &awssm.ListWorkteamsInput{NameContains: aws.String("team"), SortBy: smtypes.ListWorkteamsSortByOptionsCreateDate})
	require.NoError(t, err)
	require.Len(t, lw.Workteams, 1)

	_, err = c.DeleteWorkforce(ctx, &awssm.DeleteWorkforceInput{WorkforceName: aws.String("default")})
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ResourceInUse", ae.ErrorCode(), "a workforce with workteams is ResourceInUse")

	uw, err := c.UpdateWorkteam(ctx, &awssm.UpdateWorkteamInput{WorkteamName: aws.String("team-1"), Description: aws.String("new")})
	require.NoError(t, err)
	assert.Equal(t, "new", aws.ToString(uw.Workteam.Description))

	_, err = c.CreateLabelingJob(ctx, &awssm.CreateLabelingJobInput{
		LabelingJobName: aws.String("lj-1"), LabelAttributeName: aws.String("label"),
		RoleArn:      aws.String("arn:aws:iam::123456789012:role/svc"),
		InputConfig:  &smtypes.LabelingJobInputConfig{DataSource: &smtypes.LabelingJobDataSource{}},
		OutputConfig: &smtypes.LabelingJobOutputConfig{S3OutputPath: aws.String("s3://out/")},
		HumanTaskConfig: &smtypes.HumanTaskConfig{
			WorkteamArn: tm.WorkteamArn, UiConfig: &smtypes.UiConfig{UiTemplateS3Uri: aws.String("s3://t/ui.html")},
			TaskTitle: aws.String("t"), TaskDescription: aws.String("d"),
			NumberOfHumanWorkersPerDataObject: aws.Int32(1), TaskTimeLimitInSeconds: aws.Int32(600),
		},
	})
	require.NoError(t, err)

	lj, err := c.ListLabelingJobsForWorkteam(ctx, &awssm.ListLabelingJobsForWorkteamInput{WorkteamArn: tm.WorkteamArn})
	require.NoError(t, err)
	require.Len(t, lj.LabelingJobSummaryList, 1)
	assert.Equal(t, "lj-1", aws.ToString(lj.LabelingJobSummaryList[0].LabelingJobName))
	assert.NotEmpty(t, aws.ToString(lj.LabelingJobSummaryList[0].JobReferenceCode))

	_, err = c.ListLabelingJobsForWorkteam(ctx, &awssm.ListLabelingJobsForWorkteamInput{WorkteamArn: aws.String("arn:aws:sagemaker:us-east-1:123456789012:workteam/private-crowd/ghost")})

	var nf *smtypes.ResourceNotFound

	require.ErrorAs(t, err, &nf)

	ss, err := c.ListSubscribedWorkteams(ctx, &awssm.ListSubscribedWorkteamsInput{})
	require.NoError(t, err)
	assert.Empty(t, ss.SubscribedWorkteams)

	del, err := c.DeleteWorkteam(ctx, &awssm.DeleteWorkteamInput{WorkteamName: aws.String("team-1")})
	require.NoError(t, err)
	assert.True(t, aws.ToBool(del.Success))

	del, err = c.DeleteWorkteam(ctx, &awssm.DeleteWorkteamInput{WorkteamName: aws.String("team-1")})
	require.NoError(t, err)
	assert.False(t, aws.ToBool(del.Success))

	uf, err := c.UpdateWorkforce(ctx, &awssm.UpdateWorkforceInput{
		WorkforceName: aws.String("default"), SourceIpConfig: &smtypes.SourceIpConfig{Cidrs: []string{"192.168.0.0/16"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"192.168.0.0/16"}, uf.Workforce.SourceIpConfig.Cidrs)

	lf, err := c.ListWorkforces(ctx, &awssm.ListWorkforcesInput{})
	require.NoError(t, err)
	require.Len(t, lf.Workforces, 1)

	_, err = c.DeleteWorkforce(ctx, &awssm.DeleteWorkforceInput{WorkforceName: aws.String("default")})
	require.NoError(t, err)

	_, err = c.DescribeWorkforce(ctx, &awssm.DescribeWorkforceInput{WorkforceName: aws.String("default")})
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())
}

func TestSDKImages_AllOperations(t *testing.T) { // T109, T112
	c := smClient(t)
	ctx := context.Background()

	img, err := c.CreateImage(ctx, &awssm.CreateImageInput{
		ImageName: aws.String("my-image"), RoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
		DisplayName: aws.String("My image"), Description: aws.String("first"),
		Tags: []smtypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(img.ImageArn), "image/my-image")

	_, err = c.CreateImage(ctx, &awssm.CreateImageInput{ImageName: aws.String("my-image"), RoleArn: aws.String("arn:aws:iam::123456789012:role/svc")})

	var inUse *smtypes.ResourceInUse

	require.ErrorAs(t, err, &inUse, "a duplicate image is ResourceInUse")

	d, err := c.DescribeImage(ctx, &awssm.DescribeImageInput{ImageName: aws.String("my-image")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.ImageStatusCreated, d.ImageStatus)
	assert.Equal(t, "My image", aws.ToString(d.DisplayName))
	assert.Equal(t, "arn:aws:iam::123456789012:role/svc", aws.ToString(d.RoleArn))

	_, err = c.UpdateImage(ctx, &awssm.UpdateImageInput{ImageName: aws.String("my-image"), Description: aws.String("second"), DeleteProperties: []string{"DisplayName"}})
	require.NoError(t, err)

	d, err = c.DescribeImage(ctx, &awssm.DescribeImageInput{ImageName: aws.String("my-image")})
	require.NoError(t, err)
	assert.Equal(t, "second", aws.ToString(d.Description))
	assert.Nil(t, d.DisplayName)

	// versions
	for i, alias := range []string{"stable", "beta"} {
		out, verr := c.CreateImageVersion(ctx, &awssm.CreateImageVersionInput{
			ImageName: aws.String("my-image"), BaseImage: aws.String("123456789012.dkr.ecr.us-east-1.amazonaws.com/repo:" + alias),
			ClientToken: aws.String("token-" + alias), Aliases: []string{alias}, MLFramework: aws.String("PyTorch 1.13"),
			JobType: smtypes.JobTypeTraining, Processor: smtypes.ProcessorGpu, Horovod: aws.Bool(true),
		})
		require.NoError(t, verr)
		assert.Contains(t, aws.ToString(out.ImageVersionArn), "image-version/my-image/"+strconv.Itoa(i+1))
	}

	dv, err := c.DescribeImageVersion(ctx, &awssm.DescribeImageVersionInput{ImageName: aws.String("my-image"), Alias: aws.String("stable")})
	require.NoError(t, err)
	assert.Equal(t, int32(1), aws.ToInt32(dv.Version))
	assert.Equal(t, smtypes.ImageVersionStatusCreated, dv.ImageVersionStatus)
	assert.Equal(t, smtypes.JobTypeTraining, dv.JobType)
	assert.Equal(t, smtypes.ProcessorGpu, dv.Processor)
	assert.True(t, aws.ToBool(dv.Horovod))
	assert.Contains(t, aws.ToString(dv.BaseImage), "repo:stable")

	latest, err := c.DescribeImageVersion(ctx, &awssm.DescribeImageVersionInput{ImageName: aws.String("my-image")})
	require.NoError(t, err)
	assert.Equal(t, int32(2), aws.ToInt32(latest.Version))

	_, err = c.UpdateImageVersion(ctx, &awssm.UpdateImageVersionInput{
		ImageName: aws.String("my-image"), Version: aws.Int32(1), AliasesToAdd: []string{"prod"}, ReleaseNotes: aws.String("n"),
	})
	require.NoError(t, err)

	aliases := awssm.NewListAliasesPaginator(c, &awssm.ListAliasesInput{ImageName: aws.String("my-image"), MaxResults: aws.Int32(1)})
	var got []string

	for aliases.HasMorePages() {
		page, perr := aliases.NextPage(ctx)
		require.NoError(t, perr)

		got = append(got, page.SageMakerImageVersionAliases...)
	}

	assert.Equal(t, []string{"beta", "prod", "stable"}, got)

	lv, err := c.ListImageVersions(ctx, &awssm.ListImageVersionsInput{ImageName: aws.String("my-image"), SortBy: smtypes.ImageVersionSortByVersion, SortOrder: smtypes.ImageVersionSortOrderDescending})
	require.NoError(t, err)
	require.Len(t, lv.ImageVersions, 2)
	assert.Equal(t, int32(2), aws.ToInt32(lv.ImageVersions[0].Version))

	li, err := c.ListImages(ctx, &awssm.ListImagesInput{NameContains: aws.String("my"), SortBy: smtypes.ImageSortByImageName, SortOrder: smtypes.ImageSortOrderAscending})
	require.NoError(t, err)
	require.Len(t, li.Images, 1)
	assert.Equal(t, smtypes.ImageStatusCreated, li.Images[0].ImageStatus)

	_, err = c.DeleteImageVersion(ctx, &awssm.DeleteImageVersionInput{ImageName: aws.String("my-image"), Alias: aws.String("beta")})
	require.NoError(t, err)

	_, err = c.DescribeImageVersion(ctx, &awssm.DescribeImageVersionInput{ImageName: aws.String("my-image"), Version: aws.Int32(2)})

	var nf *smtypes.ResourceNotFound

	require.ErrorAs(t, err, &nf)

	// app image configs
	_, err = c.CreateAppImageConfig(ctx, &awssm.CreateAppImageConfigInput{
		AppImageConfigName: aws.String("cfg-1"),
		KernelGatewayImageConfig: &smtypes.KernelGatewayImageConfig{
			KernelSpecs:      []smtypes.KernelSpec{{Name: aws.String("python3"), DisplayName: aws.String("Python 3")}},
			FileSystemConfig: &smtypes.FileSystemConfig{DefaultGid: aws.Int32(100), DefaultUid: aws.Int32(1000), MountPath: aws.String("/home/u")},
		},
	})
	require.NoError(t, err)

	ac, err := c.DescribeAppImageConfig(ctx, &awssm.DescribeAppImageConfigInput{AppImageConfigName: aws.String("cfg-1")})
	require.NoError(t, err)
	assert.Equal(t, "python3", aws.ToString(ac.KernelGatewayImageConfig.KernelSpecs[0].Name))
	assert.Equal(t, "/home/u", aws.ToString(ac.KernelGatewayImageConfig.FileSystemConfig.MountPath))

	_, err = c.UpdateAppImageConfig(ctx, &awssm.UpdateAppImageConfigInput{
		AppImageConfigName:       aws.String("cfg-1"),
		JupyterLabAppImageConfig: &smtypes.JupyterLabAppImageConfig{ContainerConfig: &smtypes.ContainerConfig{ContainerEntrypoint: []string{"jupyter"}}},
	})
	require.NoError(t, err)

	lc, err := c.ListAppImageConfigs(ctx, &awssm.ListAppImageConfigsInput{NameContains: aws.String("cfg"), SortBy: smtypes.AppImageConfigSortKeyLastModifiedTime})
	require.NoError(t, err)
	require.Len(t, lc.AppImageConfigs, 1)
	assert.NotNil(t, lc.AppImageConfigs[0].JupyterLabAppImageConfig)

	_, err = c.DeleteAppImageConfig(ctx, &awssm.DeleteAppImageConfigInput{AppImageConfigName: aws.String("cfg-1")})
	require.NoError(t, err)

	_, err = c.DescribeAppImageConfig(ctx, &awssm.DescribeAppImageConfigInput{AppImageConfigName: aws.String("cfg-1")})
	require.ErrorAs(t, err, &nf)

	// deleting the image removes its versions
	_, err = c.DeleteImage(ctx, &awssm.DeleteImageInput{ImageName: aws.String("my-image")})
	require.NoError(t, err)

	_, err = c.ListImageVersions(ctx, &awssm.ListImageVersionsInput{ImageName: aws.String("my-image")})
	require.ErrorAs(t, err, &nf)

	_, err = c.DescribeImage(ctx, &awssm.DescribeImageInput{ImageName: aws.String("my-image")})
	require.ErrorAs(t, err, &nf)
}

func TestSDKTrialComponents_AllOperations(t *testing.T) { // T90, T92
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateExperiment(ctx, &awssm.CreateExperimentInput{ExperimentName: aws.String("exp-1")})
	require.NoError(t, err)
	_, err = c.CreateTrial(ctx, &awssm.CreateTrialInput{TrialName: aws.String("trial-1"), ExperimentName: aws.String("exp-1")})
	require.NoError(t, err)

	out, err := c.CreateTrialComponent(ctx, &awssm.CreateTrialComponentInput{
		TrialComponentName: aws.String("comp-1"), DisplayName: aws.String("first"),
		StartTime: aws.Time(time.Unix(1700000000, 0)),
		Status:    &smtypes.TrialComponentStatus{PrimaryStatus: smtypes.TrialComponentPrimaryStatusInProgress, Message: aws.String("go")},
		Parameters: map[string]smtypes.TrialComponentParameterValue{
			"lr":  &smtypes.TrialComponentParameterValueMemberNumberValue{Value: 0.1},
			"opt": &smtypes.TrialComponentParameterValueMemberStringValue{Value: "adam"},
		},
		InputArtifacts:     map[string]smtypes.TrialComponentArtifact{"train": {Value: aws.String("s3://b/train"), MediaType: aws.String("text/csv")}},
		OutputArtifacts:    map[string]smtypes.TrialComponentArtifact{"model": {Value: aws.String("s3://b/model")}},
		MetadataProperties: &smtypes.MetadataProperties{CommitId: aws.String("abc123"), Repository: aws.String("repo")},
	})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(out.TrialComponentArn), "experiment-trial-component/comp-1")

	d, err := c.DescribeTrialComponent(ctx, &awssm.DescribeTrialComponentInput{TrialComponentName: aws.String("comp-1")})
	require.NoError(t, err)
	assert.Equal(t, "first", aws.ToString(d.DisplayName))
	assert.Equal(t, smtypes.TrialComponentPrimaryStatusInProgress, d.Status.PrimaryStatus)
	assert.Equal(t, time.Unix(1700000000, 0).UTC(), d.StartTime.UTC())
	num, ok := d.Parameters["lr"].(*smtypes.TrialComponentParameterValueMemberNumberValue)
	require.True(t, ok)
	assert.InDelta(t, 0.1, num.Value, 0.0001)
	str, ok := d.Parameters["opt"].(*smtypes.TrialComponentParameterValueMemberStringValue)
	require.True(t, ok)
	assert.Equal(t, "adam", str.Value)
	assert.Equal(t, "s3://b/train", aws.ToString(d.InputArtifacts["train"].Value))
	assert.Equal(t, "abc123", aws.ToString(d.MetadataProperties.CommitId))

	_, err = c.UpdateTrialComponent(ctx, &awssm.UpdateTrialComponentInput{
		TrialComponentName: aws.String("comp-1"), ParametersToRemove: []string{"lr"},
		Status:     &smtypes.TrialComponentStatus{PrimaryStatus: smtypes.TrialComponentPrimaryStatusCompleted},
		Parameters: map[string]smtypes.TrialComponentParameterValue{"epochs": &smtypes.TrialComponentParameterValueMemberNumberValue{Value: 3}},
	})
	require.NoError(t, err)

	d, err = c.DescribeTrialComponent(ctx, &awssm.DescribeTrialComponentInput{TrialComponentName: aws.String("comp-1")})
	require.NoError(t, err)
	assert.Len(t, d.Parameters, 2)
	assert.Equal(t, smtypes.TrialComponentPrimaryStatusCompleted, d.Status.PrimaryStatus)

	assoc, err := c.AssociateTrialComponent(ctx, &awssm.AssociateTrialComponentInput{TrialComponentName: aws.String("comp-1"), TrialName: aws.String("trial-1")})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(assoc.TrialArn), "experiment-trial/trial-1")

	byTrial, err := c.ListTrialComponents(ctx, &awssm.ListTrialComponentsInput{TrialName: aws.String("trial-1")})
	require.NoError(t, err)
	require.Len(t, byTrial.TrialComponentSummaries, 1)
	assert.Equal(t, "comp-1", aws.ToString(byTrial.TrialComponentSummaries[0].TrialComponentName))

	_, err = c.ListTrialComponents(ctx, &awssm.ListTrialComponentsInput{TrialName: aws.String("trial-1"), ExperimentName: aws.String("exp-1")})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())

	pager := awssm.NewListTrialComponentsPaginator(c, &awssm.ListTrialComponentsInput{MaxResults: aws.Int32(1)})
	pages := 0

	for pager.HasMorePages() {
		_, perr := pager.NextPage(ctx)
		require.NoError(t, perr)

		pages++
	}

	assert.Equal(t, 1, pages)

	_, err = c.DeleteTrialComponent(ctx, &awssm.DeleteTrialComponentInput{TrialComponentName: aws.String("comp-1")})
	require.ErrorAs(t, err, &ae, "an associated component cannot be deleted")
	assert.Equal(t, "ValidationException", ae.ErrorCode())

	_, err = c.DeleteTrial(ctx, &awssm.DeleteTrialInput{TrialName: aws.String("trial-1")})
	require.Error(t, err, "a trial with components cannot be deleted")

	_, err = c.DisassociateTrialComponent(ctx, &awssm.DisassociateTrialComponentInput{TrialComponentName: aws.String("comp-1"), TrialName: aws.String("trial-1")})
	require.NoError(t, err)

	del, err := c.DeleteTrialComponent(ctx, &awssm.DeleteTrialComponentInput{TrialComponentName: aws.String("comp-1")})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(del.TrialComponentArn), "comp-1")

	var nf *smtypes.ResourceNotFound

	_, err = c.DescribeTrialComponent(ctx, &awssm.DescribeTrialComponentInput{TrialComponentName: aws.String("comp-1")})
	require.ErrorAs(t, err, &nf)

	_, err = c.DeleteExperiment(ctx, &awssm.DeleteExperimentInput{ExperimentName: aws.String("exp-1")})
	require.Error(t, err, "an experiment with trials cannot be deleted")
}

func TestSDKLineage_AllOperations(t *testing.T) { // T94, T96, T98, T99
	c := smClient(t)
	ctx := context.Background()

	_, err := c.DescribeLineageGroup(ctx, &awssm.DescribeLineageGroupInput{LineageGroupName: aws.String("sagemaker-default-lineage-group")})

	var nf *smtypes.ResourceNotFound

	require.ErrorAs(t, err, &nf, "no lineage group before the first entity")

	mkArtifact := func(name, uri string) string {
		out, aerr := c.CreateArtifact(ctx, &awssm.CreateArtifactInput{
			ArtifactName: aws.String(name), ArtifactType: aws.String("Dataset"),
			Source:     &smtypes.ArtifactSource{SourceUri: aws.String(uri), SourceTypes: []smtypes.ArtifactSourceType{{SourceIdType: smtypes.ArtifactSourceIdTypeS3Etag, Value: aws.String("abc")}}},
			Properties: map[string]string{"owner": "ml"},
		})
		require.NoError(t, aerr)

		return aws.ToString(out.ArtifactArn)
	}

	data, model := mkArtifact("data", "s3://data"), mkArtifact("model", "s3://model")

	_, err = c.CreateArtifact(ctx, &awssm.CreateArtifactInput{
		ArtifactName: aws.String("dup"), ArtifactType: aws.String("Dataset"), Source: &smtypes.ArtifactSource{SourceUri: aws.String("s3://data")},
	})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode(), "a duplicate source URI is rejected")

	da, err := c.DescribeArtifact(ctx, &awssm.DescribeArtifactInput{ArtifactArn: aws.String(data)})
	require.NoError(t, err)
	assert.Equal(t, "data", aws.ToString(da.ArtifactName))
	assert.Equal(t, "s3://data", aws.ToString(da.Source.SourceUri))
	assert.Equal(t, smtypes.ArtifactSourceIdTypeS3Etag, da.Source.SourceTypes[0].SourceIdType)
	assert.Equal(t, map[string]string{"owner": "ml"}, da.Properties)
	assert.NotEmpty(t, aws.ToString(da.LineageGroupArn))

	_, err = c.UpdateArtifact(ctx, &awssm.UpdateArtifactInput{ArtifactArn: aws.String(data), Properties: map[string]string{"stage": "prod"}})
	require.NoError(t, err)

	act, err := c.CreateAction(ctx, &awssm.CreateActionInput{
		ActionName: aws.String("train"), ActionType: aws.String("ModelTraining"),
		Source: &smtypes.ActionSource{SourceUri: aws.String("s3://a")}, Status: smtypes.ActionStatusInProgress, Description: aws.String("d"),
	})
	require.NoError(t, err)

	dact, err := c.DescribeAction(ctx, &awssm.DescribeActionInput{ActionName: aws.String("train")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.ActionStatusInProgress, dact.Status)

	_, err = c.UpdateAction(ctx, &awssm.UpdateActionInput{ActionName: aws.String("train"), Status: smtypes.ActionStatusCompleted})
	require.NoError(t, err)

	_, err = c.CreateContext(ctx, &awssm.CreateContextInput{
		ContextName: aws.String("my_ctx"), ContextType: aws.String("Endpoint"), Source: &smtypes.ContextSource{SourceUri: aws.String("arn:x")},
	})
	require.NoError(t, err)

	for _, assoc := range []struct{ src, dst *string }{{aws.String(data), act.ActionArn}, {act.ActionArn, aws.String(model)}} {
		_, err = c.AddAssociation(ctx, &awssm.AddAssociationInput{SourceArn: assoc.src, DestinationArn: assoc.dst, AssociationType: smtypes.AssociationEdgeTypeProduced})
		require.NoError(t, err)
	}

	_, err = c.AddAssociation(ctx, &awssm.AddAssociationInput{SourceArn: aws.String(data), DestinationArn: aws.String("arn:aws:sagemaker:us-east-1:123456789012:lineage-group/x")})
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())

	la, err := c.ListAssociations(ctx, &awssm.ListAssociationsInput{AssociationType: smtypes.AssociationEdgeTypeProduced, SortBy: smtypes.SortAssociationsByCreationTime})
	require.NoError(t, err)
	require.Len(t, la.AssociationSummaries, 2)
	assert.NotEmpty(t, aws.ToString(la.AssociationSummaries[0].SourceName))

	q, err := c.QueryLineage(ctx, &awssm.QueryLineageInput{
		StartArns: []string{data}, Direction: smtypes.DirectionDescendants, IncludeEdges: aws.Bool(true), MaxDepth: aws.Int32(5),
	})
	require.NoError(t, err)
	require.Len(t, q.Vertices, 2)
	assert.Equal(t, act.ActionArn, q.Vertices[0].Arn)
	assert.Equal(t, smtypes.LineageTypeAction, q.Vertices[0].LineageType)
	assert.Equal(t, model, aws.ToString(q.Vertices[1].Arn))
	assert.Equal(t, smtypes.LineageTypeArtifact, q.Vertices[1].LineageType)
	require.Len(t, q.Edges, 2)

	_, err = c.QueryLineage(ctx, &awssm.QueryLineageInput{StartArns: []string{data}, MaxDepth: aws.Int32(11)})
	require.ErrorAs(t, err, &ae)

	_, err = c.QueryLineage(ctx, &awssm.QueryLineageInput{StartArns: []string{"arn:aws:sagemaker:us-east-1:123456789012:artifact/ghost"}})
	require.ErrorAs(t, err, &nf)

	for _, call := range []func() error{
		func() error {
			_, e := c.ListArtifacts(ctx, &awssm.ListArtifactsInput{ArtifactType: aws.String("Dataset")})
			return e
		},
		func() error {
			_, e := c.ListActions(ctx, &awssm.ListActionsInput{ActionType: aws.String("ModelTraining")})
			return e
		},
		func() error {
			_, e := c.ListContexts(ctx, &awssm.ListContextsInput{SortBy: smtypes.SortContextsByName})
			return e
		},
	} {
		require.NoError(t, call())
	}

	lg, err := c.DescribeLineageGroup(ctx, &awssm.DescribeLineageGroupInput{LineageGroupName: aws.String("sagemaker-default-lineage-group")})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(lg.LineageGroupArn), "lineage-group/sagemaker-default-lineage-group")

	llg, err := c.ListLineageGroups(ctx, &awssm.ListLineageGroupsInput{})
	require.NoError(t, err)
	assert.Len(t, llg.LineageGroupSummaries, 1)

	pol, err := c.GetLineageGroupPolicy(ctx, &awssm.GetLineageGroupPolicyInput{LineageGroupName: aws.String("sagemaker-default-lineage-group")})
	require.NoError(t, err)
	assert.Equal(t, aws.ToString(lg.LineageGroupArn), aws.ToString(pol.LineageGroupArn))

	// deletes: an entity with associations is blocked until they are removed
	_, err = c.DeleteArtifact(ctx, &awssm.DeleteArtifactInput{ArtifactArn: aws.String(data)})
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())

	_, err = c.DeleteAssociation(ctx, &awssm.DeleteAssociationInput{SourceArn: aws.String(data), DestinationArn: act.ActionArn})
	require.NoError(t, err)

	_, err = c.DeleteArtifact(ctx, &awssm.DeleteArtifactInput{ArtifactArn: aws.String(data)})
	require.NoError(t, err)

	_, err = c.DeleteArtifact(ctx, &awssm.DeleteArtifactInput{Source: &smtypes.ArtifactSource{SourceUri: aws.String("s3://nothing")}})
	require.ErrorAs(t, err, &nf)

	_, err = c.DeleteContext(ctx, &awssm.DeleteContextInput{ContextName: aws.String("my_ctx")})
	require.NoError(t, err)

	_, err = c.DescribeContext(ctx, &awssm.DescribeContextInput{ContextName: aws.String("my_ctx")})
	require.ErrorAs(t, err, &nf)
}

func newCloudClient(t *testing.T, opts ...config.Option) (*awssm.Client, *awsprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS(opts...)
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{SageMaker: cloud.SageMaker, S3: cloud.S3}))
	t.Cleanup(ts.Close)

	return awssm.NewFromConfig(newCfg(t), func(o *awssm.Options) { o.BaseEndpoint = aws.String(ts.URL) }), cloud
}

func monitoringOutput() *smtypes.MonitoringOutputConfig {
	return &smtypes.MonitoringOutputConfig{MonitoringOutputs: []smtypes.MonitoringOutput{{
		S3Output: &smtypes.MonitoringS3Output{S3Uri: aws.String("s3://b/out"), LocalPath: aws.String("/opt/ml/processing/output")},
	}}}
}

func monitoringResources() *smtypes.MonitoringResources {
	return &smtypes.MonitoringResources{ClusterConfig: &smtypes.MonitoringClusterConfig{
		InstanceCount: aws.Int32(1), InstanceType: smtypes.ProcessingInstanceTypeMlM5Xlarge, VolumeSizeInGB: aws.Int32(20),
	}}
}

func endpointInput(name string) *smtypes.EndpointInput {
	return &smtypes.EndpointInput{EndpointName: aws.String(name), LocalPath: aws.String("/opt/ml/processing/input")}
}

func TestSDKMonitoringJobDefinitions_AllKinds(t *testing.T) { // T80
	c, _ := newCloudClient(t)
	ctx := context.Background()
	role := aws.String("arn:aws:iam::123456789012:role/svc")

	_, err := c.CreateDataQualityJobDefinition(ctx, &awssm.CreateDataQualityJobDefinitionInput{
		JobDefinitionName:           aws.String("dq-1"),
		RoleArn:                     role,
		DataQualityAppSpecification: &smtypes.DataQualityAppSpecification{ImageUri: aws.String("dq:1"), Environment: map[string]string{"A": "1"}},
		DataQualityJobInput:         &smtypes.DataQualityJobInput{EndpointInput: endpointInput("ep-1")},
		DataQualityJobOutputConfig:  monitoringOutput(),
		JobResources:                monitoringResources(),
		Tags:                        []smtypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	require.NoError(t, err)

	dq, err := c.DescribeDataQualityJobDefinition(ctx, &awssm.DescribeDataQualityJobDefinitionInput{JobDefinitionName: aws.String("dq-1")})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(dq.JobDefinitionArn), "data-quality-job-definition/dq-1")
	assert.Equal(t, "dq:1", aws.ToString(dq.DataQualityAppSpecification.ImageUri))
	assert.Equal(t, map[string]string{"A": "1"}, dq.DataQualityAppSpecification.Environment)
	assert.Equal(t, "ep-1", aws.ToString(dq.DataQualityJobInput.EndpointInput.EndpointName))
	assert.Equal(t, "s3://b/out", aws.ToString(dq.DataQualityJobOutputConfig.MonitoringOutputs[0].S3Output.S3Uri))
	assert.Equal(t, int32(20), aws.ToInt32(dq.JobResources.ClusterConfig.VolumeSizeInGB))
	assert.Equal(t, "arn:aws:iam::123456789012:role/svc", aws.ToString(dq.RoleArn))
	assert.NotNil(t, dq.CreationTime)

	_, err = c.CreateDataQualityJobDefinition(ctx, &awssm.CreateDataQualityJobDefinitionInput{
		JobDefinitionName: aws.String("dq-1"), RoleArn: role, DataQualityAppSpecification: &smtypes.DataQualityAppSpecification{ImageUri: aws.String("x")},
		DataQualityJobInput: &smtypes.DataQualityJobInput{EndpointInput: endpointInput("ep-1")}, DataQualityJobOutputConfig: monitoringOutput(), JobResources: monitoringResources(),
	})

	var inUse *smtypes.ResourceInUse

	require.ErrorAs(t, err, &inUse)

	_, err = c.CreateModelQualityJobDefinition(ctx, &awssm.CreateModelQualityJobDefinitionInput{
		JobDefinitionName: aws.String("mq-1"), RoleArn: role,
		ModelQualityAppSpecification: &smtypes.ModelQualityAppSpecification{ImageUri: aws.String("mq:1"), ProblemType: smtypes.MonitoringProblemTypeBinaryClassification},
		ModelQualityJobInput:         &smtypes.ModelQualityJobInput{GroundTruthS3Input: &smtypes.MonitoringGroundTruthS3Input{S3Uri: aws.String("s3://gt")}, EndpointInput: endpointInput("ep-1")},
		ModelQualityJobOutputConfig:  monitoringOutput(), JobResources: monitoringResources(),
	})
	require.NoError(t, err)

	mq, err := c.DescribeModelQualityJobDefinition(ctx, &awssm.DescribeModelQualityJobDefinitionInput{JobDefinitionName: aws.String("mq-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.MonitoringProblemTypeBinaryClassification, mq.ModelQualityAppSpecification.ProblemType)
	assert.Equal(t, "s3://gt", aws.ToString(mq.ModelQualityJobInput.GroundTruthS3Input.S3Uri))

	_, err = c.CreateModelBiasJobDefinition(ctx, &awssm.CreateModelBiasJobDefinitionInput{
		JobDefinitionName: aws.String("mb-1"), RoleArn: role,
		ModelBiasAppSpecification: &smtypes.ModelBiasAppSpecification{ImageUri: aws.String("mb:1"), ConfigUri: aws.String("s3://cfg")},
		ModelBiasJobInput:         &smtypes.ModelBiasJobInput{GroundTruthS3Input: &smtypes.MonitoringGroundTruthS3Input{S3Uri: aws.String("s3://gt")}, EndpointInput: endpointInput("ep-2")},
		ModelBiasJobOutputConfig:  monitoringOutput(), JobResources: monitoringResources(),
	})
	require.NoError(t, err)

	_, err = c.CreateModelExplainabilityJobDefinition(ctx, &awssm.CreateModelExplainabilityJobDefinitionInput{
		JobDefinitionName: aws.String("me-1"), RoleArn: role,
		ModelExplainabilityAppSpecification: &smtypes.ModelExplainabilityAppSpecification{ImageUri: aws.String("me:1"), ConfigUri: aws.String("s3://cfg")},
		ModelExplainabilityJobInput:         &smtypes.ModelExplainabilityJobInput{EndpointInput: endpointInput("ep-2")},
		ModelExplainabilityJobOutputConfig:  monitoringOutput(), JobResources: monitoringResources(),
	})
	require.NoError(t, err)

	me, err := c.DescribeModelExplainabilityJobDefinition(ctx, &awssm.DescribeModelExplainabilityJobDefinitionInput{JobDefinitionName: aws.String("me-1")})
	require.NoError(t, err)
	assert.Equal(t, "s3://cfg", aws.ToString(me.ModelExplainabilityAppSpecification.ConfigUri))

	ldq, err := c.ListDataQualityJobDefinitions(ctx, &awssm.ListDataQualityJobDefinitionsInput{EndpointName: aws.String("ep-1")})
	require.NoError(t, err)
	require.Len(t, ldq.JobDefinitionSummaries, 1)
	assert.Equal(t, "dq-1", aws.ToString(ldq.JobDefinitionSummaries[0].MonitoringJobDefinitionName))
	assert.Equal(t, "ep-1", aws.ToString(ldq.JobDefinitionSummaries[0].EndpointName))

	lmb, err := c.ListModelBiasJobDefinitions(ctx, &awssm.ListModelBiasJobDefinitionsInput{NameContains: aws.String("mb"), SortBy: smtypes.MonitoringJobDefinitionSortKeyName})
	require.NoError(t, err)
	assert.Len(t, lmb.JobDefinitionSummaries, 1)

	for name, call := range map[string]func() error{
		"dq": func() error {
			_, e := c.DeleteDataQualityJobDefinition(ctx, &awssm.DeleteDataQualityJobDefinitionInput{JobDefinitionName: aws.String("dq-1")})
			return e
		},
		"mq": func() error {
			_, e := c.DeleteModelQualityJobDefinition(ctx, &awssm.DeleteModelQualityJobDefinitionInput{JobDefinitionName: aws.String("mq-1")})
			return e
		},
		"mb": func() error {
			_, e := c.DeleteModelBiasJobDefinition(ctx, &awssm.DeleteModelBiasJobDefinitionInput{JobDefinitionName: aws.String("mb-1")})
			return e
		},
		"me": func() error {
			_, e := c.DeleteModelExplainabilityJobDefinition(ctx, &awssm.DeleteModelExplainabilityJobDefinitionInput{JobDefinitionName: aws.String("me-1")})
			return e
		},
	} {
		require.NoError(t, call(), name)
	}

	_, err = c.DescribeDataQualityJobDefinition(ctx, &awssm.DescribeDataQualityJobDefinitionInput{JobDefinitionName: aws.String("dq-1")})

	var nf *smtypes.ResourceNotFound

	require.ErrorAs(t, err, &nf)
}

func TestSDKMonitoringSchedule_ExecutionsAndAlerts(t *testing.T) { // T82, T83, T86, T88
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	c, cloud := newCloudClient(t, config.WithClock(fc))
	ctx := context.Background()
	role := aws.String("arn:aws:iam::123456789012:role/svc")

	_, err := c.CreateDataQualityJobDefinition(ctx, &awssm.CreateDataQualityJobDefinitionInput{
		JobDefinitionName: aws.String("dq-1"), RoleArn: role, DataQualityAppSpecification: &smtypes.DataQualityAppSpecification{ImageUri: aws.String("dq:1")},
		DataQualityJobInput: &smtypes.DataQualityJobInput{EndpointInput: endpointInput("ep-1")}, DataQualityJobOutputConfig: monitoringOutput(), JobResources: monitoringResources(),
	})
	require.NoError(t, err)

	// a schedule needs its endpoint
	_, err = c.CreateMonitoringSchedule(ctx, &awssm.CreateMonitoringScheduleInput{
		MonitoringScheduleName: aws.String("s-1"),
		MonitoringScheduleConfig: &smtypes.MonitoringScheduleConfig{
			MonitoringJobDefinitionName: aws.String("dq-1"), MonitoringType: smtypes.MonitoringTypeDataQuality,
			ScheduleConfig: &smtypes.ScheduleConfig{ScheduleExpression: aws.String("cron(0 * ? * * *)")},
		},
	})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode(), "the definition's endpoint does not exist yet")

	_, err = c.CreateModel(ctx, &awssm.CreateModelInput{
		ModelName: aws.String("m"), ExecutionRoleArn: role, PrimaryContainer: &smtypes.ContainerDefinition{Image: aws.String("i")},
	})
	require.NoError(t, err)
	_, err = c.CreateEndpointConfig(ctx, &awssm.CreateEndpointConfigInput{
		EndpointConfigName: aws.String("cfg"),
		ProductionVariants: []smtypes.ProductionVariant{{VariantName: aws.String("v"), ModelName: aws.String("m"), InstanceType: smtypes.ProductionVariantInstanceTypeMlM5Large, InitialInstanceCount: aws.Int32(1)}},
	})
	require.NoError(t, err)
	_, err = c.CreateEndpoint(ctx, &awssm.CreateEndpointInput{EndpointName: aws.String("ep-1"), EndpointConfigName: aws.String("cfg")})
	require.NoError(t, err)

	out, err := c.CreateMonitoringSchedule(ctx, &awssm.CreateMonitoringScheduleInput{
		MonitoringScheduleName: aws.String("s-1"),
		MonitoringScheduleConfig: &smtypes.MonitoringScheduleConfig{
			MonitoringJobDefinitionName: aws.String("dq-1"), MonitoringType: smtypes.MonitoringTypeDataQuality,
			ScheduleConfig: &smtypes.ScheduleConfig{ScheduleExpression: aws.String("cron(0 * ? * * *)")},
		},
		Tags: []smtypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(out.MonitoringScheduleArn), "monitoring-schedule/s-1")

	d, err := c.DescribeMonitoringSchedule(ctx, &awssm.DescribeMonitoringScheduleInput{MonitoringScheduleName: aws.String("s-1")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.ScheduleStatusScheduled, d.MonitoringScheduleStatus)
	assert.Equal(t, smtypes.MonitoringTypeDataQuality, d.MonitoringType)
	assert.Equal(t, "ep-1", aws.ToString(d.EndpointName))
	assert.Equal(t, "cron(0 * ? * * *)", aws.ToString(d.MonitoringScheduleConfig.ScheduleConfig.ScheduleExpression))
	assert.Nil(t, d.LastMonitoringExecutionSummary, "nothing has run yet")

	fc.Advance(3*time.Hour + 30*time.Minute)

	execs, err := c.ListMonitoringExecutions(ctx, &awssm.ListMonitoringExecutionsInput{
		MonitoringScheduleName: aws.String("s-1"), SortBy: smtypes.MonitoringExecutionSortKeyScheduledTime, SortOrder: smtypes.SortOrderAscending,
	})
	require.NoError(t, err)
	require.Len(t, execs.MonitoringExecutionSummaries, 3)
	assert.Equal(t, smtypes.ExecutionStatusCompleted, execs.MonitoringExecutionSummaries[0].MonitoringExecutionStatus)
	assert.Equal(t, time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC), execs.MonitoringExecutionSummaries[0].ScheduledTime.UTC())
	assert.Equal(t, "ep-1", aws.ToString(execs.MonitoringExecutionSummaries[0].EndpointName))
	assert.NotEmpty(t, aws.ToString(execs.MonitoringExecutionSummaries[0].ProcessingJobArn))

	d, err = c.DescribeMonitoringSchedule(ctx, &awssm.DescribeMonitoringScheduleInput{MonitoringScheduleName: aws.String("s-1")})
	require.NoError(t, err)
	require.NotNil(t, d.LastMonitoringExecutionSummary)
	assert.Equal(t, time.Date(2025, 1, 1, 3, 0, 0, 0, time.UTC), d.LastMonitoringExecutionSummary.ScheduledTime.UTC())

	paged := awssm.NewListMonitoringExecutionsPaginator(c, &awssm.ListMonitoringExecutionsInput{MaxResults: aws.Int32(1)})
	count := 0

	for paged.HasMorePages() {
		page, perr := paged.NextPage(ctx)
		require.NoError(t, perr)

		count += len(page.MonitoringExecutionSummaries)
	}

	assert.Equal(t, 3, count)

	_, err = c.StopMonitoringSchedule(ctx, &awssm.StopMonitoringScheduleInput{MonitoringScheduleName: aws.String("s-1")})
	require.NoError(t, err)
	fc.Advance(5 * time.Hour)

	execs, err = c.ListMonitoringExecutions(ctx, &awssm.ListMonitoringExecutionsInput{MonitoringScheduleName: aws.String("s-1")})
	require.NoError(t, err)
	assert.Len(t, execs.MonitoringExecutionSummaries, 3, "a stopped schedule produces no new executions")

	_, err = c.UpdateMonitoringSchedule(ctx, &awssm.UpdateMonitoringScheduleInput{
		MonitoringScheduleName: aws.String("s-1"),
		MonitoringScheduleConfig: &smtypes.MonitoringScheduleConfig{
			MonitoringJobDefinitionName: aws.String("dq-1"), MonitoringType: smtypes.MonitoringTypeDataQuality,
			ScheduleConfig: &smtypes.ScheduleConfig{ScheduleExpression: aws.String("cron(0 12 ? * * *)")},
		},
	})
	require.NoError(t, err)

	_, err = c.StartMonitoringSchedule(ctx, &awssm.StartMonitoringScheduleInput{MonitoringScheduleName: aws.String("s-1")})
	require.NoError(t, err)

	ls, err := c.ListMonitoringSchedules(ctx, &awssm.ListMonitoringSchedulesInput{
		EndpointName: aws.String("ep-1"), StatusEquals: smtypes.ScheduleStatusScheduled, MonitoringTypeEquals: smtypes.MonitoringTypeDataQuality,
	})
	require.NoError(t, err)
	require.Len(t, ls.MonitoringScheduleSummaries, 1)
	assert.Equal(t, "dq-1", aws.ToString(ls.MonitoringScheduleSummaries[0].MonitoringJobDefinitionName))

	// alerts: seeded (real SageMaker has no create API), then read and updated through the SDK
	require.NoError(t, cloud.SageMaker.PutMonitoringAlert("s-1", "alert-1", 1, 2))

	la, err := c.ListMonitoringAlerts(ctx, &awssm.ListMonitoringAlertsInput{MonitoringScheduleName: aws.String("s-1")})
	require.NoError(t, err)
	require.Len(t, la.MonitoringAlertSummaries, 1)
	assert.Equal(t, smtypes.MonitoringAlertStatusOk, la.MonitoringAlertSummaries[0].AlertStatus)
	assert.Equal(t, int32(2), aws.ToInt32(la.MonitoringAlertSummaries[0].EvaluationPeriod))

	ua, err := c.UpdateMonitoringAlert(ctx, &awssm.UpdateMonitoringAlertInput{
		MonitoringScheduleName: aws.String("s-1"), MonitoringAlertName: aws.String("alert-1"), DatapointsToAlert: aws.Int32(2), EvaluationPeriod: aws.Int32(3),
	})
	require.NoError(t, err)
	assert.Equal(t, "alert-1", aws.ToString(ua.MonitoringAlertName))

	_, err = c.UpdateMonitoringAlert(ctx, &awssm.UpdateMonitoringAlertInput{
		MonitoringScheduleName: aws.String("s-1"), MonitoringAlertName: aws.String("alert-1"), DatapointsToAlert: aws.Int32(0), EvaluationPeriod: aws.Int32(3),
	})
	require.ErrorAs(t, err, &ae)

	_, err = c.ListMonitoringAlertHistory(ctx, &awssm.ListMonitoringAlertHistoryInput{MonitoringScheduleName: aws.String("s-1")})
	require.NoError(t, err)

	_, err = c.DeleteMonitoringSchedule(ctx, &awssm.DeleteMonitoringScheduleInput{MonitoringScheduleName: aws.String("s-1")})
	require.NoError(t, err)

	var nf *smtypes.ResourceNotFound

	_, err = c.DescribeMonitoringSchedule(ctx, &awssm.DescribeMonitoringScheduleInput{MonitoringScheduleName: aws.String("s-1")})
	require.ErrorAs(t, err, &nf)

	history, err := c.ListMonitoringExecutions(ctx, &awssm.ListMonitoringExecutionsInput{MonitoringScheduleName: aws.String("s-1")})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(history.MonitoringExecutionSummaries), 3, "the execution history outlives the schedule")
}

func TestSDKSearch_AndSuggestions(t *testing.T) { // T114, T116, T117, T118
	c := smClient(t)
	ctx := context.Background()

	createTrainingJobs(t, c, "alpha", "beta", "gamma")

	for _, n := range []string{"alpha", "gamma"} {
		_, err := c.AddTags(ctx, &awssm.AddTagsInput{
			ResourceArn: aws.String("arn:aws:sagemaker:us-east-1:123456789012:training-job/" + n),
			Tags:        []smtypes.Tag{{Key: aws.String("team"), Value: aws.String("ml")}},
		})
		require.NoError(t, err)
	}

	res, err := c.Search(ctx, &awssm.SearchInput{
		Resource: smtypes.ResourceTypeTrainingJob,
		SearchExpression: &smtypes.SearchExpression{
			Filters: []smtypes.Filter{
				{Name: aws.String("Tags.team"), Operator: smtypes.OperatorEquals, Value: aws.String("ml")},
				{Name: aws.String("TrainingJobName"), Operator: smtypes.OperatorNotEquals, Value: aws.String("gamma")},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Results, 1)
	require.NotNil(t, res.Results[0].TrainingJob)
	assert.Equal(t, "alpha", aws.ToString(res.Results[0].TrainingJob.TrainingJobName))
	assert.Equal(t, smtypes.TrainingJobStatusCompleted, res.Results[0].TrainingJob.TrainingJobStatus)
	assert.NotNil(t, res.Results[0].TrainingJob.CreationTime)
	require.Len(t, res.Results[0].TrainingJob.Tags, 1)
	assert.Equal(t, "team", aws.ToString(res.Results[0].TrainingJob.Tags[0].Key))
	require.NotNil(t, res.TotalHits)
	assert.Equal(t, int64(1), aws.ToInt64(res.TotalHits.Value))
	assert.Equal(t, smtypes.RelationEqualTo, res.TotalHits.Relation)

	pager := awssm.NewSearchPaginator(c, &awssm.SearchInput{Resource: smtypes.ResourceTypeTrainingJob, MaxResults: aws.Int32(2), SortBy: aws.String("TrainingJobName"), SortOrder: smtypes.SearchSortOrderAscending})
	var names []string

	for pager.HasMorePages() {
		page, perr := pager.NextPage(ctx)
		require.NoError(t, perr)

		for _, r := range page.Results {
			names = append(names, aws.ToString(r.TrainingJob.TrainingJobName))
		}
	}

	assert.Equal(t, []string{"alpha", "beta", "gamma"}, names)

	_, err = c.Search(ctx, &awssm.SearchInput{
		Resource: smtypes.ResourceTypeTrainingJob,
		SearchExpression: &smtypes.SearchExpression{Filters: []smtypes.Filter{
			{Name: aws.String("TrainingJobName"), Operator: smtypes.OperatorContains, Value: aws.String("a")},
			{Name: aws.String("RoleArn"), Operator: smtypes.OperatorContains, Value: aws.String("r")},
		}},
	})

	var ae smithy.APIError

	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())
	assert.Contains(t, ae.ErrorMessage(), "'CONTAINS' operator usage limit of 1 exceeded.")

	_, err = c.Search(ctx, &awssm.SearchInput{
		Resource:         smtypes.ResourceTypeTrainingJob,
		SearchExpression: &smtypes.SearchExpression{Filters: []smtypes.Filter{{Name: aws.String("NoSuchProperty"), Value: aws.String("x")}}},
	})
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "ValidationException", ae.ErrorCode())

	empty, err := c.Search(ctx, &awssm.SearchInput{Resource: smtypes.ResourceTypeModelCard})
	require.NoError(t, err)
	assert.Empty(t, empty.Results, "a valid resource type with no data searches as empty")

	nested, err := c.Search(ctx, &awssm.SearchInput{
		Resource: smtypes.ResourceTypeTrainingJob,
		SearchExpression: &smtypes.SearchExpression{NestedFilters: []smtypes.NestedFilters{{
			NestedPropertyName: aws.String("Tags"),
			Filters:            []smtypes.Filter{{Name: aws.String("Tags.Key"), Operator: smtypes.OperatorEquals, Value: aws.String("team")}},
		}}},
	})
	require.NoError(t, err)
	assert.Len(t, nested.Results, 2)

	sug, err := c.GetSearchSuggestions(ctx, &awssm.GetSearchSuggestionsInput{
		Resource:        smtypes.ResourceTypeTrainingJob,
		SuggestionQuery: &smtypes.SuggestionQuery{PropertyNameQuery: &smtypes.PropertyNameQuery{PropertyNameHint: aws.String("tags")}},
	})
	require.NoError(t, err)
	require.Len(t, sug.PropertyNameSuggestions, 1)
	assert.Equal(t, "Tags.team", aws.ToString(sug.PropertyNameSuggestions[0].PropertyName))
}

func TestSDK_MalformedARN_BeforeLookup(t *testing.T) { // T24
	c := smClient(t)
	ctx := context.Background()

	calls := map[string]func() error{
		"ListTags": func() error {
			_, err := c.ListTags(ctx, &awssm.ListTagsInput{ResourceArn: aws.String("not-an-arn")})
			return err
		},
		"AddTags": func() error {
			_, err := c.AddTags(ctx, &awssm.AddTagsInput{
				ResourceArn: aws.String("not-an-arn"), Tags: []smtypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
			})
			return err
		},
		"DeleteTags": func() error {
			_, err := c.DeleteTags(ctx, &awssm.DeleteTagsInput{ResourceArn: aws.String("not-an-arn"), TagKeys: []string{"k"}})
			return err
		},
		"DescribePipelineExecution": func() error {
			_, err := c.DescribePipelineExecution(ctx, &awssm.DescribePipelineExecutionInput{PipelineExecutionArn: aws.String("not-an-arn")})
			return err
		},
		"DescribeArtifact": func() error {
			_, err := c.DescribeArtifact(ctx, &awssm.DescribeArtifactInput{ArtifactArn: aws.String("not-an-arn")})
			return err
		},
	}

	for op, call := range calls {
		err := call()
		require.Error(t, err, op)

		var ae smithy.APIError

		require.ErrorAs(t, err, &ae, op)
		assert.Equal(t, "ValidationException", ae.ErrorCode(), "%s: a malformed ARN is rejected before lookup", op)
	}
}

// TestSDKNotebook_DescribeDefaultsAndNetworkFields pins the members Terraform
// reads back after create (DirectInternetAccess defaults to Enabled) and that
// create-time network inputs round trip.
func TestSDKNotebook_DescribeDefaultsAndNetworkFields(t *testing.T) {
	c := smClient(t)
	ctx := context.Background()

	_, err := c.CreateNotebookInstance(ctx, &awssm.CreateNotebookInstanceInput{
		NotebookInstanceName: aws.String("nb-net"), InstanceType: smtypes.InstanceTypeMlT3Medium, RoleArn: aws.String("arn:aws:iam::123456789012:role/r"),
	})
	require.NoError(t, err)

	d, err := c.DescribeNotebookInstance(ctx, &awssm.DescribeNotebookInstanceInput{NotebookInstanceName: aws.String("nb-net")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.DirectInternetAccessEnabled, d.DirectInternetAccess)

	_, err = c.CreateNotebookInstance(ctx, &awssm.CreateNotebookInstanceInput{
		NotebookInstanceName: aws.String("nb-vpc"), InstanceType: smtypes.InstanceTypeMlT3Medium, RoleArn: aws.String("arn:aws:iam::123456789012:role/r"),
		SubnetId: aws.String("subnet-1"), SecurityGroupIds: []string{"sg-1"}, DirectInternetAccess: smtypes.DirectInternetAccessDisabled, KmsKeyId: aws.String("key-1"),
	})
	require.NoError(t, err)

	d, err = c.DescribeNotebookInstance(ctx, &awssm.DescribeNotebookInstanceInput{NotebookInstanceName: aws.String("nb-vpc")})
	require.NoError(t, err)
	assert.Equal(t, "subnet-1", aws.ToString(d.SubnetId))
	assert.Equal(t, []string{"sg-1"}, d.SecurityGroups)
	assert.Equal(t, smtypes.DirectInternetAccessDisabled, d.DirectInternetAccess)
	assert.Equal(t, "key-1", aws.ToString(d.KmsKeyId))
}
