package lambda

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	awslambda "github.com/stackshy/cloudemu/v2/providers/aws/lambda"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

const (
	testFn    = "arn:aws:lambda:us-east-1:123456789012:function:"
	testLayer = "arn:aws:lambda:us-east-1:123456789012:layer:"
)

func testHandler(t *testing.T) *Handler {
	t.Helper()

	m := awslambda.New(config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012")))
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, sdrv.FunctionConfig{
		Name: "f", Runtime: "python3.12", Handler: "h", Tags: map[string]string{"env": "dev"},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	return New(m)
}

func testScope() awsauthz.Scope {
	return awsauthz.Scope{AccountID: "123456789012", Region: "us-east-1", Partition: "aws"}
}

// TestIAMChecks pins the action and resource of every operation.
func TestIAMChecks(t *testing.T) {
	h := testHandler(t)

	cases := []struct {
		method, target, body string
		want                 []string // "action resource"
	}{
		{"GET", "/2015-03-31/functions", "", []string{"lambda:ListFunctions *"}},
		{"POST", "/2015-03-31/functions", `{"FunctionName":"n","Tags":{"a":"b"},"Layers":["` + testLayer + `l:3"]}`,
			[]string{"lambda:CreateFunction " + testFn + "n", "lambda:TagResource " + testFn + "n", "lambda:GetLayerVersion " + testLayer + "l:3"}},
		{"GET", "/2015-03-31/functions/f", "", []string{"lambda:GetFunction " + testFn + "f"}},
		{"GET", "/2015-03-31/functions/f:prod", "", []string{"lambda:GetFunction " + testFn + "f:prod"}},
		{"DELETE", "/2015-03-31/functions/f?Qualifier=2", "", []string{"lambda:DeleteFunction " + testFn + "f:2"}},
		{"POST", "/2015-03-31/functions/arn:aws:lambda:us-east-1:123456789012:function:f:prod/invocations", "",
			[]string{"lambda:InvokeFunction " + testFn + "f:prod"}},
		{"PUT", "/2015-03-31/functions/f/configuration", `{"Layers":["` + testLayer + `l:1"]}`,
			[]string{"lambda:UpdateFunctionConfiguration " + testFn + "f", "lambda:GetLayerVersion " + testLayer + "l:1"}},
		{"PUT", "/2015-03-31/functions/f/code", "{}", []string{"lambda:UpdateFunctionCode " + testFn + "f"}},
		{"PUT", "/2015-03-31/functions/f:prod/configuration", "{}", []string{"lambda:UpdateFunctionConfiguration " + testFn + "f"}},
		{"PUT", "/2015-03-31/functions/f/code?Qualifier=1", "{}", []string{"lambda:UpdateFunctionCode " + testFn + "f"}},
		{"POST", "/2015-03-31/functions/f:prod/versions", "{}", []string{"lambda:PublishVersion " + testFn + "f"}},
		{"POST", "/2015-03-31/functions/f:prod/aliases", "{}", []string{"lambda:CreateAlias " + testFn + "f"}},
		{"DELETE", "/2015-03-31/functions/f:1/aliases/prod", "", []string{"lambda:DeleteAlias " + testFn + "f"}},
		{"GET", "/2015-03-31/functions/f:$LATEST", "", []string{"lambda:GetFunction " + testFn + "f"}},
		{"POST", "/2015-03-31/functions/f/invocations?Qualifier=$LATEST", "", []string{"lambda:InvokeFunction " + testFn + "f"}},
		{"POST", "/2017-03-31/tags/" + testFn + "f:prod", `{"Tags":{"k":"v"}}`, []string{"lambda:TagResource " + testFn + "f"}},
		{"POST", "/2015-03-31/functions/f/versions", "{}", []string{"lambda:PublishVersion " + testFn + "f"}},
		{"DELETE", "/2015-03-31/functions/f/aliases/prod", "", []string{"lambda:DeleteAlias " + testFn + "f"}},
		{"POST", "/2015-03-31/functions/f/policy", `{"Principal":"s3.amazonaws.com"}`, []string{"lambda:AddPermission " + testFn + "f"}},
		{"DELETE", "/2015-03-31/functions/f/policy/s1?Qualifier=prod", "", []string{"lambda:RemovePermission " + testFn + "f:prod"}},
		{"POST", "/2017-03-31/tags/" + testFn + "f", `{"Tags":{"k":"v"}}`, []string{"lambda:TagResource " + testFn + "f"}},
		{"GET", "/2017-03-31/tags/arn:aws:lambda:us-east-1:123456789012:function:f", "", []string{"lambda:ListTags " + testFn + "f"}},
		{"DELETE", "/2017-03-31/tags/not-an-arn?tagKeys=k", "", []string{"lambda:UntagResource "}},
		{"POST", "/2015-03-31/event-source-mappings/", `{"FunctionName":"f"}`, []string{"lambda:CreateEventSourceMapping *"}},
		{"GET", "/2015-03-31/event-source-mappings/u1", "",
			[]string{"lambda:GetEventSourceMapping arn:aws:lambda:us-east-1:123456789012:event-source-mapping:u1"}},
		{"GET", "/2018-10-31/layers", "", []string{"lambda:ListLayers *"}},
		{"GET", "/2018-10-31/layers?find=LayerVersion&Arn=arn:aws:lambda:eu-west-1:999999999999:layer:l:4", "",
			[]string{"lambda:GetLayerVersion " + testLayer + "l:4"}},
		{"POST", "/2018-10-31/layers/l/versions", "{}", []string{"lambda:PublishLayerVersion " + testLayer + "l"}},
		{"GET", "/2018-10-31/layers/l/versions", "", []string{"lambda:ListLayerVersions *"}},
		{"DELETE", "/2018-10-31/layers/l/versions/2", "", []string{"lambda:DeleteLayerVersion " + testLayer + "l:2"}},
		{"DELETE", "/2018-10-31/layers/l/versions/2/policy/s", "", []string{"lambda:RemoveLayerVersionPermission " + testLayer + "l:2"}},
		{"POST", "/2021-10-31/functions/f/url?Qualifier=prod", `{"AuthType":"NONE"}`, []string{"lambda:CreateFunctionUrlConfig " + testFn + "f:prod"}},
		{"GET", "/2021-10-31/functions/f/urls", "", []string{"lambda:ListFunctionUrlConfigs " + testFn + "f"}},
		{"PUT", "/2019-09-25/functions/f/event-invoke-config", "{}", []string{"lambda:PutFunctionEventInvokeConfig " + testFn + "f"}},
		{"PUT", "/2019-09-30/functions/f/provisioned-concurrency?Qualifier=1", "{}",
			[]string{"lambda:PutProvisionedConcurrencyConfig " + testFn + "f:1"}},
		{"GET", "/2020-06-30/functions/f/code-signing-config", "", []string{"lambda:GetFunctionCodeSigningConfig " + testFn + "f"}},
		{"PUT", "/2017-10-31/functions/f/concurrency", "{}", []string{"lambda:PutFunctionConcurrency " + testFn + "f"}},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))

		checks, ok := h.IAMChecks(r, testScope())
		if !ok {
			t.Errorf("%s %s: ok=false", tc.method, tc.target)
			continue
		}

		got := make([]string, 0, len(checks))
		for _, c := range checks {
			got = append(got, c.Action+" "+c.Resource)
		}

		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s %s: checks %v, want %v", tc.method, tc.target, got, tc.want)
		}
	}
}

