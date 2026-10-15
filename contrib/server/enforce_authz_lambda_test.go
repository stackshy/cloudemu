package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

func lambdaClient(t *testing.T, endpoint string, c aws.Credentials) *lambda.Client {
	t.Helper()

	return lambda.NewFromConfig(credsConfig(t, c), func(o *lambda.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

// urlGet calls a function URL by its host, on the server's listener.
func urlGet(t *testing.T, endpoint, functionURL string) (int, string) {
	t.Helper()

	u, err := url.Parse(functionURL)
	if err != nil {
		t.Fatalf("parse %s: %v", functionURL, err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint+"/", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Host = u.Host

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", u.Host, err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

// TestEnforceAuthLambda drives the real Lambda SDK against cloudemu serve
// with --enforce-auth: Invoke and the control plane are authorized on the
// function, version or alias ARN, and a NONE function URL is public only once
// its resource-based policy grants public access.
func TestEnforceAuthLambda(t *testing.T) {
	endpoint, stop := enforceAuthServer(t)
	defer stop()

	ctx := context.Background()
	boot := clientsFor(t, endpoint, seedBootUser(t, endpoint))
	admin := lambdaClient(t, endpoint, boot.cred)

	_, err := admin.CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: aws.String("app"), Runtime: lambdatypes.RuntimePython312, Handler: aws.String("index.handler"),
		Role: aws.String("arn:aws:iam::000000000000:role/r"), Code: &lambdatypes.FunctionCode{ZipFile: []byte("code")},
		Publish: true,
	})
	wantOK(t, "CreateFunction as boot", err)

	_, err = admin.CreateAlias(ctx, &lambda.CreateAliasInput{
		FunctionName: aws.String("app"), Name: aws.String("prod"), FunctionVersion: aws.String("1"),
	})
	wantOK(t, "CreateAlias as boot", err)

	got, err := admin.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String("app")})
	wantOK(t, "GetFunction as boot", err)

	arn := aws.ToString(got.Configuration.FunctionArn)
	invoker := lambdaClient(t, endpoint, boot.newUser(t, "invoker", `{"Version":"2012-10-17","Statement":[`+
		`{"Effect":"Allow","Action":"lambda:InvokeFunction","Resource":"`+arn+`:prod"},`+
		`{"Effect":"Allow","Action":"lambda:GetFunction","Resource":"`+arn+`"}]}`))

	_, err = invoker.Invoke(ctx, &lambda.InvokeInput{FunctionName: aws.String("app"), Qualifier: aws.String("prod")})
	wantOK(t, "Invoke the alias", err)
	_, err = invoker.Invoke(ctx, &lambda.InvokeInput{FunctionName: aws.String("app:prod")})
	wantOK(t, "Invoke name:alias", err)
	_, err = invoker.Invoke(ctx, &lambda.InvokeInput{FunctionName: aws.String("app")})
	wantCode(t, "Invoke unqualified", err, "AccessDeniedException")
	_, err = invoker.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String("app")})
	wantOK(t, "GetFunction", err)
	_, err = invoker.DeleteFunction(ctx, &lambda.DeleteFunctionInput{FunctionName: aws.String("app")})
	wantCode(t, "DeleteFunction", err, "AccessDeniedException")

	if !strings.Contains(err.Error(), "lambda:DeleteFunction on resource: "+arn) {
		t.Fatalf("deny message = %v", err)
	}

	u, err := admin.CreateFunctionUrlConfig(ctx, &lambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String("app"), AuthType: lambdatypes.FunctionUrlAuthTypeNone,
	})
	wantOK(t, "CreateFunctionUrlConfig", err)

	if status, body := urlGet(t, endpoint, aws.ToString(u.FunctionUrl)); status != http.StatusForbidden {
		t.Fatalf("NONE URL without a public grant: %d %s, want 403", status, body)
	}

	_, err = admin.AddPermission(ctx, &lambda.AddPermissionInput{
		FunctionName: aws.String("app"), StatementId: aws.String("url"), Action: aws.String("lambda:InvokeFunctionUrl"),
		Principal: aws.String("*"), FunctionUrlAuthType: lambdatypes.FunctionUrlAuthTypeNone,
	})
	wantOK(t, "AddPermission InvokeFunctionUrl", err)
	_, err = admin.AddPermission(ctx, &lambda.AddPermissionInput{
		FunctionName: aws.String("app"), StatementId: aws.String("invoke"), Action: aws.String("lambda:InvokeFunction"),
		Principal: aws.String("*"), InvokedViaFunctionUrl: aws.Bool(true),
	})
	wantOK(t, "AddPermission InvokeFunction", err)

	if status, body := urlGet(t, endpoint, aws.ToString(u.FunctionUrl)); status != http.StatusOK {
		t.Fatalf("NONE URL with the public grant: %d %s, want 200", status, body)
	}
}
