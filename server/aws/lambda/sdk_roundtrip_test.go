package lambda_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/smithy-go"
	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// createBasicFunction creates a minimal function for fidelity tests.
func createBasicFunction(t *testing.T, client *awslambda.Client, name string) {
	t.Helper()

	if _, err := client.CreateFunction(context.Background(), &awslambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Description:  aws.String("desc-" + name),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("package-" + name)},
	}); err != nil {
		t.Fatalf("CreateFunction(%s): %v", name, err)
	}
}

func newSDKClient(t *testing.T) (*awslambda.Client, *awsprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Lambda: cloud.Lambda})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return client, cloud
}

func TestSDKLambdaCreateGetDelete(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("hello"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		MemorySize:   aws.Int32(128),
		Timeout:      aws.Int32(30),
		Code: &lambdatypes.FunctionCode{
			ZipFile: []byte("fake-zip"),
		},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	got, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{
		FunctionName: aws.String("hello"),
	})
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}

	if got.Configuration == nil || aws.ToString(got.Configuration.FunctionName) != "hello" {
		t.Fatalf("got %+v, want FunctionName=hello", got.Configuration)
	}

	if aws.ToString(got.Configuration.Handler) != "main" {
		t.Fatalf("Handler = %q, want main", aws.ToString(got.Configuration.Handler))
	}

	if got.Configuration.MemorySize == nil || *got.Configuration.MemorySize != 128 {
		t.Fatalf("MemorySize = %v, want 128", got.Configuration.MemorySize)
	}

	list, err := client.ListFunctions(ctx, &awslambda.ListFunctionsInput{})
	if err != nil {
		t.Fatalf("ListFunctions: %v", err)
	}

	if len(list.Functions) != 1 || aws.ToString(list.Functions[0].FunctionName) != "hello" {
		t.Fatalf("Functions = %+v, want one named hello", list.Functions)
	}

	if _, err := client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{
		FunctionName: aws.String("hello"),
	}); err != nil {
		t.Fatalf("DeleteFunction: %v", err)
	}

	if _, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{
		FunctionName: aws.String("hello"),
	}); err == nil {
		t.Fatal("GetFunction after delete returned nil error, want NotFound")
	}
}

