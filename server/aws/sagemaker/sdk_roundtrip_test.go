package sagemaker_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssm "github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	smrt "github.com/aws/aws-sdk-go-v2/service/sagemakerruntime"
	smrttypes "github.com/aws/aws-sdk-go-v2/service/sagemakerruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	smdriver "github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// modelFailingDriver wraps the real driver and fails every invocation as a
// model-container error, which is what a failing user container produces. The
// emulator has no production way to make a container fail, so the wire shape is
// pinned through the driver seam instead.
type modelFailingDriver struct{ smdriver.Service }

func (modelFailingDriver) InvokeEndpoint(context.Context, smdriver.InvokeEndpointInput) (*smdriver.InvokeEndpointOutput, error) {
	return nil, &smdriver.ModelError{OriginalStatusCode: 500, Message: "model container failure"}
}

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
	// everything else, e.g. a generic ValidationException, arrives as
	// *smithy.GenericAPIError, as it does against real SageMaker.)
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

func newRuntimeClients(t *testing.T) (*awssm.Client, *smrt.Client) {
	t.Helper()

	url := newServer(t)
	cfg := newCfg(t)

	return awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) }),
		smrt.NewFromConfig(cfg, func(o *smrt.Options) { o.BaseEndpoint = aws.String(url) })
}

func TestSDKInvoke_ContentTypeAndVariant(t *testing.T) { // T44, T46
	c, rt := newRuntimeClients(t)
	ctx := context.Background()
	seedSDKEndpoint(t, c, "ep-1", "cfg-1", 1, 1)

	out, err := rt.InvokeEndpoint(ctx, &smrt.InvokeEndpointInput{
		EndpointName: aws.String("ep-1"), ContentType: aws.String("text/csv"), Accept: aws.String("*/*"), Body: []byte("1,2"),
	})
	require.NoError(t, err)
	assert.Equal(t, "text/csv", aws.ToString(out.ContentType), "a wildcard Accept is not echoed back")

	out, err = rt.InvokeEndpoint(ctx, &smrt.InvokeEndpointInput{
		EndpointName: aws.String("ep-1"), ContentType: aws.String("text/csv"), Accept: aws.String("application/json"), Body: []byte("1,2"),
	})
	require.NoError(t, err)
	assert.Equal(t, "application/json", aws.ToString(out.ContentType))

	out, err = rt.InvokeEndpoint(ctx, &smrt.InvokeEndpointInput{
		EndpointName: aws.String("ep-1"), Body: []byte("x"), TargetVariant: aws.String("v1"),
	})
	require.NoError(t, err)
	assert.Equal(t, "v1", aws.ToString(out.InvokedProductionVariant))

	_, err = rt.InvokeEndpoint(ctx, &smrt.InvokeEndpointInput{
		EndpointName: aws.String("ep-1"), Body: []byte("x"), TargetVariant: aws.String("nope"),
	})

	var ve *smrttypes.ValidationError

	require.ErrorAs(t, err, &ve)
}

func TestSDKInvoke_Errors(t *testing.T) { // T45
	c, rt := newRuntimeClients(t)
	ctx := context.Background()

	_, err := rt.InvokeEndpoint(ctx, &smrt.InvokeEndpointInput{EndpointName: aws.String("missing"), Body: []byte("x")})

	var ve *smrttypes.ValidationError

	require.ErrorAs(t, err, &ve)
	assert.Contains(t, aws.ToString(ve.Message), "not found")

	var re interface{ HTTPStatusCode() int }
	if errors.As(err, &re) {
		assert.Equal(t, 400, re.HTTPStatusCode(), "a missing endpoint is a 400 ValidationError")
	}

	seedSDKEndpoint(t, c, "ep-1", "cfg-1", 1, 1)

	big := make([]byte, (6<<20)+1)
	_, err = rt.InvokeEndpoint(ctx, &smrt.InvokeEndpointInput{EndpointName: aws.String("ep-1"), Body: big})
	require.ErrorAs(t, err, &ve, "an oversized payload is rejected, never silently truncated")

	exact := make([]byte, 6<<20)
	out, err := rt.InvokeEndpoint(ctx, &smrt.InvokeEndpointInput{EndpointName: aws.String("ep-1"), Body: exact})
	require.NoError(t, err)
	assert.Len(t, out.Body, 6<<20, "a payload at the limit is echoed whole")
}

