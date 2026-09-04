package lambda_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/smithy-go"
)

// TestSDKLambdaLayerLifecycle drives the full AWS Lambda Layers real-user
// workflow against the emulator through a real aws-sdk-go-v2 client: publish
// two versions (monotonic numbering), look them up by name+version and by
// ARN, list/paginate/filter, delete the older version and confirm the counter
// never reuses it, round-trip a layer version's resource-based policy, attach
// a layer to a function, and reject a function that references a layer
// version that doesn't exist.
func TestSDKLambdaLayerLifecycle(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	v1, err := client.PublishLayerVersion(ctx, &awslambda.PublishLayerVersionInput{
		LayerName:               aws.String("shared-deps"),
		Description:             aws.String("first"),
		CompatibleRuntimes:      []lambdatypes.Runtime{lambdatypes.RuntimePython39},
		CompatibleArchitectures: []lambdatypes.Architecture{lambdatypes.ArchitectureArm64},
		LicenseInfo:             aws.String("MIT"),
		Content:                 &lambdatypes.LayerVersionContentInput{ZipFile: zipWith(t, "lib.txt", "v1")},
	})
	if err != nil {
		t.Fatalf("PublishLayerVersion v1: %v", err)
	}

	if v1.Version != 1 {
		t.Fatalf("v1.Version = %d, want 1", v1.Version)
	}

	v2, err := client.PublishLayerVersion(ctx, &awslambda.PublishLayerVersionInput{
		LayerName: aws.String("shared-deps"),
		Content:   &lambdatypes.LayerVersionContentInput{ZipFile: zipWith(t, "lib2.txt", "v2")},
	})
	if err != nil {
		t.Fatalf("PublishLayerVersion v2: %v", err)
	}

	if v2.Version != 2 {
		t.Fatalf("v2.Version = %d, want 2", v2.Version)
	}

	v2Arn := aws.ToString(v2.LayerVersionArn)

	t.Run("GetLayerVersion and GetLayerVersionByArn agree", func(t *testing.T) {
		byName, err := client.GetLayerVersion(ctx, &awslambda.GetLayerVersionInput{
			LayerName: aws.String("shared-deps"), VersionNumber: aws.Int64(1),
		})
		if err != nil {
			t.Fatalf("GetLayerVersion: %v", err)
		}

		if aws.ToString(byName.LicenseInfo) != "MIT" || len(byName.CompatibleArchitectures) != 1 {
			t.Fatalf("GetLayerVersion(1) = %+v, want LicenseInfo MIT and one CompatibleArchitecture", byName)
		}

		byArn, err := client.GetLayerVersionByArn(ctx, &awslambda.GetLayerVersionByArnInput{Arn: aws.String(v2Arn)})
		if err != nil {
			t.Fatalf("GetLayerVersionByArn: %v", err)
		}

		if byArn.Version != 2 || aws.ToString(byArn.LayerVersionArn) != v2Arn {
			t.Fatalf("GetLayerVersionByArn = %+v, want version 2 arn %s", byArn, v2Arn)
		}
	})

	t.Run("ListLayerVersions and ListLayers see both versions", func(t *testing.T) {
		lv, err := client.ListLayerVersions(ctx, &awslambda.ListLayerVersionsInput{LayerName: aws.String("shared-deps")})
		if err != nil {
			t.Fatalf("ListLayerVersions: %v", err)
		}

		if len(lv.LayerVersions) != 2 {
			t.Fatalf("LayerVersions = %+v, want 2", lv.LayerVersions)
		}

		ll, err := client.ListLayers(ctx, &awslambda.ListLayersInput{})
		if err != nil {
			t.Fatalf("ListLayers: %v", err)
		}

		if len(ll.Layers) != 1 || ll.Layers[0].LatestMatchingVersion.Version != 2 {
			t.Fatalf("ListLayers = %+v, want one layer latest version 2", ll.Layers)
		}
	})

	t.Run("filters exclude a non-matching runtime", func(t *testing.T) {
		lv, err := client.ListLayerVersions(ctx, &awslambda.ListLayerVersionsInput{
			LayerName: aws.String("shared-deps"), CompatibleRuntime: lambdatypes.RuntimeNodejs18x,
		})
		if err != nil {
			t.Fatalf("ListLayerVersions filtered: %v", err)
		}

		if len(lv.LayerVersions) != 0 {
			t.Fatalf("filtered LayerVersions = %+v, want none (v1's runtime is python3.9, not nodejs18.x)", lv.LayerVersions)
		}
	})

	t.Run("delete does not renumber", func(t *testing.T) {
		if _, err := client.DeleteLayerVersion(ctx, &awslambda.DeleteLayerVersionInput{
			LayerName: aws.String("shared-deps"), VersionNumber: aws.Int64(1),
		}); err != nil {
			t.Fatalf("DeleteLayerVersion: %v", err)
		}

		lv, err := client.ListLayerVersions(ctx, &awslambda.ListLayerVersionsInput{LayerName: aws.String("shared-deps")})
		if err != nil {
			t.Fatalf("ListLayerVersions after delete: %v", err)
		}

		if len(lv.LayerVersions) != 1 || lv.LayerVersions[0].Version != 2 {
			t.Fatalf("LayerVersions after delete = %+v, want only version 2", lv.LayerVersions)
		}

		v3, err := client.PublishLayerVersion(ctx, &awslambda.PublishLayerVersionInput{
			LayerName: aws.String("shared-deps"),
			Content:   &lambdatypes.LayerVersionContentInput{ZipFile: zipWith(t, "lib3.txt", "v3")},
		})
		if err != nil {
			t.Fatalf("PublishLayerVersion v3: %v", err)
		}

		if v3.Version != 3 {
			t.Fatalf("v3.Version = %d, want 3 (must not reuse deleted version 1)", v3.Version)
		}
	})

	var policyRevision string

	t.Run("layer version permission round trip", func(t *testing.T) {
		add, err := client.AddLayerVersionPermission(ctx, &awslambda.AddLayerVersionPermissionInput{
			LayerName:     aws.String("shared-deps"),
			VersionNumber: aws.Int64(2),
			StatementId:   aws.String("xaccount"),
			Action:        aws.String("lambda:GetLayerVersion"),
			Principal:     aws.String("111111111111"),
		})
		if err != nil {
			t.Fatalf("AddLayerVersionPermission: %v", err)
		}

		if aws.ToString(add.RevisionId) == "" || !strings.Contains(aws.ToString(add.Statement), "xaccount") {
			t.Fatalf("AddLayerVersionPermission response = %+v, want a RevisionId and Statement carrying the Sid", add)
		}

		policyRevision = aws.ToString(add.RevisionId)

		policy, err := client.GetLayerVersionPolicy(ctx, &awslambda.GetLayerVersionPolicyInput{
			LayerName: aws.String("shared-deps"), VersionNumber: aws.Int64(2),
		})
		if err != nil {
			t.Fatalf("GetLayerVersionPolicy: %v", err)
		}

		if aws.ToString(policy.RevisionId) != policyRevision || !strings.Contains(aws.ToString(policy.Policy), "111111111111") {
			t.Fatalf("GetLayerVersionPolicy = %+v, want revision %s and the granted principal", policy, policyRevision)
		}

		if _, err := client.RemoveLayerVersionPermission(ctx, &awslambda.RemoveLayerVersionPermissionInput{
			LayerName: aws.String("shared-deps"), VersionNumber: aws.Int64(2),
			StatementId: aws.String("xaccount"), RevisionId: aws.String(policyRevision),
		}); err != nil {
			t.Fatalf("RemoveLayerVersionPermission: %v", err)
		}

		if _, err := client.GetLayerVersionPolicy(ctx, &awslambda.GetLayerVersionPolicyInput{
			LayerName: aws.String("shared-deps"), VersionNumber: aws.Int64(2),
		}); err == nil {
			t.Fatal("GetLayerVersionPolicy after removing the only statement: want NotFound, got nil error")
		}
	})

	t.Run("CreateFunction with a valid layer succeeds and echoes it", func(t *testing.T) {
		_, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
			FunctionName: aws.String("uses-layer"),
			Runtime:      lambdatypes.RuntimeGo1x,
			Role:         aws.String("arn:aws:iam::000000000000:role/test"),
			Handler:      aws.String("main"),
			Code:         &lambdatypes.FunctionCode{ZipFile: zipWith(t, "main.go", "code")},
			Layers:       []string{v2Arn},
		})
		if err != nil {
			t.Fatalf("CreateFunction with a valid layer: %v", err)
		}

		cfg, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
			FunctionName: aws.String("uses-layer"),
		})
		if err != nil {
			t.Fatalf("GetFunctionConfiguration: %v", err)
		}

		if len(cfg.Layers) != 1 || aws.ToString(cfg.Layers[0].Arn) != v2Arn || cfg.Layers[0].CodeSize == 0 {
			t.Fatalf("GetFunctionConfiguration.Layers = %+v, want one entry with Arn %s and non-zero CodeSize", cfg.Layers, v2Arn)
		}
	})

	t.Run("CreateFunction with a bogus layer arn is rejected", func(t *testing.T) {
		bogusArn := "arn:aws:lambda:us-east-1:000000000000:layer:shared-deps:999"

		_, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
			FunctionName: aws.String("bad-layer-fn"),
			Runtime:      lambdatypes.RuntimeGo1x,
			Role:         aws.String("arn:aws:iam::000000000000:role/test"),
			Handler:      aws.String("main"),
			Code:         &lambdatypes.FunctionCode{ZipFile: []byte("code")},
			Layers:       []string{bogusArn},
		})

		var apiErr smithy.APIError
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
			t.Fatalf("CreateFunction with a bogus layer arn err = %v, want InvalidParameterValueException", err)
		}

		if _, gerr := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("bad-layer-fn")}); gerr == nil {
			t.Fatal("function with a bogus layer arn was created despite the rejected request")
		}
	})

	t.Run("UpdateFunctionConfiguration with a bogus layer arn is rejected", func(t *testing.T) {
		bogusArn := "arn:aws:lambda:us-east-1:000000000000:layer:does-not-exist:1"

		_, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String("uses-layer"),
			Layers:       []string{bogusArn},
		})

		var apiErr smithy.APIError
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
			t.Fatalf("UpdateFunctionConfiguration with a bogus layer arn err = %v, want InvalidParameterValueException", err)
		}
	})
}
