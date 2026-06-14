package sagemaker_test

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssm "github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	smrt "github.com/aws/aws-sdk-go-v2/service/sagemakerruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

func newServer(t *testing.T) string {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		SageMaker: cloud.SageMaker,
		// S3 included to exercise routing precedence: the runtime path
		// /endpoints/{name}/invocations must be claimed before S3's catch-all.
		S3: cloud.S3,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts.URL
}

func newCfg(t *testing.T) aws.Config {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	require.NoError(t, err)

	return cfg
}

func TestSDKTrainingJobLifecycle(t *testing.T) {
	url := newServer(t)
	client := awssm.NewFromConfig(newCfg(t), func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })
	ctx := context.Background()

	_, err := client.CreateTrainingJob(ctx, &awssm.CreateTrainingJobInput{
		TrainingJobName: aws.String("job-1"),
		RoleArn:         aws.String("arn:aws:iam::123456789012:role/svc"),
		AlgorithmSpecification: &smtypes.AlgorithmSpecification{
			TrainingImage:     aws.String("xgboost:latest"),
			TrainingInputMode: smtypes.TrainingInputModeFile,
		},
		OutputDataConfig: &smtypes.OutputDataConfig{S3OutputPath: aws.String("s3://bucket/out")},
		ResourceConfig: &smtypes.ResourceConfig{
			InstanceType:   smtypes.TrainingInstanceTypeMlM5Large,
			InstanceCount:  aws.Int32(1),
			VolumeSizeInGB: aws.Int32(10),
		},
		StoppingCondition: &smtypes.StoppingCondition{MaxRuntimeInSeconds: aws.Int32(3600)},
		Tags:              []smtypes.Tag{{Key: aws.String("team"), Value: aws.String("ml")}},
	})
	require.NoError(t, err)

	desc, err := client.DescribeTrainingJob(ctx, &awssm.DescribeTrainingJobInput{
		TrainingJobName: aws.String("job-1"),
	})
	require.NoError(t, err)
	assert.Equal(t, smtypes.TrainingJobStatusCompleted, desc.TrainingJobStatus)
	assert.Equal(t, "xgboost:latest", aws.ToString(desc.AlgorithmSpecification.TrainingImage))
	assert.Equal(t, "s3://bucket/out/output/model.tar.gz", aws.ToString(desc.ModelArtifacts.S3ModelArtifacts))

	list, err := client.ListTrainingJobs(ctx, &awssm.ListTrainingJobsInput{})
	require.NoError(t, err)
	assert.Len(t, list.TrainingJobSummaries, 1)

	_, err = client.StopTrainingJob(ctx, &awssm.StopTrainingJobInput{TrainingJobName: aws.String("job-1")})
	require.NoError(t, err)
}

func TestSDKInferenceFlowAndInvoke(t *testing.T) {
	url := newServer(t)
	cfg := newCfg(t)
	client := awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })
	rt := smrt.NewFromConfig(cfg, func(o *smrt.Options) { o.BaseEndpoint = aws.String(url) })
	ctx := context.Background()

	_, err := client.CreateModel(ctx, &awssm.CreateModelInput{
		ModelName:        aws.String("model-a"),
		ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
		PrimaryContainer: &smtypes.ContainerDefinition{
			Image:        aws.String("inference:latest"),
			ModelDataUrl: aws.String("s3://bucket/model.tar.gz"),
		},
	})
	require.NoError(t, err)

	_, err = client.CreateEndpointConfig(ctx, &awssm.CreateEndpointConfigInput{
		EndpointConfigName: aws.String("cfg-a"),
		ProductionVariants: []smtypes.ProductionVariant{{
			VariantName:          aws.String("v1"),
			ModelName:            aws.String("model-a"),
			InstanceType:         smtypes.ProductionVariantInstanceTypeMlM5Large,
			InitialInstanceCount: aws.Int32(1),
			InitialVariantWeight: aws.Float32(1),
		}},
	})
	require.NoError(t, err)

	_, err = client.CreateEndpoint(ctx, &awssm.CreateEndpointInput{
		EndpointName:       aws.String("ep-a"),
		EndpointConfigName: aws.String("cfg-a"),
	})
	require.NoError(t, err)

	desc, err := client.DescribeEndpoint(ctx, &awssm.DescribeEndpointInput{EndpointName: aws.String("ep-a")})
	require.NoError(t, err)
	assert.Equal(t, smtypes.EndpointStatusInService, desc.EndpointStatus)
	require.Len(t, desc.ProductionVariants, 1)
	assert.Equal(t, "v1", aws.ToString(desc.ProductionVariants[0].VariantName))

	inv, err := rt.InvokeEndpoint(ctx, &smrt.InvokeEndpointInput{
		EndpointName: aws.String("ep-a"),
		ContentType:  aws.String("application/json"),
		Accept:       aws.String("application/json"),
		Body:         []byte(`{"instances":[[1,2,3]]}`),
	})
	require.NoError(t, err)
	assert.True(t, bytes.Equal([]byte(`{"instances":[[1,2,3]]}`), inv.Body))
	assert.Equal(t, "v1", aws.ToString(inv.InvokedProductionVariant))
}

