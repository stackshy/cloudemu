package aws

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestAuthzMatrixLambdaQualifiedDeny checks a Deny on the unqualified
// function is not bypassed by naming a version, an alias or $LATEST in the
// FunctionName or the Qualifier of an operation that acts on the function
// itself: each is checked on the resource dispatch changes.
func TestAuthzMatrixLambdaQualifiedDeny(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newFunction(t, cloud, "f", nil)

	bob := userWithPolicy(t, cloud, "bob", policyDoc(stmt("Allow", "lambda:*", "*"), stmt("Deny", "lambda:*", fnARN+"f")))
	ctx := context.Background()
	versionsBefore, _ := cloud.Lambda.ListVersions(ctx, "f")

	refs := []struct{ name, query string }{
		{"f:prod", ""}, {"f:1", ""}, {"f:$LATEST", ""}, {url.PathEscape(fnARN + "f:prod"), ""},
		{"f", "Qualifier=prod"}, {"f", "Qualifier=1"}, {"f", "Qualifier=$LATEST"},
	}

	for _, ref := range refs {
		base := lambdaPath + "/" + ref.name
		q := ""

		if ref.query != "" {
			q = "?" + ref.query
		}

		for _, rq := range []sreq{
			{method: http.MethodPut, path: base + "/configuration" + q, ctype: "application/json", body: `{"Description":"changed"}`},
			{method: http.MethodPut, path: base + "/code" + q, ctype: "application/json", body: `{"ZipFile":"UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="}`},
			{method: http.MethodPost, path: base + "/versions" + q, ctype: "application/json", body: `{}`},
			{method: http.MethodGet, path: base + "/versions" + q},
			{method: http.MethodPost, path: base + "/aliases" + q, ctype: "application/json", body: `{"Name":"evil","FunctionVersion":"1"}`},
			{method: http.MethodGet, path: base + "/aliases" + q},
			{method: http.MethodGet, path: base + "/aliases/prod" + q},
			{method: http.MethodPut, path: base + "/aliases/prod" + q, ctype: "application/json", body: `{"FunctionVersion":"$LATEST"}`},
			{method: http.MethodDelete, path: base + "/aliases/prod" + q},
		} {
			rq.service = "lambda"
			status, body := doSigned(t, ts, bob, rq)
			wantDenied(t, status, body, lambdaDeny)
		}
	}

	info, err := cloud.Lambda.GetFunction(ctx, "f")
	if err != nil || info.Description == "changed" {
		t.Fatalf("a denied update changed f: %+v %v", info, err)
	}

	if vers, _ := cloud.Lambda.ListVersions(ctx, "f"); len(vers) != len(versionsBefore) {
		t.Fatalf("versions %d, want %d", len(vers), len(versionsBefore))
	}

	if aliases, _ := cloud.Lambda.ListAliases(ctx, "f"); len(aliases) != 1 || aliases[0].FunctionVersion != "1" {
		t.Fatalf("aliases changed: %+v", aliases)
	}

	t.Run("Invoke", func(t *testing.T) {
		// $LATEST is the unqualified function, so the Deny applies. A version
		// or alias is a different resource: a policy on the unqualified ARN
		// does not match it (Lambda Developer Guide, "Referencing functions in
		// the Resource section of policies").
		for _, ref := range []string{"f:$LATEST", "f"} {
			status, body := doSigned(t, ts, bob, invokeReq(ref))
			wantDenied(t, status, body, lambdaDeny)
		}

		status, body := doSigned(t, ts, bob, invokeReq("f", "Qualifier=$LATEST"))
		wantDenied(t, status, body, lambdaDeny)

		for _, ref := range []string{"f:prod", "f:1"} {
			status, body := doSigned(t, ts, bob, invokeReq(ref))
			wantNotDenied(t, status, body)
		}
	})
}

// TestAuthzMatrixLambdaLatestPolicy checks a $LATEST-qualified AddPermission
// cannot write the unqualified function's policy: Lambda does not support
// policies on $LATEST.
func TestAuthzMatrixLambdaLatestPolicy(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newFunction(t, cloud, "f", nil)

	bob := userWithPolicy(t, cloud, "bob", policyDoc(stmt("Allow", "lambda:*", "*"), stmt("Deny", "lambda:*", fnARN+"f")))
	boot := userWithPolicy(t, cloud, "boot", "")
	nogrant := userWithPolicy(t, cloud, "nogrant", allow("dynamodb:ListTables"))

	grant := `{"StatementId":"open","Action":"lambda:InvokeFunction","Principal":"*"}`
	policyPath := lambdaPath + "/f/policy?Qualifier=$LATEST"

	status, body := doSigned(t, ts, bob, sreq{path: policyPath, ctype: "application/json", body: grant, service: "lambda"})
	wantDenied(t, status, body, lambdaDeny)

	status, body = doSigned(t, ts, bob, sreq{path: lambdaPath + "/f:$LATEST/policy", ctype: "application/json", body: grant, service: "lambda"})
	wantDenied(t, status, body, lambdaDeny)

	status, body = doSigned(t, ts, boot, sreq{path: policyPath, ctype: "application/json", body: grant, service: "lambda"})
	if status != http.StatusBadRequest {
		t.Fatalf("AddPermission on $LATEST: %d %s, want 400", status, body)
	}

	if _, stmts, err := cloud.Lambda.PolicyStatements(context.Background(), "f", ""); err != nil || len(stmts) != 0 {
		t.Fatalf("policy of f = %v %v, want empty", stmts, err)
	}

	if err := cloud.Lambda.AddPermission(context.Background(), "f", "$LATEST", sdrv.PermissionStatement{
		StatementID: "x", Action: "lambda:InvokeFunction", Principal: "*",
	}); err == nil {
		t.Fatal("the driver accepted a $LATEST policy")
	}

	status, body = doSigned(t, ts, nogrant, invokeReq("f"))
	wantDenied(t, status, body, lambdaDeny)
}