func TestSDKLambdaInvoke(t *testing.T) {
	client, cloud := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("echo"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	cloud.Lambda.RegisterHandler("echo", func(_ context.Context, payload []byte) ([]byte, error) {
		out := append([]byte(`{"echoed":`), payload...)
		out = append(out, '}')

		return out, nil
	})

	resp, err := client.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String("echo"),
		Payload:      []byte(`"hi"`),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if got := string(resp.Payload); got != `{"echoed":"hi"}` {
		t.Fatalf("Payload = %q, want {\"echoed\":\"hi\"}", got)
	}

	if resp.FunctionError != nil && *resp.FunctionError != "" {
		t.Fatalf("FunctionError = %q, want empty", *resp.FunctionError)
	}

	// Read+exhaust to simulate a real client clean-up — guards against any
	// surprises if Invoke ever returns a streaming body.
	_, _ = io.ReadAll(bytes.NewReader(resp.Payload))
}

func TestSDKLambdaInvokeOnMissingFunction(t *testing.T) {
	client, _ := newSDKClient(t)

	_, err := client.Invoke(context.Background(), &awslambda.InvokeInput{
		FunctionName: aws.String("nope"),
		Payload:      []byte(`{}`),
	})
	if err == nil {
		t.Fatal("Invoke on missing function returned nil error, want NotFound")
	}
}

func TestSDKLambdaConcurrencyRoundtrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("busy"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	put, err := client.PutFunctionConcurrency(ctx, &awslambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String("busy"),
		ReservedConcurrentExecutions: aws.Int32(5),
	})
	if err != nil {
		t.Fatalf("PutFunctionConcurrency: %v", err)
	}

	if put.ReservedConcurrentExecutions == nil || *put.ReservedConcurrentExecutions != 5 {
		t.Fatalf("Put ReservedConcurrentExecutions = %v, want 5", put.ReservedConcurrentExecutions)
	}

	got, err := client.GetFunctionConcurrency(ctx, &awslambda.GetFunctionConcurrencyInput{
		FunctionName: aws.String("busy"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConcurrency: %v", err)
	}

	if got.ReservedConcurrentExecutions == nil || *got.ReservedConcurrentExecutions != 5 {
		t.Fatalf("Get ReservedConcurrentExecutions = %v, want 5", got.ReservedConcurrentExecutions)
	}

	if _, err := client.DeleteFunctionConcurrency(ctx, &awslambda.DeleteFunctionConcurrencyInput{
		FunctionName: aws.String("busy"),
	}); err != nil {
		t.Fatalf("DeleteFunctionConcurrency: %v", err)
	}

	// After delete the function still exists with no reserved concurrency, so
	// GetFunctionConcurrency is HTTP 200 with an empty body (nil executions), not
	// a 404 — only a missing function is NotFound.
	after, err := client.GetFunctionConcurrency(ctx, &awslambda.GetFunctionConcurrencyInput{
		FunctionName: aws.String("busy"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConcurrency after delete: %v, want 200 empty", err)
	}
	if after.ReservedConcurrentExecutions != nil {
		t.Fatalf("ReservedConcurrentExecutions = %v, want nil after delete", *after.ReservedConcurrentExecutions)
	}
}

func TestSDKLambdaLayersRoundtrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	pub, err := client.PublishLayerVersion(ctx, &awslambda.PublishLayerVersionInput{
		LayerName:          aws.String("deps"),
		Description:        aws.String("shared deps"),
		CompatibleRuntimes: []lambdatypes.Runtime{lambdatypes.RuntimePython39},
		Content: &lambdatypes.LayerVersionContentInput{
			ZipFile: []byte("layer-zip"),
		},
	})
	if err != nil {
		t.Fatalf("PublishLayerVersion: %v", err)
	}

	if pub.Version != 1 {
		t.Fatalf("Version = %d, want 1", pub.Version)
	}

	if aws.ToString(pub.LayerVersionArn) == "" {
		t.Fatal("LayerVersionArn empty")
	}

	if pub.Content == nil || pub.Content.CodeSize == 0 {
		t.Fatalf("Content = %+v, want non-zero CodeSize", pub.Content)
	}

	got, err := client.GetLayerVersion(ctx, &awslambda.GetLayerVersionInput{
		LayerName:     aws.String("deps"),
		VersionNumber: aws.Int64(1),
	})
	if err != nil {
		t.Fatalf("GetLayerVersion: %v", err)
	}

	if got.Version != 1 || aws.ToString(got.Description) != "shared deps" {
		t.Fatalf("GetLayerVersion = %+v, want version 1 / shared deps", got)
	}

	lv, err := client.ListLayerVersions(ctx, &awslambda.ListLayerVersionsInput{
		LayerName: aws.String("deps"),
	})
	if err != nil {
		t.Fatalf("ListLayerVersions: %v", err)
	}

	if len(lv.LayerVersions) != 1 || lv.LayerVersions[0].Version != 1 {
		t.Fatalf("LayerVersions = %+v, want one version 1", lv.LayerVersions)
	}

	ll, err := client.ListLayers(ctx, &awslambda.ListLayersInput{})
	if err != nil {
		t.Fatalf("ListLayers: %v", err)
	}

	if len(ll.Layers) != 1 || aws.ToString(ll.Layers[0].LayerName) != "deps" {
		t.Fatalf("Layers = %+v, want one named deps", ll.Layers)
	}

	if ll.Layers[0].LatestMatchingVersion == nil || ll.Layers[0].LatestMatchingVersion.Version != 1 {
		t.Fatalf("LatestMatchingVersion = %+v, want version 1", ll.Layers[0].LatestMatchingVersion)
	}

	if _, err := client.DeleteLayerVersion(ctx, &awslambda.DeleteLayerVersionInput{
		LayerName:     aws.String("deps"),
		VersionNumber: aws.Int64(1),
	}); err != nil {
		t.Fatalf("DeleteLayerVersion: %v", err)
	}

	if _, err := client.GetLayerVersion(ctx, &awslambda.GetLayerVersionInput{
		LayerName:     aws.String("deps"),
		VersionNumber: aws.Int64(1),
	}); err == nil {
		t.Fatal("GetLayerVersion after delete returned nil error, want NotFound")
	}
}

// TestSDKGetFunctionConfiguration covers the blocker: GET .../configuration
// (the op FunctionActive/FunctionUpdated waiters poll) returned 405 and hung
// every Terraform/SAM/CDK deploy.
func TestSDKGetFunctionConfiguration(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("cfg"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	out, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("cfg"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}
	if aws.ToString(out.FunctionName) != "cfg" || aws.ToString(out.Handler) != "main" {
		t.Fatalf("config = %+v, want FunctionName=cfg Handler=main", out)
	}
}

// TestSDKFunctionAttributes covers the fields Terraform reads every plan
// (Role, CodeSha256, CodeSize, Version, RevisionId, Description). Before the
// fix these came back empty, causing perpetual drift and broken code-change
// detection.
func TestSDKFunctionAttributes(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	code := []byte("package-attrs")
	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("attrs"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/exec"),
		Handler:      aws.String("main"),
		Description:  aws.String("my function"),
		Code:         &lambdatypes.FunctionCode{ZipFile: code},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	got, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("attrs")})
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}

	c := got.Configuration
	if aws.ToString(c.Role) != "arn:aws:iam::000000000000:role/exec" {
		t.Fatalf("Role = %q, want the exec role", aws.ToString(c.Role))
	}
	if aws.ToString(c.Description) != "my function" {
		t.Fatalf("Description = %q, want 'my function'", aws.ToString(c.Description))
	}
	if aws.ToString(c.CodeSha256) == "" {
		t.Fatal("CodeSha256 empty, want base64 sha of the package")
	}
	if c.CodeSize != int64(len(code)) {
		t.Fatalf("CodeSize = %d, want %d", c.CodeSize, len(code))
	}
	if aws.ToString(c.Version) != "$LATEST" {
		t.Fatalf("Version = %q, want $LATEST", aws.ToString(c.Version))
	}
	if aws.ToString(c.RevisionId) == "" {
		t.Fatal("RevisionId empty, want a revision id")
	}
}

// TestSDKUpdateFunctionConfigurationPersists covers UpdateFunctionConfiguration
// silently dropping Description and Role: the update "succeeded" but never took
// effect.
func TestSDKUpdateFunctionConfigurationPersists(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "upd")

	before, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("upd")})
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	origRevision := aws.ToString(before.Configuration.RevisionId)

	if _, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("upd"),
		Description:  aws.String("updated description"),
		Role:         aws.String("arn:aws:iam::000000000000:role/newrole"),
	}); err != nil {
		t.Fatalf("UpdateFunctionConfiguration: %v", err)
	}

	got, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("upd"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}
	if aws.ToString(got.Description) != "updated description" {
		t.Fatalf("Description = %q, want 'updated description'", aws.ToString(got.Description))
	}
	if aws.ToString(got.Role) != "arn:aws:iam::000000000000:role/newrole" {
		t.Fatalf("Role = %q, want the new role", aws.ToString(got.Role))
	}
	if aws.ToString(got.RevisionId) == origRevision {
		t.Fatal("RevisionId unchanged after update, want a new revision")
	}
}

