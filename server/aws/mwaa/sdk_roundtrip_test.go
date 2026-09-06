package mwaa_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsmwaa "github.com/aws/aws-sdk-go-v2/service/mwaa"
	mwtypes "github.com/aws/aws-sdk-go-v2/service/mwaa/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awsmwaa.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{MWAA: cloud.MWAA})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsmwaa.NewFromConfig(cfg, func(o *awsmwaa.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.APIOptions = append(o.APIOptions, disableHostPrefix)
	})
}

// disableHostPrefix disables the "api." endpoint host prefix MWAA applies to
// its control-plane operations, so the SDK talks to the local httptest server
// instead of a rewritten, unresolvable host.
func disableHostPrefix(stack *middleware.Stack) error {
	return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("disableHostPrefix",
		func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler,
		) (middleware.InitializeOutput, middleware.Metadata, error) {
			return next.HandleInitialize(smithyhttp.DisableEndpointHostPrefix(ctx, true), in)
		}), middleware.Before)
}

// createInput builds a full aws_mwaa_environment-shaped CreateEnvironment input.
func createInput(name string) *awsmwaa.CreateEnvironmentInput {
	module := func(level mwtypes.LoggingLevel, enabled bool) *mwtypes.ModuleLoggingConfigurationInput {
		return &mwtypes.ModuleLoggingConfigurationInput{Enabled: aws.Bool(enabled), LogLevel: level}
	}

	return &awsmwaa.CreateEnvironmentInput{
		Name:                aws.String(name),
		AirflowVersion:      aws.String("2.10.1"),
		DagS3Path:           aws.String("dags"),
		ExecutionRoleArn:    aws.String("arn:aws:iam::123456789012:role/my-execution-role"),
		SourceBucketArn:     aws.String("arn:aws:s3:::my-airflow-bucket-unique-name"),
		EnvironmentClass:    aws.String("mw1.small"),
		MaxWorkers:          aws.Int32(10),
		MinWorkers:          aws.Int32(1),
		Schedulers:          aws.Int32(2),
		WebserverAccessMode: mwtypes.WebserverAccessModePublicOnly,
		NetworkConfiguration: &mwtypes.NetworkConfiguration{
			SecurityGroupIds: []string{"sg-0123456789abcdef0"},
			SubnetIds:        []string{"subnet-0123456789abcdef0", "subnet-0123456789abcdef1"},
		},
		LoggingConfiguration: &mwtypes.LoggingConfigurationInput{
			DagProcessingLogs: module(mwtypes.LoggingLevelInfo, true),
			SchedulerLogs:     module(mwtypes.LoggingLevelWarning, true),
			TaskLogs:          module(mwtypes.LoggingLevelInfo, true),
			WebserverLogs:     module(mwtypes.LoggingLevelError, false),
			WorkerLogs:        module(mwtypes.LoggingLevelInfo, false),
		},
		AirflowConfigurationOptions:  map[string]string{"core.default_task_retries": "3"},
		WeeklyMaintenanceWindowStart: aws.String("TUE:03:30"),
		Tags:                         map[string]string{"env": "test"},
	}
}

func get(t *testing.T, c *awsmwaa.Client, name string) *mwtypes.Environment {
	t.Helper()

	out, err := c.GetEnvironment(context.Background(), &awsmwaa.GetEnvironmentInput{Name: aws.String(name)})
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}

	return out.Environment
}

func TestSDKEnvironmentLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateEnvironment(ctx, createInput("sdk-env"))
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	arn := aws.ToString(create.Arn)
	if arn == "" {
		t.Fatal("create Arn empty")
	}

	e1 := get(t, c, "sdk-env")

	if string(e1.Status) != "AVAILABLE" {
		t.Fatalf("status = %q, want AVAILABLE", e1.Status)
	}

	if aws.ToString(e1.Arn) != arn {
		t.Fatalf("get Arn = %q, want %q", aws.ToString(e1.Arn), arn)
	}

	assertComputedPresent(t, e1)
	assertConfigRoundTrip(t, e1)

	// Second read: every computed and config field must be byte-identical.
	e2 := get(t, c, "sdk-env")
	assertStable(t, e1, e2)
}