// TestAuthzMatrixLambdaForeignARN checks a FunctionName ARN of another
// account or region never acts on the local function of the same name.
func TestAuthzMatrixLambdaForeignARN(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newFunction(t, cloud, "f", nil)

	boot := userWithPolicy(t, cloud, "boot", "")

	for _, rq := range []sreq{
		invokeReq("arn:aws:lambda:us-east-1:999999999999:function:f"),
		invokeReq("arn:aws:lambda:eu-west-1:123456789012:function:f"),
		{method: http.MethodDelete, path: lambdaPath + "/999999999999:function:f", service: "lambda"},
		{path: "/2017-03-31/tags/arn:aws:lambda:us-east-1:999999999999:function:f", ctype: "application/json",
			body: `{"Tags":{"a":"b"}}`, service: "lambda"},
	} {
		status, body := doSigned(t, ts, boot, rq)
		if status != http.StatusNotFound {
			t.Errorf("%s %s: %d %s, want 404", rq.method, rq.path, status, body)
		}
	}

	if !functionExists(cloud, "f") {
		t.Fatal("a foreign ARN deleted the local function")
	}

	if tags, _ := cloud.Lambda.ListFunctionTags(context.Background(), "f"); tags["a"] != "" {
		t.Fatalf("a foreign ARN tagged the local function: %v", tags)
	}

	status, body := doSigned(t, ts, boot, sreq{path: lambdaPath, ctype: "application/json", service: "lambda",
		body: `{"FunctionName":"g:prod","Runtime":"python3.12","Role":"arn:aws:iam::123456789012:role/r","Handler":"h",` +
			`"Code":{"ZipFile":"UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="}}`})
	if status != http.StatusBadRequest {
		t.Fatalf("CreateFunction g:prod: %d %s, want 400", status, body)
	}

	status, body = doSigned(t, ts, boot, sreq{path: lambdaPath, ctype: "application/json", service: "lambda",
		body: `{"FunctionName":"` + fnARN + `g","Runtime":"python3.12","Role":"arn:aws:iam::123456789012:role/r","Handler":"h",` +
			`"Code":{"ZipFile":"UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="}}`})
	if status != http.StatusCreated || !functionExists(cloud, "g") {
		t.Fatalf("CreateFunction by local ARN: %d %s, want 201 creating g", status, body)
	}
}

// TestAuthzMatrixLambdaRecreatedAliasGrant checks a resource-policy grant on
// an alias does not outlive the alias: after DeleteAlias and a CreateAlias of
// the same name, the principal the old alias named can no longer invoke it.
func TestAuthzMatrixLambdaRecreatedAliasGrant(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newFunction(t, cloud, "f", nil)

	boot := userWithPolicy(t, cloud, "boot", "")
	named := userWithPolicy(t, cloud, "named", allow("dynamodb:ListTables"))

	if err := cloud.Lambda.AddPermission(context.Background(), "f", "prod", sdrv.PermissionStatement{
		StatementID: "named", Action: "lambda:InvokeFunction", Principal: "arn:aws:iam::" + acctID + ":user/named",
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}

	status, body := doSigned(t, ts, named, invokeReq("f:prod"))
	wantNotDenied(t, status, body)

	status, body = doSigned(t, ts, boot, sreq{method: http.MethodDelete, path: lambdaPath + "/f/aliases/prod", service: "lambda"})
	if status != http.StatusNoContent {
		t.Fatalf("DeleteAlias: %d %s, want 204", status, body)
	}

	status, body = doSigned(t, ts, boot, sreq{path: lambdaPath + "/f/aliases", ctype: "application/json",
		body: `{"Name":"prod","FunctionVersion":"1"}`, service: "lambda"})
	if status != http.StatusCreated {
		t.Fatalf("CreateAlias: %d %s, want 201", status, body)
	}

	status, body = doSigned(t, ts, named, invokeReq("f:prod"))
	wantDenied(t, status, body, "lambda:InvokeFunction on resource: "+fnARN+"f:prod")
}