// TestSDKListFunctionsPagination covers ListFunctions ignoring MaxItems/Marker
// and never returning a NextMarker.
func TestSDKListFunctionsPagination(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	for _, n := range []string{"fn-a", "fn-b", "fn-c"} {
		createBasicFunction(t, client, n)
	}

	page1, err := client.ListFunctions(ctx, &awslambda.ListFunctionsInput{MaxItems: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListFunctions page1: %v", err)
	}
	if len(page1.Functions) != 2 {
		t.Fatalf("page1 = %d functions, want 2", len(page1.Functions))
	}
	if aws.ToString(page1.NextMarker) == "" {
		t.Fatal("NextMarker empty on truncated page, want a marker")
	}

	page2, err := client.ListFunctions(ctx, &awslambda.ListFunctionsInput{
		MaxItems: aws.Int32(2),
		Marker:   page1.NextMarker,
	})
	if err != nil {
		t.Fatalf("ListFunctions page2: %v", err)
	}
	if len(page2.Functions) != 1 {
		t.Fatalf("page2 = %d functions, want 1", len(page2.Functions))
	}
	if aws.ToString(page2.NextMarker) != "" {
		t.Fatalf("page2 NextMarker = %q, want empty on last page", aws.ToString(page2.NextMarker))
	}
}

// TestSDKEventSourceMappingFunctionArn covers Create/Get returning the bare
// function name in FunctionArn instead of a full ARN.
func TestSDKEventSourceMappingFunctionArn(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "consumer")

	created, err := client.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		FunctionName:   aws.String("consumer"),
		EventSourceArn: aws.String("arn:aws:sqs:us-east-1:000000000000:my-queue"),
	})
	if err != nil {
		t.Fatalf("CreateEventSourceMapping: %v", err)
	}

	arn := aws.ToString(created.FunctionArn)
	if !strings.HasPrefix(arn, "arn:aws:lambda:") || !strings.HasSuffix(arn, ":function:consumer") {
		t.Fatalf("FunctionArn = %q, want a full lambda function ARN", arn)
	}

	got, err := client.GetEventSourceMapping(ctx, &awslambda.GetEventSourceMappingInput{UUID: created.UUID})
	if err != nil {
		t.Fatalf("GetEventSourceMapping: %v", err)
	}
	if aws.ToString(got.FunctionArn) != arn {
		t.Fatalf("Get FunctionArn = %q, want %q", aws.ToString(got.FunctionArn), arn)
	}
}