// TestIAMChecksUnknown checks the requests the handler answers with an
// error are reported as unknown.
func TestIAMChecksUnknown(t *testing.T) {
	h := testHandler(t)

	for _, tc := range []struct{ method, target, body string }{
		{"PUT", "/2015-03-31/functions", ""},
		{"POST", "/2015-03-31/functions/f:a/invocations?Qualifier=b", ""},
		{"POST", "/2015-03-31/functions", "{not json"},
		{"GET", "/2018-10-31/layers/l/versions/x", ""},
		{"GET", "/2018-10-31/layers?find=LayerVersion&Arn=bad", ""},
		{"PUT", "/2020-06-30/functions/f/code-signing-config", ""},
		{"POST", "/2015-03-31/functions/arn:aws:lambda:eu-west-1:123456789012:function:f/invocations", ""},
		{"GET", "/2015-03-31/functions/999999999999:function:f", ""},
		{"POST", "/2017-03-31/tags/arn:aws:lambda:us-east-1:999999999999:function:f", `{"Tags":{"a":"b"}}`},
		{"POST", "/2015-03-31/functions", `{"FunctionName":"f:prod"}`},
		{"POST", "/2015-03-31/functions", `{"FunctionName":"arn:aws:lambda:us-east-1:999999999999:function:n"}`},
		{"GET", "/", ""},
	} {
		r := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
		r.Host = "x.lambda-url.us-east-1.on.aws"

		if tc.target != "/" {
			r.Host = "localhost"
		}

		if _, ok := h.IAMChecks(r, testScope()); ok {
			t.Errorf("%s %s: ok=true, want unknown", tc.method, tc.target)
		}
	}
}

