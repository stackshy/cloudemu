package lambda_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// TestSDKAliasNotFoundParity: DeleteAlias of an alias the function does not
// have succeeds (HTTP 204), UpdateAlias of one is ResourceNotFoundException
// "Alias not found: <function ARN>:<alias>", and DeleteAlias on a missing
// function is still ResourceNotFoundException.
func TestSDKAliasNotFoundParity(t *testing.T) {
	c := newParityLambdaClient(t)
	ctx := context.Background()

	created := createParityFunction(t, c, "aliasfn", []byte("zip"))

	if _, err := c.DeleteAlias(ctx, &awslambda.DeleteAliasInput{
		FunctionName: aws.String("aliasfn"), Name: aws.String("non-existent"),
	}); err != nil {
		t.Errorf("DeleteAlias(missing alias) error = %v, want success", err)
	}

	_, err := c.UpdateAlias(ctx, &awslambda.UpdateAliasInput{
		FunctionName: aws.String("aliasfn"), Name: aws.String("non-existent"),
	})

	var nf *lambdatypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("UpdateAlias(missing alias) error = %v, want ResourceNotFoundException", err)
	}

	if want := "Alias not found: " + aws.ToString(created.FunctionArn) + ":non-existent"; aws.ToString(nf.Message) != want {
		t.Errorf("UpdateAlias message = %q, want %q", aws.ToString(nf.Message), want)
	}

	// ResourceNotFoundException is not a modeled DeleteAlias error, so the SDK
	// surfaces it as a generic API error; check the code.
	_, err = c.DeleteAlias(ctx, &awslambda.DeleteAliasInput{
		FunctionName: aws.String("ghost"), Name: aws.String("live"),
	})
	if errorCode(err) != "ResourceNotFoundException" {
		t.Errorf("DeleteAlias(missing function) error = %v, want ResourceNotFoundException", err)
	}
}