// TestSDKDeleteEventSourceMappingReturnsConfig covers DeleteEventSourceMapping
// returning the full EventSourceMappingConfiguration with State "Deleting"
// rather than an empty body, so a caller can read UUID/State off the response.
func TestSDKDeleteEventSourceMappingReturnsConfig(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "delconsumer")

	created, err := client.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		FunctionName:   aws.String("delconsumer"),
		EventSourceArn: aws.String("arn:aws:sqs:us-east-1:000000000000:del-queue"),
	})
	if err != nil {
		t.Fatalf("CreateEventSourceMapping: %v", err)
	}

	del, err := client.DeleteEventSourceMapping(ctx, &awslambda.DeleteEventSourceMappingInput{
		UUID: created.UUID,
	})
	if err != nil {
		t.Fatalf("DeleteEventSourceMapping: %v", err)
	}

	if aws.ToString(del.UUID) != aws.ToString(created.UUID) {
		t.Fatalf("Delete UUID = %q, want %q", aws.ToString(del.UUID), aws.ToString(created.UUID))
	}
	if aws.ToString(del.State) != "Deleting" {
		t.Fatalf("Delete State = %q, want Deleting", aws.ToString(del.State))
	}
	if aws.ToString(del.FunctionArn) == "" {
		t.Fatal("Delete FunctionArn empty, want the full function ARN")
	}

	// The mapping is gone afterward.
	if _, err := client.GetEventSourceMapping(ctx, &awslambda.GetEventSourceMappingInput{
		UUID: created.UUID,
	}); err == nil {
		t.Fatal("GetEventSourceMapping after delete = nil error, want ResourceNotFoundException")
	}
}