// TestIAMCheckContext checks the condition keys of the operations that have
// them.
func TestIAMCheckContext(t *testing.T) {
	h := testHandler(t)

	cases := []struct {
		method, target, body string
		key, want            string
	}{
		{"GET", "/2015-03-31/functions/f", "", "aws:ResourceTag/env", "dev"},
		{"POST", "/2015-03-31/functions", `{"FunctionName":"n","Tags":{"team":"a"}}`, "aws:RequestTag/team", "a"},
		{"POST", "/2015-03-31/functions", `{"FunctionName":"n","VpcConfig":{"SubnetIds":["s1"]}}`, "lambda:SubnetIds", "s1"},
		{"POST", "/2015-03-31/functions/f/policy", `{"Principal":"s3.amazonaws.com","FunctionUrlAuthType":"NONE"}`,
			"lambda:Principal", "s3.amazonaws.com"},
		{"POST", "/2021-10-31/functions/f/url", `{"AuthType":"AWS_IAM"}`, "lambda:FunctionUrlAuthType", "AWS_IAM"},
		{"POST", "/2015-03-31/event-source-mappings", `{"FunctionName":"f:prod"}`, "lambda:FunctionArn", testFn + "f:prod"},
		{"DELETE", "/2017-03-31/tags/" + testFn + "f?tagKeys=a&tagKeys=b", "", "aws:TagKeys", "a\x00b"},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))

		_, cond, ok := h.IAMChecksWithContext(r, testScope())
		if !ok {
			t.Errorf("%s %s: ok=false", tc.method, tc.target)
			continue
		}

		want := strings.ReplaceAll(tc.want, "\x00", iamdriver.ConditionValueSeparator)
		if cond[tc.key] != want {
			t.Errorf("%s %s: %s = %q, want %q", tc.method, tc.target, tc.key, cond[tc.key], want)
		}
	}
}

// TestIAMChecksKeepBody checks reading the body for the checks leaves it for
// dispatch.
func TestIAMChecksKeepBody(t *testing.T) {
	h := testHandler(t)
	body := `{"FunctionName":"kept","Runtime":"python3.12","Handler":"h","Role":"arn:aws:iam::123456789012:role/r",` +
		`"Code":{"ZipFile":"UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="}}`
	r := httptest.NewRequest(http.MethodPost, "/2015-03-31/functions", strings.NewReader(body))

	if _, ok := h.IAMChecks(r, testScope()); !ok {
		t.Fatal("IAMChecks: ok=false")
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("CreateFunction after IAMChecks: %d %s", w.Code, w.Body)
	}
}