func TestSDKTagsRoundTrip(t *testing.T) {
	url := newServer(t)
	client := awssm.NewFromConfig(newCfg(t), func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })
	ctx := context.Background()

	out, err := client.CreateModel(ctx, &awssm.CreateModelInput{
		ModelName:        aws.String("tagged-model"),
		ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
		PrimaryContainer: &smtypes.ContainerDefinition{Image: aws.String("img:latest")},
	})
	require.NoError(t, err)

	_, err = client.AddTags(ctx, &awssm.AddTagsInput{
		ResourceArn: out.ModelArn,
		Tags:        []smtypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	require.NoError(t, err)

	tags, err := client.ListTags(ctx, &awssm.ListTagsInput{ResourceArn: out.ModelArn})
	require.NoError(t, err)
	require.Len(t, tags.Tags, 1)
	assert.Equal(t, "env", aws.ToString(tags.Tags[0].Key))
}

func TestSDKDescribeMissingTrainingJob(t *testing.T) {
	url := newServer(t)
	client := awssm.NewFromConfig(newCfg(t), func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })

	_, err := client.DescribeTrainingJob(context.Background(), &awssm.DescribeTrainingJobInput{
		TrainingJobName: aws.String("ghost"),
	})
	require.Error(t, err)

	// DescribeTrainingJob models ResourceNotFound, so the NotFound→ResourceNotFound
	// mapping deserializes as a typed SDK error. (Typed-error matching is
	// per-operation: the SDK only surfaces an exception an operation models;
	// everything else — e.g. a generic ValidationException — arrives as
	// *smithy.GenericAPIError, exactly as it does against real SageMaker.)
	var rnf *smtypes.ResourceNotFound
	assert.True(t, errors.As(err, &rnf), "expected ResourceNotFound, got %T", err)
}

// TestSDKValidationErrorIsGeneric locks in that an InvalidArgument →
// ValidationException surfaces as a (non-modeled) error: CreateEndpoint against
// a missing config returns an error that is NOT one of SageMaker's modeled
// typed exceptions, matching real SageMaker where ValidationException is a
// cross-service generic error.
func TestSDKValidationErrorIsGeneric(t *testing.T) {
	url := newServer(t)
	client := awssm.NewFromConfig(newCfg(t), func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })

	_, err := client.CreateEndpoint(context.Background(), &awssm.CreateEndpointInput{
		EndpointName:       aws.String("ep"),
		EndpointConfigName: aws.String("does-not-exist"),
	})
	require.Error(t, err)

	var (
		rnf   *smtypes.ResourceNotFound
		inUse *smtypes.ResourceInUse
	)
	assert.False(t, errors.As(err, &rnf), "ValidationException must not match a modeled typed error")
	assert.False(t, errors.As(err, &inUse), "ValidationException must not match a modeled typed error")
}
