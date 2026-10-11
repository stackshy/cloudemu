package lambda_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// TestSDKRecreatedAliasStartsClean guards against a recreated alias inheriting
// the deleted alias's resource policy and function URL. Both are sub-resources
// of the alias ARN in real Lambda and go away with DeleteAlias.
func TestSDKRecreatedAliasStartsClean(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "alias-clean")

	createAlias := func() {
		t.Helper()

		if _, err := client.CreateAlias(ctx, &awslambda.CreateAliasInput{
			FunctionName: aws.String("alias-clean"), Name: aws.String("live"), FunctionVersion: aws.String("1"),
		}); err != nil {
			t.Fatalf("CreateAlias: %v", err)
		}
	}

	createAlias()

	if _, err := client.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("alias-clean"), Qualifier: aws.String("live"),
		StatementId: aws.String("old-grant"), Action: aws.String("lambda:InvokeFunction"),
		Principal: aws.String("111111111111"),
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}

	if _, err := client.CreateFunctionUrlConfig(ctx, &awslambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String("alias-clean"), Qualifier: aws.String("live"),
		AuthType: lambdatypes.FunctionUrlAuthTypeNone,
	}); err != nil {
		t.Fatalf("CreateFunctionUrlConfig: %v", err)
	}

	if _, err := client.DeleteAlias(ctx, &awslambda.DeleteAliasInput{
		FunctionName: aws.String("alias-clean"), Name: aws.String("live"),
	}); err != nil {
		t.Fatalf("DeleteAlias: %v", err)
	}

	createAlias()

	var nf *lambdatypes.ResourceNotFoundException

	_, err := client.GetPolicy(ctx, &awslambda.GetPolicyInput{
		FunctionName: aws.String("alias-clean"), Qualifier: aws.String("live"),
	})
	if !errors.As(err, &nf) {
		t.Fatalf("GetPolicy(live) after recreate err = %v, want ResourceNotFoundException", err)
	}

	_, err = client.GetFunctionUrlConfig(ctx, &awslambda.GetFunctionUrlConfigInput{
		FunctionName: aws.String("alias-clean"), Qualifier: aws.String("live"),
	})
	if !errors.As(err, &nf) {
		t.Fatalf("GetFunctionUrlConfig(live) after recreate err = %v, want ResourceNotFoundException", err)
	}
}

// TestSDKDeleteVersionDropsPolicy checks DeleteFunction with a version
// Qualifier also drops that version's resource policy.
func TestSDKDeleteVersionDropsPolicy(t *testing.T) {
	client, cloud := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "ver-clean")

	if _, err := client.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("ver-clean"), Qualifier: aws.String("1"),
		StatementId: aws.String("old-grant"), Action: aws.String("lambda:InvokeFunction"),
		Principal: aws.String("111111111111"),
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}

	if _, err := client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{
		FunctionName: aws.String("ver-clean"), Qualifier: aws.String("1"),
	}); err != nil {
		t.Fatalf("DeleteFunction(Qualifier=1): %v", err)
	}

	if _, stmts, _ := cloud.Lambda.PolicyStatements(ctx, "ver-clean", "1"); len(stmts) != 0 {
		t.Fatalf("version 1 policy statements after delete = %v, want none", stmts)
	}
}