// TestSDKListEventSourceMappingsByEventSourceArn covers the EventSourceArn query
// filter: listing with an EventSourceArn returns only mappings for that source.
func TestSDKListEventSourceMappingsByEventSourceArn(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "multi")

	const (
		arnA = "arn:aws:sqs:us-east-1:000000000000:queue-a"
		arnB = "arn:aws:sqs:us-east-1:000000000000:queue-b"
	)

	for _, src := range []string{arnA, arnB} {
		if _, err := client.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
			FunctionName:   aws.String("multi"),
			EventSourceArn: aws.String(src),
		}); err != nil {
			t.Fatalf("CreateEventSourceMapping(%s): %v", src, err)
		}
	}

	filtered, err := client.ListEventSourceMappings(ctx, &awslambda.ListEventSourceMappingsInput{
		EventSourceArn: aws.String(arnA),
	})
	if err != nil {
		t.Fatalf("ListEventSourceMappings(EventSourceArn=A): %v", err)
	}

	if len(filtered.EventSourceMappings) != 1 {
		t.Fatalf("filtered mappings = %d, want 1", len(filtered.EventSourceMappings))
	}
	if aws.ToString(filtered.EventSourceMappings[0].EventSourceArn) != arnA {
		t.Fatalf("filtered EventSourceArn = %q, want %q",
			aws.ToString(filtered.EventSourceMappings[0].EventSourceArn), arnA)
	}

	// No filter returns both.
	all, err := client.ListEventSourceMappings(ctx, &awslambda.ListEventSourceMappingsInput{})
	if err != nil {
		t.Fatalf("ListEventSourceMappings(all): %v", err)
	}
	if len(all.EventSourceMappings) != 2 {
		t.Fatalf("unfiltered mappings = %d, want 2", len(all.EventSourceMappings))
	}
}

// TestSDKCreateAliasRoutingConfigValidation covers RoutingConfig validation:
// a weight on $LATEST is InvalidParameterValueException, a weight on a
// nonexistent version is ResourceNotFoundException, and a weight on a real
// published version succeeds.
func TestSDKCreateAliasRoutingConfigValidation(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "routed")

	for range 2 {
		if _, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{
			FunctionName: aws.String("routed"),
		}); err != nil {
			t.Fatalf("PublishVersion: %v", err)
		}
	}

	// $LATEST in the weights is rejected with InvalidParameterValueException.
	_, err := client.CreateAlias(ctx, &awslambda.CreateAliasInput{
		FunctionName:    aws.String("routed"),
		Name:            aws.String("bad-latest"),
		FunctionVersion: aws.String("1"),
		RoutingConfig: &lambdatypes.AliasRoutingConfiguration{
			AdditionalVersionWeights: map[string]float64{"$LATEST": 0.1},
		},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("CreateAlias($LATEST weight) err = %v, want InvalidParameterValueException", err)
	}

	// The alias's own FunctionVersion cannot be $LATEST when a routing config is
	// present — a weighted alias's primary target must be a published version.
	_, err = client.CreateAlias(ctx, &awslambda.CreateAliasInput{
		FunctionName:    aws.String("routed"),
		Name:            aws.String("bad-primary-latest"),
		FunctionVersion: aws.String("$LATEST"),
		RoutingConfig: &lambdatypes.AliasRoutingConfiguration{
			AdditionalVersionWeights: map[string]float64{"1": 0.1},
		},
	})

	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("CreateAlias($LATEST primary) err = %v, want InvalidParameterValueException", err)
	}

	// A nonexistent version is ResourceNotFoundException.
	_, err = client.CreateAlias(ctx, &awslambda.CreateAliasInput{
		FunctionName:    aws.String("routed"),
		Name:            aws.String("bad-missing"),
		FunctionVersion: aws.String("1"),
		RoutingConfig: &lambdatypes.AliasRoutingConfiguration{
			AdditionalVersionWeights: map[string]float64{"99": 0.1},
		},
	})

	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ResourceNotFoundException" {
		t.Fatalf("CreateAlias(missing version weight) err = %v, want ResourceNotFoundException", err)
	}

	// A real version succeeds.
	created, err := client.CreateAlias(ctx, &awslambda.CreateAliasInput{
		FunctionName:    aws.String("routed"),
		Name:            aws.String("good"),
		FunctionVersion: aws.String("1"),
		RoutingConfig: &lambdatypes.AliasRoutingConfiguration{
			AdditionalVersionWeights: map[string]float64{"2": 0.2},
		},
	})
	if err != nil {
		t.Fatalf("CreateAlias(valid weight): %v", err)
	}
	if created.RoutingConfig == nil || created.RoutingConfig.AdditionalVersionWeights["2"] != 0.2 {
		t.Fatalf("RoutingConfig = %+v, want version 2 weight 0.2", created.RoutingConfig)
	}

	// A plain alias may point to $LATEST; adding a routing config to it later
	// (leaving the effective FunctionVersion at $LATEST) must be rejected.
	if _, err = client.CreateAlias(ctx, &awslambda.CreateAliasInput{
		FunctionName:    aws.String("routed"),
		Name:            aws.String("plain-latest"),
		FunctionVersion: aws.String("$LATEST"),
	}); err != nil {
		t.Fatalf("CreateAlias(plain $LATEST): %v", err)
	}

	_, err = client.UpdateAlias(ctx, &awslambda.UpdateAliasInput{
		FunctionName: aws.String("routed"),
		Name:         aws.String("plain-latest"),
		RoutingConfig: &lambdatypes.AliasRoutingConfiguration{
			AdditionalVersionWeights: map[string]float64{"1": 0.1},
		},
	})

	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("UpdateAlias(routing on $LATEST) err = %v, want InvalidParameterValueException", err)
	}
}

