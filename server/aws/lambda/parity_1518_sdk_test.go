package lambda_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newParityLambdaClient(t *testing.T, opts ...config.Option) *awslambda.Client {
	t.Helper()

	cloud := cloudemu.NewAWS(opts...)
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{Lambda: cloud.Lambda}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func createParityFunction(t *testing.T, c *awslambda.Client, name string, code []byte) *awslambda.CreateFunctionOutput {
	t.Helper()

	out, err := c.CreateFunction(context.Background(), &awslambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lambdatypes.RuntimePython312,
		Role:         aws.String("arn:aws:iam::123456789012:role/test"),
		Handler:      aws.String("main.handler"),
		Code:         &lambdatypes.FunctionCode{ZipFile: code},
		Environment:  &lambdatypes.Environment{Variables: map[string]string{"STAGE": "one"}},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	return out
}

// TestSDKPublishedVersionIsImmutable: after UpdateFunctionCode /
// UpdateFunctionConfiguration, GetFunction --qualifier 1 still reports the
// CodeSize, Environment and Architectures version 1 was cut with.
func TestSDKPublishedVersionIsImmutable(t *testing.T) {
	c := newParityLambdaClient(t)
	ctx := context.Background()

	createParityFunction(t, c, "imm", []byte("v1-code"))

	pub, err := c.PublishVersion(ctx, &awslambda.PublishVersionInput{FunctionName: aws.String("imm")})
	if err != nil {
		t.Fatal(err)
	}

	if pub.CodeSize != int64(len("v1-code")) {
		t.Fatalf("PublishVersion CodeSize = %d", pub.CodeSize)
	}

	if _, err := c.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
		FunctionName: aws.String("imm"), ZipFile: []byte("version-two-code-is-longer"),
		Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureArm64},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("imm"),
		Environment:  &lambdatypes.Environment{Variables: map[string]string{"STAGE": "two"}},
	}); err != nil {
		t.Fatal(err)
	}

	v1, err := c.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("imm"), Qualifier: aws.String("1")})
	if err != nil {
		t.Fatal(err)
	}

	cfg := v1.Configuration
	if cfg.CodeSize != int64(len("v1-code")) {
		t.Errorf("version 1 CodeSize = %d, want %d", cfg.CodeSize, len("v1-code"))
	}

	if cfg.Environment == nil || cfg.Environment.Variables["STAGE"] != "one" {
		t.Errorf("version 1 Environment = %+v, want STAGE=one", cfg.Environment)
	}

	if len(cfg.Architectures) != 1 || cfg.Architectures[0] != lambdatypes.ArchitectureX8664 {
		t.Errorf("version 1 Architectures = %v, want [x86_64]", cfg.Architectures)
	}

	latest, err := c.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("imm")})
	if err != nil {
		t.Fatal(err)
	}

	if latest.Configuration.CodeSize != int64(len("version-two-code-is-longer")) {
		t.Errorf("$LATEST CodeSize = %d", latest.Configuration.CodeSize)
	}

	vers, err := c.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{FunctionName: aws.String("imm")})
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range vers.Versions {
		if aws.ToString(v.Version) == "1" && v.CodeSize != int64(len("v1-code")) {
			t.Errorf("ListVersions version 1 CodeSize = %d", v.CodeSize)
		}
	}
}

// TestSDKInvokeLogTypeTail: LogType=Tail returns the base64 execution-log tail.
func TestSDKInvokeLogTypeTail(t *testing.T) {
	c := newParityLambdaClient(t)
	ctx := context.Background()

	createParityFunction(t, c, "logs", []byte("zip"))

	out, err := c.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String("logs"), Payload: []byte(`{"a":1}`), LogType: lambdatypes.LogTypeTail,
	})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := base64.StdEncoding.DecodeString(aws.ToString(out.LogResult))
	if err != nil {
		t.Fatalf("LogResult not base64: %v", err)
	}

	for _, want := range []string{"START RequestId:", "END RequestId:", "REPORT RequestId:"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("log tail missing %q: %q", want, raw)
		}
	}

	plain, err := c.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String("logs")})
	if err != nil {
		t.Fatal(err)
	}

	if plain.LogResult != nil {
		t.Errorf("LogType=None LogResult = %q, want nil", aws.ToString(plain.LogResult))
	}
}

