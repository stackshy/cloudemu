package lambda_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/smithy-go"
)

// zipWith returns a minimal valid zip archive containing a single file, so a
// function importing a layer can have the two packages merged (the overlay path
// unzips both).
func zipWith(t *testing.T, name, body string) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	f, err := zw.Create(name)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := f.Write([]byte(body)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	return buf.Bytes()
}

// TestSDKCreateFunctionPublish covers CreateFunction with Publish=true: AWS
// publishes version 1 and the response is that published version's configuration
// (Version "1", :1-qualified ARN). Before the fix Publish was ignored — the
// response stayed $LATEST and no version was cut, breaking Terraform
// aws_lambda_function{publish=true}.
func TestSDKCreateFunctionPublish(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("pubfn"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Publish:      true,
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	})
	if err != nil {
		t.Fatalf("CreateFunction(Publish): %v", err)
	}

	if aws.ToString(out.Version) != "1" {
		t.Fatalf("Version = %q, want 1", aws.ToString(out.Version))
	}
	if !strings.HasSuffix(aws.ToString(out.FunctionArn), ":function:pubfn:1") {
		t.Fatalf("FunctionArn = %q, want qualified with :1", aws.ToString(out.FunctionArn))
	}

	// The published version is retrievable via GetFunction Qualifier=1.
	got, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{
		FunctionName: aws.String("pubfn"),
		Qualifier:    aws.String("1"),
	})
	if err != nil {
		t.Fatalf("GetFunction(Qualifier=1): %v", err)
	}
	if aws.ToString(got.Configuration.Version) != "1" {
		t.Fatalf("Get Version = %q, want 1", aws.ToString(got.Configuration.Version))
	}
}

// TestSDKUpdateFunctionCodePublish covers UpdateFunctionCode with Publish=true
// cutting a new version (2, after the create-time $LATEST) and returning that
// version's qualified configuration.
func TestSDKUpdateFunctionCodePublish(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "updpub")

	out, err := client.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
		FunctionName: aws.String("updpub"),
		ZipFile:      []byte("new-code"),
		Publish:      true,
	})
	if err != nil {
		t.Fatalf("UpdateFunctionCode(Publish): %v", err)
	}

	if aws.ToString(out.Version) != "1" {
		t.Fatalf("Version = %q, want 1", aws.ToString(out.Version))
	}
	if !strings.HasSuffix(aws.ToString(out.FunctionArn), ":function:updpub:1") {
		t.Fatalf("FunctionArn = %q, want qualified with :1", aws.ToString(out.FunctionArn))
	}
}

// TestSDKArchitecturesRoundtrip covers the Architectures field: an explicit
// [arm64] round-trips and an omitted value defaults to [x86_64], matching AWS.
func TestSDKArchitecturesRoundtrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName:  aws.String("arm"),
		Runtime:       lambdatypes.RuntimeGo1x,
		Role:          aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:       aws.String("main"),
		Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureArm64},
		Code:          &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction(arm64): %v", err)
	}

	got, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("arm")})
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	if archs := got.Configuration.Architectures; len(archs) != 1 || archs[0] != lambdatypes.ArchitectureArm64 {
		t.Fatalf("Architectures = %v, want [arm64]", got.Configuration.Architectures)
	}

	// A function created without Architectures defaults to [x86_64].
	createBasicFunction(t, client, "x86")

	def, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("x86"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}
	if archs := def.Architectures; len(archs) != 1 || archs[0] != lambdatypes.ArchitectureX8664 {
		t.Fatalf("default Architectures = %v, want [x86_64]", def.Architectures)
	}
}

