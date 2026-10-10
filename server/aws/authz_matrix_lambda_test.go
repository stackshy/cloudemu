package aws

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

const (
	// lambdaDeny is the body of Lambda's AccessDeniedException.
	lambdaDeny = `"Type":"User"`
	// urlForbidden is the body of a denied call through a function URL.
	urlForbidden = "Forbidden. For troubleshooting Function URL authorization issues"
	fnARN        = "arn:aws:lambda:us-east-1:123456789012:function:"
	acctID       = "123456789012"
)

func stmt(effect, action, resource string) string {
	return `{"Effect":"` + effect + `","Action":"` + action + `","Resource":"` + resource + `"}`
}

func stmtCond(effect, action, resource, cond string) string {
	return `{"Effect":"` + effect + `","Action":"` + action + `","Resource":"` + resource + `","Condition":` + cond + `}`
}

// newFunction creates fn directly through the driver, with a published
// version 1 and an alias prod on it.
func newFunction(t *testing.T, cloud *awsprovider.Provider, name string, tags map[string]string) {
	t.Helper()

	ctx := context.Background()

	if _, err := cloud.Lambda.CreateFunction(ctx, sdrv.FunctionConfig{
		Name: name, Runtime: "python3.12", Handler: "index.handler", Role: "arn:aws:iam::123456789012:role/r", Tags: tags,
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := cloud.Lambda.PublishVersion(ctx, name, ""); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	if _, err := cloud.Lambda.CreateAlias(ctx, sdrv.AliasConfig{FunctionName: name, Name: "prod", FunctionVersion: "1"}); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}
}

func functionExists(cloud *awsprovider.Provider, name string) bool {
	_, err := cloud.Lambda.GetFunction(context.Background(), name)
	return err == nil
}

// invokeReq is a signed Invoke of function ref, with an optional query.
func invokeReq(ref string, query ...string) sreq {
	path := lambdaPath + "/" + ref + "/invocations"
	if len(query) > 0 {
		path += "?" + strings.Join(query, "&")
	}

	return sreq{path: path, ctype: "application/json", body: `{}`, service: "lambda"}
}

// TestAuthzMatrixLambdaInvoke covers qualified and unqualified function
// ARNs: a policy on the function does not cover its versions or aliases, and
// a policy on an alias covers only that alias.
func TestAuthzMatrixLambdaInvoke(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newFunction(t, cloud, "fn1", nil)

	invoker := userWithPolicy(t, cloud, "invoker", policyDoc(stmt("Allow", "lambda:InvokeFunction", fnARN+"fn1")))
	aliasInvoker := userWithPolicy(t, cloud, "aliasinvoker", policyDoc(stmt("Allow", "lambda:InvokeFunction", fnARN+"fn1:prod")))

	for _, tc := range []struct {
		name    string
		creds   string
		rq      sreq
		allowed bool
	}{
		{"unqualified", "invoker", invokeReq("fn1"), true},
		{"function ARN of another account names the local function", "invoker",
			invokeReq("arn:aws:lambda:eu-west-1:999999999999:function:fn1"), true},
		{"alias in the name", "invoker", invokeReq("fn1:prod"), false},
		{"alias in the query", "invoker", invokeReq("fn1", "Qualifier=prod"), false},
		{"alias for the alias user", "alias", invokeReq("fn1:prod"), true},
		{"alias by query for the alias user", "alias", invokeReq("fn1", "Qualifier=prod"), true},
		{"unqualified for the alias user", "alias", invokeReq("fn1"), false},
		{"version for the alias user", "alias", invokeReq("fn1", "Qualifier=1"), false},
		{"event invocation", "invoker", withHeader(invokeReq("fn1"), "X-Amz-Invocation-Type", "Event"), true},
		{"dry run", "invoker", withHeader(invokeReq("fn1"), "X-Amz-Invocation-Type", "DryRun"), true},
		{"GetFunction is a different action", "invoker", sreq{method: http.MethodGet, path: lambdaPath + "/fn1", service: "lambda"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			creds := invoker
			if tc.creds == "alias" {
				creds = aliasInvoker
			}

			status, body := doSigned(t, ts, creds, tc.rq)
			if tc.allowed {
				wantNotDenied(t, status, body)
				return
			}

			wantDenied(t, status, body, lambdaDeny)
		})
	}
}

func withHeader(rq sreq, k, v string) sreq {
	rq.header = map[string]string{k: v}
	return rq
}

// TestAuthzMatrixLambdaDenyOne checks a Deny on one Lambda action on one
// function blocks only that action on that function.
func TestAuthzMatrixLambdaDenyOne(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newFunction(t, cloud, "prod", nil)
	newFunction(t, cloud, "dev", nil)

	u := userWithPolicy(t, cloud, "denydelete", policyDoc(stmt("Allow", "*", "*"), stmt("Deny", "lambda:DeleteFunction", fnARN+"prod")))

	status, body := doSigned(t, ts, u, sreq{method: http.MethodDelete, path: lambdaPath + "/prod", service: "lambda"})
	wantDenied(t, status, body, "lambda:DeleteFunction on resource: "+fnARN+"prod with an explicit deny")

	if !functionExists(cloud, "prod") {
		t.Fatal("a denied DeleteFunction removed the function")
	}

	status, body = doSigned(t, ts, u, sreq{path: lambdaPath, ctype: "application/json",
		body: strings.Replace(lambdaCreate, "f1", "created", 1), service: "lambda"})
	wantNotDenied(t, status, body)

	status, body = doSigned(t, ts, u, sreq{method: http.MethodDelete, path: lambdaPath + "/dev", service: "lambda"})
	wantNotDenied(t, status, body)

	if functionExists(cloud, "dev") {
		t.Fatal("DeleteFunction on dev did not run")
	}
}

// TestAuthzMatrixLambdaConditions covers the tag, layer, function URL,
// principal and event source mapping condition keys.
func TestAuthzMatrixLambdaConditions(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newFunction(t, cloud, "devfn", map[string]string{"env": "dev"})
	newFunction(t, cloud, "prodfn", map[string]string{"env": "prod"})

	t.Run("aws:RequestTag on TagResource", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "tagger", policyDoc(stmtCond("Allow", "lambda:TagResource", "*",
			`{"StringEquals":{"aws:RequestTag/team":"a"}}`)))
		tagPath := "/2017-03-31/tags/" + fnARN + "devfn"

		status, body := doSigned(t, ts, u, sreq{path: tagPath, ctype: "application/json", body: `{"Tags":{"team":"a"}}`, service: "lambda"})
		wantNotDenied(t, status, body)

		status, body = doSigned(t, ts, u, sreq{path: tagPath, ctype: "application/json", body: `{"Tags":{"team":"b"}}`, service: "lambda"})
		wantDenied(t, status, body, lambdaDeny)
	})

	t.Run("aws:ResourceTag on GetFunction", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "devreader", policyDoc(stmtCond("Allow", "lambda:GetFunction", "*",
			`{"StringEquals":{"aws:ResourceTag/env":"dev"}}`)))

		status, body := doSigned(t, ts, u, sreq{method: http.MethodGet, path: lambdaPath + "/devfn", service: "lambda"})
		wantNotDenied(t, status, body)

		status, body = doSigned(t, ts, u, sreq{method: http.MethodGet, path: lambdaPath + "/prodfn", service: "lambda"})
		wantDenied(t, status, body, lambdaDeny)
	})

	t.Run("a layer needs lambda:GetLayerVersion", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "creator", policyDoc(stmt("Allow", "lambda:CreateFunction", "*")))
		withLayer := strings.Replace(lambdaCreate, `"f1",`, `"layered","Layers":["arn:aws:lambda:us-east-1:123456789012:layer:l:1"],`, 1)

		status, body := doSigned(t, ts, u, sreq{path: lambdaPath, ctype: "application/json", body: withLayer, service: "lambda"})
		wantDenied(t, status, body, "lambda:GetLayerVersion on resource: arn:aws:lambda:us-east-1:123456789012:layer:l:1")

		if functionExists(cloud, "layered") {
			t.Fatal("a denied CreateFunction created the function")
		}
	})

	t.Run("tagging at create needs lambda:TagResource", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "notagger", policyDoc(stmt("Allow", "lambda:CreateFunction", "*")))
		tagged := strings.Replace(lambdaCreate, `"f1",`, `"taggedfn","Tags":{"a":"b"},`, 1)

		status, body := doSigned(t, ts, u, sreq{path: lambdaPath, ctype: "application/json", body: tagged, service: "lambda"})
		wantDenied(t, status, body, "lambda:TagResource")
	})

	t.Run("lambda:FunctionUrlAuthType on CreateFunctionUrlConfig", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "urlmaker", policyDoc(stmtCond("Allow", "lambda:CreateFunctionUrlConfig", "*",
			`{"StringEquals":{"lambda:FunctionUrlAuthType":"AWS_IAM"}}`)))
		urlPath := "/2021-10-31/functions/devfn/url"

		status, body := doSigned(t, ts, u, sreq{path: urlPath, ctype: "application/json", body: `{"AuthType":"NONE"}`, service: "lambda"})
		wantDenied(t, status, body, lambdaDeny)

		status, body = doSigned(t, ts, u, sreq{path: urlPath, ctype: "application/json", body: `{"AuthType":"AWS_IAM"}`, service: "lambda"})
		wantNotDenied(t, status, body)
	})

	t.Run("lambda:Principal on AddPermission", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "granter", policyDoc(stmtCond("Allow", "lambda:AddPermission", "*",
			`{"StringEquals":{"lambda:Principal":"s3.amazonaws.com"}}`)))
		polPath := lambdaPath + "/prodfn/policy"

		status, body := doSigned(t, ts, u, sreq{path: polPath, ctype: "application/json", service: "lambda",
			body: `{"StatementId":"sns","Action":"lambda:InvokeFunction","Principal":"sns.amazonaws.com"}`})
		wantDenied(t, status, body, lambdaDeny)

		status, body = doSigned(t, ts, u, sreq{path: polPath, ctype: "application/json", service: "lambda",
			body: `{"StatementId":"s3","Action":"lambda:InvokeFunction","Principal":"s3.amazonaws.com"}`})
		wantNotDenied(t, status, body)
	})

	t.Run("lambda:FunctionArn on CreateEventSourceMapping", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "mapper", policyDoc(stmt("Allow", "*", "*"),
			stmtCond("Deny", "lambda:CreateEventSourceMapping", "*", `{"StringEquals":{"lambda:FunctionArn":"`+fnARN+`prodfn"}}`)))
		esm := `{"EventSourceArn":"arn:aws:sqs:us-east-1:123456789012:q","FunctionName":"prodfn"}`

		status, body := doSigned(t, ts, u, sreq{path: "/2015-03-31/event-source-mappings/", ctype: "application/json", body: esm, service: "lambda"})
		wantDenied(t, status, body, "with an explicit deny")
	})
}