// TestSDKInvoke_ModelError pins the wire shape of a model-container failure:
// HTTP 424 with the modeled ModelError exception.
func TestSDKInvoke_ModelError(t *testing.T) { // T132, B6
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{SageMaker: modelFailingDriver{cloud.SageMaker}}))
	t.Cleanup(ts.Close)

	cfg := newCfg(t)
	c := awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	rt := smrt.NewFromConfig(cfg, func(o *smrt.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	_, err := c.CreateModel(ctx, &awssm.CreateModelInput{ModelName: aws.String("m"), ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/r")})
	require.NoError(t, err)
	_, err = c.CreateEndpointConfig(ctx, &awssm.CreateEndpointConfigInput{
		EndpointConfigName: aws.String("c"),
		ProductionVariants: []smtypes.ProductionVariant{{VariantName: aws.String("v"), ModelName: aws.String("m"), InstanceType: smtypes.ProductionVariantInstanceTypeMlM5Large, InitialInstanceCount: aws.Int32(1)}},
	})
	require.NoError(t, err)
	_, err = c.CreateEndpoint(ctx, &awssm.CreateEndpointInput{EndpointName: aws.String("e"), EndpointConfigName: aws.String("c")})
	require.NoError(t, err)

	_, err = rt.InvokeEndpoint(ctx, &smrt.InvokeEndpointInput{EndpointName: aws.String("e"), Body: []byte("x")})

	var me *smrttypes.ModelError
	require.ErrorAs(t, err, &me)
}

func rawPost(t *testing.T, url, target, contentType string, body []byte, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)

	if target != "" {
		req.Header.Set("X-Amz-Target", "SageMaker."+target)
	}

	req.Header.Set("Content-Type", contentType)

	for k, v := range hdr {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp, out
}

// TestInvoke_MarkupResponseIsNotServedAsMarkup pins that an echoed body is never
// rendered as active content: markup types are downgraded and sniffing is off.
func TestInvoke_MarkupResponseIsNotServedAsMarkup(t *testing.T) {
	url := newServer(t)
	c := awssm.NewFromConfig(newCfg(t), func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })
	ctx := context.Background()

	_, err := c.CreateModel(ctx, &awssm.CreateModelInput{ModelName: aws.String("m"), ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/r")})
	require.NoError(t, err)
	_, err = c.CreateEndpointConfig(ctx, &awssm.CreateEndpointConfigInput{
		EndpointConfigName: aws.String("c"),
		ProductionVariants: []smtypes.ProductionVariant{{VariantName: aws.String("v"), ModelName: aws.String("m"), InstanceType: smtypes.ProductionVariantInstanceTypeMlM5Large, InitialInstanceCount: aws.Int32(1)}},
	})
	require.NoError(t, err)
	_, err = c.CreateEndpoint(ctx, &awssm.CreateEndpointInput{EndpointName: aws.String("e"), EndpointConfigName: aws.String("c")})
	require.NoError(t, err)

	resp, body := rawPost(t, url+"/endpoints/e/invocations", "", "text/plain", []byte("<script>alert(1)</script>"), map[string]string{"Accept": "text/html"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "sandbox", resp.Header.Get("Content-Security-Policy"), "a response that slips past the type check still cannot run script")
	assert.Equal(t, "<script>alert(1)</script>", string(body))

	// Any +xml suffix and the XSL types are active content too, not only the common few.
	for _, ct := range []string{"application/rss+xml", "application/atom+xml", "application/mathml+xml", "text/xsl", "application/xslt+xml", "APPLICATION/RSS+XML; charset=utf-8"} {
		resp, _ = rawPost(t, url+"/endpoints/e/invocations", "", ct, []byte("<a/>"), map[string]string{"Accept": ct})
		assert.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"), ct)
	}

	resp, _ = rawPost(t, url+"/endpoints/e/invocations", "", "application/json", []byte("{}"), map[string]string{"Accept": "application/json"})
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"), "a data type is echoed unchanged")
}

func TestControlPlane_BodyIsBounded(t *testing.T) {
	url := newServer(t)

	resp, _ := rawPost(t, url, "ListModels", "application/x-amz-json-1.1", bytes.Repeat([]byte(" "), 2<<20), nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "a body over the limit is rejected")
}

// TestWorkforce_ClientSecretNeverReturned pins that an OIDC client secret is
// accepted on create but absent from every response.
func TestWorkforce_ClientSecretNeverReturned(t *testing.T) {
	url := newServer(t)
	body := `{"WorkforceName":"wf","OidcConfig":{"ClientId":"c","ClientSecret":"s3cret-value","Issuer":"https://i","AuthorizationEndpoint":"https://i/a",` +
		`"TokenEndpoint":"https://i/t","UserInfoEndpoint":"https://i/u","LogoutEndpoint":"https://i/l","JwksUri":"https://i/j"}}`

	resp, out := rawPost(t, url, "CreateWorkforce", "application/x-amz-json-1.1", []byte(body), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(out))

	for _, op := range []string{"DescribeWorkforce", "ListWorkforces"} {
		payload := `{"WorkforceName":"wf"}`
		if op == "ListWorkforces" {
			payload = `{}`
		}

		resp, out = rawPost(t, url, op, "application/x-amz-json-1.1", []byte(payload), nil)
		require.Equal(t, http.StatusOK, resp.StatusCode, op+": "+string(out))
		assert.NotContains(t, string(out), "ClientSecret", op)
		assert.NotContains(t, string(out), "s3cret-value", op)
	}
}

// TestSDKLimits_ResourceLimitExceededIsTyped pins the wire name of an account quota.
func TestSDKLimits_ResourceLimitExceededIsTyped(t *testing.T) {
	c, _ := newRuntimeClients(t)
	ctx := context.Background()

	for i := 0; i < 500; i++ {
		_, err := c.CreateProject(ctx, &awssm.CreateProjectInput{
			ProjectName:                       aws.String("p" + strconv.Itoa(i)),
			ServiceCatalogProvisioningDetails: &smtypes.ServiceCatalogProvisioningDetails{ProductId: aws.String("prod"), ProvisioningArtifactId: aws.String("pa")},
		})
		require.NoError(t, err)
	}

	_, err := c.CreateProject(ctx, &awssm.CreateProjectInput{
		ProjectName:                       aws.String("one-too-many"),
		ServiceCatalogProvisioningDetails: &smtypes.ServiceCatalogProvisioningDetails{ProductId: aws.String("prod"), ProvisioningArtifactId: aws.String("pa")},
	})

	var limit *smtypes.ResourceLimitExceeded

	require.ErrorAs(t, err, &limit)
}

func TestSDKInvokeAsync_UsesConfiguredOutput(t *testing.T) {
	c, rt := newRuntimeClients(t)
	ctx := context.Background()

	_, err := c.CreateModel(ctx, &awssm.CreateModelInput{ModelName: aws.String("m"), ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/r")})
	require.NoError(t, err)

	variants := []smtypes.ProductionVariant{{VariantName: aws.String("v"), ModelName: aws.String("m"), InstanceType: smtypes.ProductionVariantInstanceTypeMlM5Large, InitialInstanceCount: aws.Int32(1)}}
	_, err = c.CreateEndpointConfig(ctx, &awssm.CreateEndpointConfigInput{
		EndpointConfigName: aws.String("c"), ProductionVariants: variants,
		AsyncInferenceConfig: &smtypes.AsyncInferenceConfig{OutputConfig: &smtypes.AsyncInferenceOutputConfig{S3OutputPath: aws.String("s3://results/out")}},
	})
	require.NoError(t, err)

	d, err := c.DescribeEndpointConfig(ctx, &awssm.DescribeEndpointConfigInput{EndpointConfigName: aws.String("c")})
	require.NoError(t, err)
	assert.Equal(t, "s3://results/out", aws.ToString(d.AsyncInferenceConfig.OutputConfig.S3OutputPath))

	_, err = c.CreateEndpoint(ctx, &awssm.CreateEndpointInput{EndpointName: aws.String("e"), EndpointConfigName: aws.String("c")})
	require.NoError(t, err)

	out, err := rt.InvokeEndpointAsync(ctx, &smrt.InvokeEndpointAsyncInput{EndpointName: aws.String("e"), InputLocation: aws.String("s3://in/x")})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(out.OutputLocation), "s3://results/out/")
	assert.NotEmpty(t, aws.ToString(out.InferenceId))

	// The caller's InferenceId (the X-Amzn-SageMaker-Inference-Id header) and
	// content type (X-Amzn-SageMaker-Content-Type) are honored.
	out, err = rt.InvokeEndpointAsync(ctx, &smrt.InvokeEndpointAsyncInput{
		EndpointName: aws.String("e"), InputLocation: aws.String("s3://in/x"),
		InferenceId: aws.String("myid-1"), ContentType: aws.String("text/csv"),
	})
	require.NoError(t, err)
	assert.Equal(t, "myid-1", aws.ToString(out.InferenceId))
	assert.Equal(t, "s3://results/out/myid-1.out", aws.ToString(out.OutputLocation))
}
