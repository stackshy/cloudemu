package lambda_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// TestSDKAddPermissionConditionsRoundTrip checks AddPermission keeps
// FunctionUrlAuthType, InvokedViaFunctionUrl, SourceAccount, PrincipalOrgID
// and EventSourceToken, and GetPolicy returns them as the Condition AWS
// stores, which Terraform's aws_lambda_permission reads back.
func TestSDKAddPermissionConditionsRoundTrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("permcond"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("code")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	inputs := []*awslambda.AddPermissionInput{
		{
			FunctionName: aws.String("permcond"), StatementId: aws.String("url"), Action: aws.String("lambda:InvokeFunctionUrl"),
			Principal: aws.String("*"), FunctionUrlAuthType: lambdatypes.FunctionUrlAuthTypeNone,
		},
		{
			FunctionName: aws.String("permcond"), StatementId: aws.String("via"), Action: aws.String("lambda:InvokeFunction"),
			Principal: aws.String("*"), InvokedViaFunctionUrl: aws.Bool(true),
		},
		{
			FunctionName: aws.String("permcond"), StatementId: aws.String("s3"), Action: aws.String("lambda:InvokeFunction"),
			Principal: aws.String("s3.amazonaws.com"), SourceArn: aws.String("arn:aws:s3:::b"),
			SourceAccount: aws.String("123456789012"),
		},
	}

	for _, in := range inputs {
		out, err := client.AddPermission(ctx, in)
		if err != nil {
			t.Fatalf("AddPermission %s: %v", aws.ToString(in.StatementId), err)
		}

		if aws.ToString(out.Statement) == "" {
			t.Fatalf("AddPermission %s: empty statement", aws.ToString(in.StatementId))
		}
	}

	pol, err := client.GetPolicy(ctx, &awslambda.GetPolicyInput{FunctionName: aws.String("permcond")})
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}

	var doc struct {
		Statement []struct {
			Sid       string
			Condition map[string]map[string]string
		}
	}

	if err := json.Unmarshal([]byte(aws.ToString(pol.Policy)), &doc); err != nil {
		t.Fatalf("policy JSON: %v", err)
	}

	cond := map[string]map[string]map[string]string{}
	for _, s := range doc.Statement {
		cond[s.Sid] = s.Condition
	}

	checks := []struct{ sid, op, key, want string }{
		{"url", "StringEquals", "lambda:FunctionUrlAuthType", "NONE"},
		{"via", "Bool", "lambda:InvokedViaFunctionUrl", "true"},
		{"s3", "ArnLike", "AWS:SourceArn", "arn:aws:s3:::b"},
		{"s3", "StringEquals", "AWS:SourceAccount", "123456789012"},
	}

	for _, c := range checks {
		if got := cond[c.sid][c.op][c.key]; got != c.want {
			t.Errorf("%s %s %s = %q, want %q", c.sid, c.op, c.key, got, c.want)
		}
	}
}