// TestAuthzMatrixLambdaResourcePolicy covers the function's resource-based
// policy: within one account it grants Invoke to a principal it names, but a
// grant to the account only delegates to the caller's IAM policies.
func TestAuthzMatrixLambdaResourcePolicy(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newFunction(t, cloud, "shared", nil)

	ctx := context.Background()
	other := allow("dynamodb:ListTables")
	named := userWithPolicy(t, cloud, "named", other)
	unnamed := userWithPolicy(t, cloud, "unnamed", other)
	viaAccount := userWithPolicy(t, cloud, "viaaccount", other)

	for _, st := range []sdrv.PermissionStatement{
		{StatementID: "user", Action: "lambda:InvokeFunction", Principal: "arn:aws:iam::123456789012:user/named"},
		{StatementID: "acct", Action: "lambda:InvokeFunction", Principal: acctID},
		{StatementID: "svc", Action: "lambda:InvokeFunction", Principal: "s3.amazonaws.com"},
	} {
		if err := cloud.Lambda.AddPermission(ctx, "shared", "", st); err != nil {
			t.Fatalf("AddPermission: %v", err)
		}
	}

	status, body := doSigned(t, ts, named, invokeReq("shared"))
	wantNotDenied(t, status, body)

	status, body = doSigned(t, ts, named, invokeReq("shared:prod"))
	wantDenied(t, status, body, lambdaDeny)

	for _, c := range []aws.Credentials{unnamed, viaAccount} {
		status, body = doSigned(t, ts, c, invokeReq("shared"))
		wantDenied(t, status, body, "lambda:InvokeFunction on resource: "+fnARN+"shared")
	}
}