func assertComputedPresent(t *testing.T, e *mwtypes.Environment) {
	t.Helper()

	if aws.ToString(e.WebserverUrl) == "" {
		t.Fatal("WebserverUrl empty")
	}

	if aws.ToString(e.ServiceRoleArn) == "" {
		t.Fatal("ServiceRoleArn empty")
	}

	if e.CreatedAt == nil {
		t.Fatal("CreatedAt nil")
	}

	// An enabled log module reports a stable CloudWatchLogGroupArn; a disabled
	// one reports none.
	if e.LoggingConfiguration == nil || e.LoggingConfiguration.DagProcessingLogs == nil {
		t.Fatal("LoggingConfiguration.DagProcessingLogs missing")
	}

	if aws.ToString(e.LoggingConfiguration.DagProcessingLogs.CloudWatchLogGroupArn) == "" {
		t.Fatal("enabled DagProcessingLogs missing CloudWatchLogGroupArn")
	}

	if e.LoggingConfiguration.WebserverLogs.CloudWatchLogGroupArn != nil {
		t.Fatal("disabled WebserverLogs should have no CloudWatchLogGroupArn")
	}
}

func assertConfigRoundTrip(t *testing.T, e *mwtypes.Environment) {
	t.Helper()

	if aws.ToString(e.AirflowVersion) != "2.10.1" {
		t.Fatalf("AirflowVersion = %q", aws.ToString(e.AirflowVersion))
	}

	if aws.ToInt32(e.MaxWorkers) != 10 || aws.ToInt32(e.MinWorkers) != 1 {
		t.Fatalf("workers = %d/%d, want 10/1", aws.ToInt32(e.MaxWorkers), aws.ToInt32(e.MinWorkers))
	}

	if e.NetworkConfiguration == nil || len(e.NetworkConfiguration.SubnetIds) != 2 {
		t.Fatalf("NetworkConfiguration subnets not round-tripped: %+v", e.NetworkConfiguration)
	}

	if e.LoggingConfiguration.SchedulerLogs.LogLevel != mwtypes.LoggingLevelWarning {
		t.Fatalf("SchedulerLogs level = %q, want WARNING", e.LoggingConfiguration.SchedulerLogs.LogLevel)
	}

	if e.AirflowConfigurationOptions["core.default_task_retries"] != "3" {
		t.Fatal("AirflowConfigurationOptions not round-tripped")
	}
}

func assertStable(t *testing.T, a, b *mwtypes.Environment) {
	t.Helper()

	if aws.ToString(a.Arn) != aws.ToString(b.Arn) {
		t.Fatalf("Arn drifted: %q != %q", aws.ToString(a.Arn), aws.ToString(b.Arn))
	}

	if aws.ToString(a.WebserverUrl) != aws.ToString(b.WebserverUrl) {
		t.Fatal("WebserverUrl drifted")
	}

	if aws.ToString(a.ServiceRoleArn) != aws.ToString(b.ServiceRoleArn) {
		t.Fatal("ServiceRoleArn drifted")
	}

	if !a.CreatedAt.Equal(*b.CreatedAt) {
		t.Fatal("CreatedAt drifted")
	}

	if !reflect.DeepEqual(a.LoggingConfiguration, b.LoggingConfiguration) {
		t.Fatal("LoggingConfiguration drifted across reads")
	}

	if !reflect.DeepEqual(a.NetworkConfiguration, b.NetworkConfiguration) {
		t.Fatal("NetworkConfiguration drifted across reads")
	}
}