// TestSDKVersionPerVersionAttributes covers PublishVersion/ListVersionsByFunction
// leaving per-version CodeSha256/RevisionId empty and reusing $LATEST config.
func TestSDKVersionPerVersionAttributes(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "ver")

	pub, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{
		FunctionName: aws.String("ver"),
		Description:  aws.String("v1"),
	})
	if err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}
	if aws.ToString(pub.Version) != "1" {
		t.Fatalf("Version = %q, want 1", aws.ToString(pub.Version))
	}
	if aws.ToString(pub.CodeSha256) == "" {
		t.Fatal("published version CodeSha256 empty")
	}
	if aws.ToString(pub.RevisionId) == "" {
		t.Fatal("published version RevisionId empty")
	}
	if !strings.HasSuffix(aws.ToString(pub.FunctionArn), ":function:ver:1") {
		t.Fatalf("FunctionArn = %q, want qualified with :1", aws.ToString(pub.FunctionArn))
	}

	list, err := client.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{
		FunctionName: aws.String("ver"),
	})
	if err != nil {
		t.Fatalf("ListVersionsByFunction: %v", err)
	}
	if len(list.Versions) != 2 { // $LATEST + version 1
		t.Fatalf("Versions = %d, want 2 ($LATEST + 1)", len(list.Versions))
	}
	for _, v := range list.Versions {
		if aws.ToString(v.CodeSha256) == "" {
			t.Fatalf("version %q CodeSha256 empty", aws.ToString(v.Version))
		}
		if v.Runtime != lambdatypes.RuntimeGo1x {
			t.Fatalf("version %q Runtime = %q, want go1.x", aws.ToString(v.Version), v.Runtime)
		}
	}
}

// TestSDKAliasRevisionAndRouting covers CreateAlias/GetAlias/UpdateAlias missing
// RevisionId and RoutingConfig.
func TestSDKAliasRevisionAndRouting(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "aliased")

	for range 2 {
		if _, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{
			FunctionName: aws.String("aliased"),
		}); err != nil {
			t.Fatalf("PublishVersion: %v", err)
		}
	}

	created, err := client.CreateAlias(ctx, &awslambda.CreateAliasInput{
		FunctionName:    aws.String("aliased"),
		Name:            aws.String("live"),
		FunctionVersion: aws.String("1"),
	})
	if err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}
	if aws.ToString(created.RevisionId) == "" {
		t.Fatal("alias RevisionId empty on create")
	}

	updated, err := client.UpdateAlias(ctx, &awslambda.UpdateAliasInput{
		FunctionName:    aws.String("aliased"),
		Name:            aws.String("live"),
		FunctionVersion: aws.String("1"),
		RoutingConfig: &lambdatypes.AliasRoutingConfiguration{
			AdditionalVersionWeights: map[string]float64{"2": 0.1},
		},
	})
	if err != nil {
		t.Fatalf("UpdateAlias: %v", err)
	}
	if aws.ToString(updated.RevisionId) == aws.ToString(created.RevisionId) {
		t.Fatal("alias RevisionId unchanged after update")
	}
	if updated.RoutingConfig == nil || updated.RoutingConfig.AdditionalVersionWeights["2"] != 0.1 {
		t.Fatalf("RoutingConfig = %+v, want version 2 weight 0.1", updated.RoutingConfig)
	}
}

