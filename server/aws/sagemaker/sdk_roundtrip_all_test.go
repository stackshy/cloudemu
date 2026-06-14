package sagemaker_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	fsrt "github.com/aws/aws-sdk-go-v2/service/sagemakerfeaturestoreruntime"
	fsrttypes "github.com/aws/aws-sdk-go-v2/service/sagemakerfeaturestoreruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