func TestSDKUpdateStability(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	if _, err := c.CreateEnvironment(ctx, createInput("upd-env")); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	e1 := get(t, c, "upd-env")

	if _, err := c.UpdateEnvironment(ctx, &awsmwaa.UpdateEnvironmentInput{
		Name:       aws.String("upd-env"),
		MaxWorkers: aws.Int32(20),
		LoggingConfiguration: &mwtypes.LoggingConfigurationInput{
			DagProcessingLogs: &mwtypes.ModuleLoggingConfigurationInput{
				Enabled: aws.Bool(true), LogLevel: mwtypes.LoggingLevelDebug,
			},
			SchedulerLogs: &mwtypes.ModuleLoggingConfigurationInput{
				Enabled: aws.Bool(true), LogLevel: mwtypes.LoggingLevelWarning,
			},
			TaskLogs: &mwtypes.ModuleLoggingConfigurationInput{
				Enabled: aws.Bool(true), LogLevel: mwtypes.LoggingLevelInfo,
			},
			WebserverLogs: &mwtypes.ModuleLoggingConfigurationInput{
				Enabled: aws.Bool(false), LogLevel: mwtypes.LoggingLevelError,
			},
			WorkerLogs: &mwtypes.ModuleLoggingConfigurationInput{
				Enabled: aws.Bool(false), LogLevel: mwtypes.LoggingLevelInfo,
			},
		},
	}); err != nil {
		t.Fatalf("UpdateEnvironment: %v", err)
	}

	e2 := get(t, c, "upd-env")

	if aws.ToInt32(e2.MaxWorkers) != 20 {
		t.Fatalf("MaxWorkers after update = %d, want 20", aws.ToInt32(e2.MaxWorkers))
	}

	if e2.LoggingConfiguration.DagProcessingLogs.LogLevel != mwtypes.LoggingLevelDebug {
		t.Fatalf("DagProcessingLogs level after update = %q, want DEBUG",
			e2.LoggingConfiguration.DagProcessingLogs.LogLevel)
	}

	// Computed fields and the immutable subnet ids survive the update.
	if aws.ToString(e2.Arn) != aws.ToString(e1.Arn) {
		t.Fatal("Arn drifted after update")
	}

	if !e2.CreatedAt.Equal(*e1.CreatedAt) {
		t.Fatal("CreatedAt drifted after update")
	}

	if !reflect.DeepEqual(e2.NetworkConfiguration.SubnetIds, e1.NetworkConfiguration.SubnetIds) {
		t.Fatal("SubnetIds not preserved across update")
	}

	// Unmentioned fields survive the PATCH.
	if aws.ToString(e2.AirflowVersion) != "2.10.1" {
		t.Fatal("AirflowVersion lost on update")
	}
}

func TestSDKListAndDelete(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	if _, err := c.CreateEnvironment(ctx, createInput("list-env")); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	list, err := c.ListEnvironments(ctx, &awsmwaa.ListEnvironmentsInput{})
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}

	if len(list.Environments) != 1 || list.Environments[0] != "list-env" {
		t.Fatalf("Environments = %v, want [list-env]", list.Environments)
	}

	if _, err := c.DeleteEnvironment(ctx, &awsmwaa.DeleteEnvironmentInput{Name: aws.String("list-env")}); err != nil {
		t.Fatalf("DeleteEnvironment: %v", err)
	}

	_, err = c.GetEnvironment(ctx, &awsmwaa.GetEnvironmentInput{Name: aws.String("list-env")})

	var nf *mwtypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("GetEnvironment after delete: got %v, want ResourceNotFoundException", err)
	}
}

func TestSDKDuplicateEnvironmentValidation(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	if _, err := c.CreateEnvironment(ctx, createInput("dup")); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	_, err := c.CreateEnvironment(ctx, createInput("dup"))

	var ve *mwtypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("duplicate CreateEnvironment: got %v, want ValidationException", err)
	}
}

func TestSDKEnvironmentTags(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateEnvironment(ctx, createInput("tagged"))
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	arn := aws.ToString(create.Arn)

	if _, err := c.TagResource(ctx, &awsmwaa.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        map[string]string{"team": "data"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	lt, err := c.ListTagsForResource(ctx, &awsmwaa.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if lt.Tags["team"] != "data" || lt.Tags["env"] != "test" {
		t.Fatalf("tags = %v, want team=data and env=test", lt.Tags)
	}

	if _, err := c.UntagResource(ctx, &awsmwaa.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	lt2, _ := c.ListTagsForResource(ctx, &awsmwaa.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if _, ok := lt2.Tags["team"]; ok {
		t.Fatal("team tag not removed")
	}
}

func TestSDKCliToken(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	if _, err := c.CreateEnvironment(ctx, createInput("tok-env")); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	tok, err := c.CreateCliToken(ctx, &awsmwaa.CreateCliTokenInput{Name: aws.String("tok-env")})
	if err != nil {
		t.Fatalf("CreateCliToken: %v", err)
	}

	if aws.ToString(tok.CliToken) == "" || aws.ToString(tok.WebServerHostname) == "" {
		t.Fatalf("CliToken/WebServerHostname empty: %q %q",
			aws.ToString(tok.CliToken), aws.ToString(tok.WebServerHostname))
	}

	_, err = c.CreateCliToken(ctx, &awsmwaa.CreateCliTokenInput{Name: aws.String("missing")})

	var nf *mwtypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("CreateCliToken on missing env: got %v, want ResourceNotFoundException", err)
	}
}