// TestSDKGetAccountSettings drives GET /2016-08-19/account-settings.
func TestSDKGetAccountSettings(t *testing.T) {
	c := newParityLambdaClient(t)
	ctx := context.Background()

	createParityFunction(t, c, "acct", []byte("1234567"))

	out, err := c.GetAccountSettings(ctx, &awslambda.GetAccountSettingsInput{})
	if err != nil {
		t.Fatal(err)
	}

	if out.AccountLimit.ConcurrentExecutions != 1000 ||
		aws.ToInt32(out.AccountLimit.UnreservedConcurrentExecutions) != 1000 {
		t.Errorf("limits = %+v", out.AccountLimit)
	}

	if out.AccountUsage.FunctionCount != 1 || out.AccountUsage.TotalCodeSize != 7 {
		t.Errorf("usage = %+v", out.AccountUsage)
	}
}

// TestSDKCodeSigningConfigs drives the code signing config API end to end.
func TestSDKCodeSigningConfigs(t *testing.T) {
	c := newParityLambdaClient(t)
	ctx := context.Background()

	created, err := c.CreateCodeSigningConfig(ctx, &awslambda.CreateCodeSigningConfigInput{
		Description: aws.String("demo"),
		Tags:        map[string]string{"team": "a"},
		AllowedPublishers: &lambdatypes.AllowedPublishers{
			SigningProfileVersionArns: []string{"arn:aws:signer:us-east-1:123456789012:/signing-profiles/p/abc"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	csc := created.CodeSigningConfig
	arn := aws.ToString(csc.CodeSigningConfigArn)

	if csc.CodeSigningPolicies.UntrustedArtifactOnDeployment != lambdatypes.CodeSigningPolicyWarn {
		t.Errorf("default policy = %q", csc.CodeSigningPolicies.UntrustedArtifactOnDeployment)
	}

	got, err := c.GetCodeSigningConfig(ctx, &awslambda.GetCodeSigningConfigInput{CodeSigningConfigArn: aws.String(arn)})
	if err != nil || aws.ToString(got.CodeSigningConfig.Description) != "demo" {
		t.Fatalf("get = %+v, %v", got, err)
	}

	if _, err := c.UpdateCodeSigningConfig(ctx, &awslambda.UpdateCodeSigningConfigInput{
		CodeSigningConfigArn: aws.String(arn),
		CodeSigningPolicies: &lambdatypes.CodeSigningPolicies{
			UntrustedArtifactOnDeployment: lambdatypes.CodeSigningPolicyEnforce,
		},
	}); err != nil {
		t.Fatal(err)
	}

	list, err := c.ListCodeSigningConfigs(ctx, &awslambda.ListCodeSigningConfigsInput{})
	if err != nil || len(list.CodeSigningConfigs) != 1 ||
		list.CodeSigningConfigs[0].CodeSigningPolicies.UntrustedArtifactOnDeployment != lambdatypes.CodeSigningPolicyEnforce {
		t.Fatalf("list = %+v, %v", list, err)
	}

	// Tags given on create are visible through the shared tagging API.
	if _, err := c.TagResource(ctx, &awslambda.TagResourceInput{
		Resource: aws.String(arn), Tags: map[string]string{"env": "dev"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.UntagResource(ctx, &awslambda.UntagResourceInput{
		Resource: aws.String(arn), TagKeys: []string{"team"},
	}); err != nil {
		t.Fatal(err)
	}

	tags, err := c.ListTags(ctx, &awslambda.ListTagsInput{Resource: aws.String(arn)})
	if err != nil || len(tags.Tags) != 1 || tags.Tags["env"] != "dev" {
		t.Fatalf("csc tags = %v, %v; want {env:dev}", tags, err)
	}

	createParityFunction(t, c, "signed", []byte("zip"))

	missing := "arn:aws:lambda:us-east-1:123456789012:code-signing-config:csc-00000000000000000"

	_, err = c.PutFunctionCodeSigningConfig(ctx, &awslambda.PutFunctionCodeSigningConfigInput{
		FunctionName: aws.String("signed"), CodeSigningConfigArn: aws.String(missing),
	})

	var notFound *lambdatypes.CodeSigningConfigNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("put unknown csc err = %v, want CodeSigningConfigNotFoundException", err)
	}

	if _, err := c.PutFunctionCodeSigningConfig(ctx, &awslambda.PutFunctionCodeSigningConfigInput{
		FunctionName: aws.String("signed"), CodeSigningConfigArn: aws.String(arn),
	}); err != nil {
		t.Fatal(err)
	}

	fcsc, err := c.GetFunctionCodeSigningConfig(ctx, &awslambda.GetFunctionCodeSigningConfigInput{FunctionName: aws.String("signed")})
	if err != nil || aws.ToString(fcsc.CodeSigningConfigArn) != arn {
		t.Fatalf("get function csc = %+v, %v", fcsc, err)
	}

	fns, err := c.ListFunctionsByCodeSigningConfig(ctx, &awslambda.ListFunctionsByCodeSigningConfigInput{
		CodeSigningConfigArn: aws.String(arn),
	})
	if err != nil || len(fns.FunctionArns) != 1 {
		t.Fatalf("list functions = %+v, %v", fns, err)
	}

	_, err = c.DeleteCodeSigningConfig(ctx, &awslambda.DeleteCodeSigningConfigInput{CodeSigningConfigArn: aws.String(arn)})

	var conflict *lambdatypes.ResourceConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("delete in-use err = %v, want ResourceConflictException", err)
	}

	if _, err := c.DeleteFunctionCodeSigningConfig(ctx, &awslambda.DeleteFunctionCodeSigningConfigInput{
		FunctionName: aws.String("signed"),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.DeleteCodeSigningConfig(ctx, &awslambda.DeleteCodeSigningConfigInput{
		CodeSigningConfigArn: aws.String(arn),
	}); err != nil {
		t.Fatal(err)
	}

	_, err = c.GetCodeSigningConfig(ctx, &awslambda.GetCodeSigningConfigInput{CodeSigningConfigArn: aws.String(arn)})

	var rnf *lambdatypes.ResourceNotFoundException
	if !errors.As(err, &rnf) {
		t.Fatalf("get deleted err = %v, want ResourceNotFoundException", err)
	}
}

// TestSDKFunctionNotFoundMessage: the ResourceNotFoundException names the ARN.
func TestSDKFunctionNotFoundMessage(t *testing.T) {
	c := newParityLambdaClient(t)

	_, err := c.GetFunction(context.Background(), &awslambda.GetFunctionInput{FunctionName: aws.String("ghost")})

	var rnf *lambdatypes.ResourceNotFoundException
	if !errors.As(err, &rnf) {
		t.Fatalf("err = %v", err)
	}

	if want := "Function not found: arn:aws:lambda:us-east-1:123456789012:function:ghost"; aws.ToString(rnf.Message) != want {
		t.Errorf("message = %q, want %q", aws.ToString(rnf.Message), want)
	}
}

// TestSDKAsyncSettleWaiters: under AsyncSettle the SDK FunctionActiveV2 and
// FunctionUpdatedV2 waiters observe Pending -> Active and InProgress ->
// Successful, and an Invoke while Pending is a ResourceConflictException.
func TestSDKAsyncSettleWaiters(t *testing.T) {
	c := newParityLambdaClient(t, config.WithAsyncSettle())
	ctx := context.Background()

	created := createParityFunction(t, c, "settle", []byte("zip"))
	if created.State != lambdatypes.StatePending || created.StateReasonCode != lambdatypes.StateReasonCodeCreating ||
		created.LastUpdateStatus != "" {
		t.Fatalf("create = %q/%q/%q", created.State, created.StateReasonCode, created.LastUpdateStatus)
	}

	_, err := c.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String("settle")})

	var conflict *lambdatypes.ResourceConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("invoke while Pending err = %v, want ResourceConflictException", err)
	}

	waitActive := awslambda.NewFunctionActiveV2Waiter(c, func(o *awslambda.FunctionActiveV2WaiterOptions) {
		o.MinDelay, o.MaxDelay = 200*time.Millisecond, 300*time.Millisecond
	})
	if err := waitActive.Wait(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("settle")}, 10*time.Second); err != nil {
		t.Fatalf("FunctionActiveV2: %v", err)
	}

	upd, err := c.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("settle"), Timeout: aws.Int32(9),
	})
	if err != nil {
		t.Fatal(err)
	}

	if upd.LastUpdateStatus != lambdatypes.LastUpdateStatusInProgress || upd.State != lambdatypes.StateActive {
		t.Fatalf("update = %q/%q", upd.State, upd.LastUpdateStatus)
	}

	waitUpdated := awslambda.NewFunctionUpdatedV2Waiter(c, func(o *awslambda.FunctionUpdatedV2WaiterOptions) {
		o.MinDelay, o.MaxDelay = 200*time.Millisecond, 300*time.Millisecond
	})
	if err := waitUpdated.Wait(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("settle")}, 10*time.Second); err != nil {
		t.Fatalf("FunctionUpdatedV2: %v", err)
	}
}