// TestSDKEphemeralStorageRoundtrip covers the EphemeralStorage field: an
// explicit size round-trips and an omitted value defaults to 512 MB, matching
// AWS. An out-of-range size is rejected with InvalidParameterValueException.
func TestSDKEphemeralStorageRoundtrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName:     aws.String("bigtmp"),
		Runtime:          lambdatypes.RuntimeGo1x,
		Role:             aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:          aws.String("main"),
		EphemeralStorage: &lambdatypes.EphemeralStorage{Size: aws.Int32(2048)},
		Code:             &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction(EphemeralStorage): %v", err)
	}

	got, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("bigtmp")})
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	if es := got.Configuration.EphemeralStorage; es == nil || aws.ToInt32(es.Size) != 2048 {
		t.Fatalf("EphemeralStorage = %+v, want Size 2048", got.Configuration.EphemeralStorage)
	}

	// A function created without EphemeralStorage defaults to 512 MB.
	createBasicFunction(t, client, "deftmp")

	def, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("deftmp"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}
	if es := def.EphemeralStorage; es == nil || aws.ToInt32(es.Size) != 512 {
		t.Fatalf("default EphemeralStorage = %+v, want Size 512", def.EphemeralStorage)
	}

	// A size below the 512 MB floor is rejected.
	_, err = client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName:     aws.String("toosmall"),
		Runtime:          lambdatypes.RuntimeGo1x,
		Role:             aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:          aws.String("main"),
		EphemeralStorage: &lambdatypes.EphemeralStorage{Size: aws.Int32(256)},
		Code:             &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("CreateFunction(size 256) err = %v, want InvalidParameterValueException", err)
	}
}

// TestSDKGetFunctionConcurrencyUnset covers GetFunctionConcurrency returning
// HTTP 200 with an empty body when the function has no reserved concurrency set
// (only a MISSING function is 404). Before the fix the provider's NotFound for
// "concurrency unset" was mapped to a 404, breaking callers that read
// concurrency on functions that never set it.
func TestSDKGetFunctionConcurrencyUnset(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "noconc")

	got, err := client.GetFunctionConcurrency(ctx, &awslambda.GetFunctionConcurrencyInput{
		FunctionName: aws.String("noconc"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConcurrency(unset): %v, want 200 empty", err)
	}
	if got.ReservedConcurrentExecutions != nil {
		t.Fatalf("ReservedConcurrentExecutions = %v, want nil when unset", *got.ReservedConcurrentExecutions)
	}

	// A missing function is still ResourceNotFoundException.
	_, err = client.GetFunctionConcurrency(ctx, &awslambda.GetFunctionConcurrencyInput{
		FunctionName: aws.String("ghost"),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ResourceNotFoundException" {
		t.Fatalf("GetFunctionConcurrency(missing) err = %v, want ResourceNotFoundException", err)
	}
}

// TestSDKCreateFunctionLayersEchoed covers CreateFunction echoing the imported
// layers in the function configuration's Layers list (with Arn and a non-zero
// CodeSize). Before the fix Layers came back empty even when layers were passed.
func TestSDKCreateFunctionLayersEchoed(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	pub, err := client.PublishLayerVersion(ctx, &awslambda.PublishLayerVersionInput{
		LayerName:          aws.String("shared"),
		CompatibleRuntimes: []lambdatypes.Runtime{lambdatypes.RuntimeGo1x},
		Content:            &lambdatypes.LayerVersionContentInput{ZipFile: zipWith(t, "lib.txt", "lib")},
	})
	if err != nil {
		t.Fatalf("PublishLayerVersion: %v", err)
	}
	layerARN := aws.ToString(pub.LayerVersionArn)

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("withlayers"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Layers:       []string{layerARN},
		Code:         &lambdatypes.FunctionCode{ZipFile: zipWith(t, "main.txt", "fn")},
	}); err != nil {
		t.Fatalf("CreateFunction(Layers): %v", err)
	}

	got, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("withlayers")})
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}

	layers := got.Configuration.Layers
	if len(layers) != 1 || aws.ToString(layers[0].Arn) != layerARN {
		t.Fatalf("Layers = %+v, want one with Arn %q", layers, layerARN)
	}
	if layers[0].CodeSize == 0 {
		t.Fatal("layer CodeSize = 0, want non-zero")
	}
}