// functionURLHost creates a function URL for fn and returns its host.
func functionURLHost(t *testing.T, cloud *awsprovider.Provider, fn, authType string) string {
	t.Helper()

	cfg, err := cloud.Lambda.CreateFunctionURLConfig(context.Background(), sdrv.FunctionURLConfig{FunctionName: fn, AuthType: authType})
	if err != nil {
		t.Fatalf("CreateFunctionURLConfig: %v", err)
	}

	u, err := url.Parse(cfg.FunctionURL)
	if err != nil {
		t.Fatalf("parse %s: %v", cfg.FunctionURL, err)
	}

	return u.Host
}

func doUnsigned(t *testing.T, ts *httptest.Server, host string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/hello", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Host = host

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

// TestAuthzMatrixLambdaFunctionURL covers calls through function URLs: an
// AWS_IAM URL needs lambda:InvokeFunctionUrl and lambda:InvokeFunction from the
// caller; a NONE URL is public only when the resource-based policy grants both
// to everyone.
func TestAuthzMatrixLambdaFunctionURL(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newFunction(t, cloud, "iamurl", nil)
	newFunction(t, cloud, "puburl", nil)

	iamHost := functionURLHost(t, cloud, "iamurl", "AWS_IAM")
	pubHost := functionURLHost(t, cloud, "puburl", "NONE")
	urlReq := func(host string) sreq { return sreq{method: http.MethodGet, path: "/hello", host: host, service: "lambda"} }

	both := userWithPolicy(t, cloud, "urlboth", allow("lambda:InvokeFunctionUrl", "lambda:InvokeFunction"))
	urlOnly := userWithPolicy(t, cloud, "urlonly", allow("lambda:InvokeFunctionUrl"))

	t.Run("AWS_IAM with both actions", func(t *testing.T) {
		status, body := doSigned(t, ts, both, urlReq(iamHost))
		if status != http.StatusOK {
			t.Fatalf("status %d %s, want 200", status, body)
		}
	})

	t.Run("AWS_IAM with only InvokeFunctionUrl", func(t *testing.T) {
		status, body := doSigned(t, ts, urlOnly, urlReq(iamHost))
		wantDenied(t, status, body, urlForbidden)
	})

	t.Run("AWS_IAM unsigned", func(t *testing.T) {
		status, body := doUnsigned(t, ts, iamHost)
		wantDenied(t, status, body, "MissingAuthenticationToken")
	})

	t.Run("NONE without a public grant", func(t *testing.T) {
		status, body := doUnsigned(t, ts, pubHost)
		wantDenied(t, status, body, urlForbidden)
	})

	ctx := context.Background()
	if err := cloud.Lambda.AddPermission(ctx, "puburl", "", sdrv.PermissionStatement{
		StatementID: "url", Action: "lambda:InvokeFunctionUrl", Principal: "*", FunctionURLAuthType: "NONE",
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}

	t.Run("NONE with only the InvokeFunctionUrl grant", func(t *testing.T) {
		status, body := doUnsigned(t, ts, pubHost)
		wantDenied(t, status, body, urlForbidden)
	})

	if err := cloud.Lambda.AddPermission(ctx, "puburl", "", sdrv.PermissionStatement{
		StatementID: "invoke", Action: "lambda:InvokeFunction", Principal: "*", InvokedViaFunctionURL: true,
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}

	t.Run("NONE with the public grant", func(t *testing.T) {
		status, body := doUnsigned(t, ts, pubHost)
		if status != http.StatusOK {
			t.Fatalf("status %d %s, want 200", status, body)
		}
	})

	t.Run("the InvokedViaFunctionUrl grant does not allow a direct Invoke", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "direct", allow("dynamodb:ListTables"))
		status, body := doSigned(t, ts, u, invokeReq("puburl"))
		wantDenied(t, status, body, lambdaDeny)
	})
}

// TestAuthzMatrixLambdaUnroutedOps checks Lambda operations this server does
// not route are never served by another handler under a Lambda signature.
func TestAuthzMatrixLambdaUnroutedOps(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newFunction(t, cloud, "fn1", nil)

	u := userWithPolicy(t, cloud, "s3all", allow("s3:*", "lambda:*"))

	for _, rq := range []sreq{
		{method: http.MethodGet, path: "/2016-08-19/account-settings", service: "lambda"},
		{path: "/2014-11-13/functions/fn1/invoke-async/", ctype: "application/json", body: `{}`, service: "lambda"},
		{path: "/2021-11-15/functions/fn1/response-streaming-invocations", ctype: "application/json", body: `{}`, service: "lambda"},
		{path: "/2020-04-22/code-signing-configs/", ctype: "application/json", body: `{}`, service: "lambda"},
		{method: http.MethodPut, path: "/2021-07-20/functions/fn1/runtime-management-config", ctype: "application/json",
			body: `{}`, service: "lambda"},
	} {
		status, body := doSigned(t, ts, u, rq)
		if status != http.StatusNotImplemented {
			t.Errorf("%s %s: status %d %s, want 501", rq.method, rq.path, status, body)
		}
	}

	for _, b := range []string{"2016-08-19", "2014-11-13", "2021-11-15", "2020-04-22", "2021-07-20"} {
		if bucketExists(t, cloud, b) {
			t.Errorf("an unrouted Lambda request created bucket %s", b)
		}
	}
}

// TestAuthzMatrixLambdaUnknownOp checks a request Lambda answers with an
// error is denied to a restricted caller and changes nothing for root.
func TestAuthzMatrixLambdaUnknownOp(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newFunction(t, cloud, "fn1", nil)

	restricted := userWithPolicy(t, cloud, "readonly", allow("lambda:GetFunction"))
	boot := userWithPolicy(t, cloud, "boot", "")

	for _, rq := range []sreq{
		{method: http.MethodPut, path: lambdaPath, service: "lambda"},
		{method: http.MethodGet, path: lambdaPath + "/fn1/invocations", service: "lambda"},
		{method: http.MethodPost, path: lambdaPath + "/fn1/other", service: "lambda"},
		{method: http.MethodPost, path: lambdaPath + "/fn1:prod/invocations?Qualifier=1", service: "lambda"},
	} {
		status, body := doSigned(t, ts, restricted, rq)
		wantDenied(t, status, body, "lambda:UnknownOperation")

		status, body = doSigned(t, ts, boot, rq)
		if status < http.StatusBadRequest || status == http.StatusForbidden {
			t.Errorf("%s %s as boot: status %d %s, want a 4xx error", rq.method, rq.path, status, body)
		}
	}

	if fns, _ := cloud.Lambda.ListFunctions(context.Background()); len(fns) != 1 {
		t.Fatalf("functions %v, want only fn1", fns)
	}
}