// TestSDKFunctionURLLifecycle covers the Function URL API (Create/Get/Update/
// List/Delete) previously returning 501 with a non-JSON body because the
// /2021-10-31/.../url path wasn't matched.
func TestSDKFunctionURLLifecycle(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "urlfn")

	created, err := client.CreateFunctionUrlConfig(ctx, &awslambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String("urlfn"),
		AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
	})
	if err != nil {
		t.Fatalf("CreateFunctionUrlConfig: %v", err)
	}
	if !strings.HasPrefix(aws.ToString(created.FunctionUrl), "https://") {
		t.Fatalf("FunctionUrl = %q, want an https url", aws.ToString(created.FunctionUrl))
	}
	if created.AuthType != lambdatypes.FunctionUrlAuthTypeNone {
		t.Fatalf("AuthType = %q, want NONE", created.AuthType)
	}

	got, err := client.GetFunctionUrlConfig(ctx, &awslambda.GetFunctionUrlConfigInput{
		FunctionName: aws.String("urlfn"),
	})
	if err != nil {
		t.Fatalf("GetFunctionUrlConfig: %v", err)
	}
	if aws.ToString(got.FunctionUrl) != aws.ToString(created.FunctionUrl) {
		t.Fatalf("Get FunctionUrl = %q, want %q", aws.ToString(got.FunctionUrl), aws.ToString(created.FunctionUrl))
	}

	if _, err := client.UpdateFunctionUrlConfig(ctx, &awslambda.UpdateFunctionUrlConfigInput{
		FunctionName: aws.String("urlfn"),
		AuthType:     lambdatypes.FunctionUrlAuthTypeAwsIam,
	}); err != nil {
		t.Fatalf("UpdateFunctionUrlConfig: %v", err)
	}

	afterUpdate, err := client.GetFunctionUrlConfig(ctx, &awslambda.GetFunctionUrlConfigInput{
		FunctionName: aws.String("urlfn"),
	})
	if err != nil {
		t.Fatalf("GetFunctionUrlConfig after update: %v", err)
	}
	if afterUpdate.AuthType != lambdatypes.FunctionUrlAuthTypeAwsIam {
		t.Fatalf("AuthType = %q, want AWS_IAM after update", afterUpdate.AuthType)
	}

	list, err := client.ListFunctionUrlConfigs(ctx, &awslambda.ListFunctionUrlConfigsInput{
		FunctionName: aws.String("urlfn"),
	})
	if err != nil {
		t.Fatalf("ListFunctionUrlConfigs: %v", err)
	}
	if len(list.FunctionUrlConfigs) != 1 {
		t.Fatalf("FunctionUrlConfigs = %d, want 1", len(list.FunctionUrlConfigs))
	}

	if _, err := client.DeleteFunctionUrlConfig(ctx, &awslambda.DeleteFunctionUrlConfigInput{
		FunctionName: aws.String("urlfn"),
	}); err != nil {
		t.Fatalf("DeleteFunctionUrlConfig: %v", err)
	}

	if _, err := client.GetFunctionUrlConfig(ctx, &awslambda.GetFunctionUrlConfigInput{
		FunctionName: aws.String("urlfn"),
	}); err == nil {
		t.Fatal("GetFunctionUrlConfig after delete returned nil error, want NotFound")
	}
}
