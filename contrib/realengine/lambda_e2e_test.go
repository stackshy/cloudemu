package realengine_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// pythonHandler doubles a number, echoes an env var, and passes through a list —
// enough that a passing test can only mean the real Python actually ran.
const pythonHandler = `import os

def lambda_handler(event, context):
    return {
        "doubled": event["n"] * 2,
        "greeting": os.environ.get("GREETING", "hi"),
        "items": event.get("items", []),
    }
`

const nodeHandler = `exports.handler = async (event) => {
  return {
    doubled: event.n * 2,
    greeting: process.env.GREETING || "hi",
    items: event.items || [],
  };
};
`

// TestLambdaPythonE2E runs the real-user AWS Lambda flow: create a function from
// a real Python zip with the AWS SDK, invoke it, and confirm the response is the
// output of the uploaded handler actually executing — all against CloudEmu
// backed by a real subprocess runtime (no Docker, no cloud account).
func TestLambdaPythonE2E(t *testing.T) {
	requireBinary(t, "python3")

	client, cleanup := lambdaClient(t)
	defer cleanup()

	zipCode := zipFile(t, "lambda_function.py", pythonHandler)

	createFn(t, client, "py-fn", "python3.12", "lambda_function.lambda_handler", zipCode)

	out := invoke(t, client, "py-fn", `{"n": 21, "items": ["a", "b"]}`)
	assertHandlerOutput(t, out, 42, "hello", []string{"a", "b"})

	deleteAndAssertGone(t, client, "py-fn")
}

// TestLambdaNodeE2E is the same flow for the Node runtime.
func TestLambdaNodeE2E(t *testing.T) {
	requireBinary(t, "node")

	client, cleanup := lambdaClient(t)
	defer cleanup()

	zipCode := zipFile(t, "index.js", nodeHandler)
	createFn(t, client, "node-fn", "nodejs20.x", "index.handler", zipCode)

	out := invoke(t, client, "node-fn", `{"n": 4, "items": ["x"]}`)
	assertHandlerOutput(t, out, 8, "hello", []string{"x"})

	deleteAndAssertGone(t, client, "node-fn")
}

// TestLambdaHandlerErrorE2E confirms a handler that raises surfaces through the
// SDK as a function error (X-Amz-Function-Error), not a transport failure.
func TestLambdaHandlerErrorE2E(t *testing.T) {
	requireBinary(t, "python3")

	client, cleanup := lambdaClient(t)
	defer cleanup()

	zipCode := zipFile(t, "lambda_function.py",
		"def lambda_handler(event, context):\n    raise ValueError('boom')\n")
	createFn(t, client, "err-fn", "python3.12", "lambda_function.lambda_handler", zipCode)

	res, err := client.Invoke(context.Background(), &lambda.InvokeInput{
		FunctionName: aws.String("err-fn"),
		Payload:      []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if res.FunctionError == nil || *res.FunctionError == "" {
		t.Fatalf("expected FunctionError to be set, got payload %s", res.Payload)
	}

	if !bytes.Contains(res.Payload, []byte("boom")) {
		t.Fatalf("expected error payload to mention 'boom', got %s", res.Payload)
	}
}

func requireBinary(t *testing.T, name string) {
	t.Helper()

	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not installed; skipping real-execution test", name)
	}
}

func lambdaClient(t *testing.T) (*lambda.Client, func()) {
	t.Helper()

	eng := realengine.NewSubprocess()

	cloud := cloudemu.NewAWS(config.WithFunctionEngine(eng))
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := lambda.NewFromConfig(cfg, func(o *lambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return client, func() {
		ts.Close()
		_ = eng.Close()
	}
}

func createFn(t *testing.T, client *lambda.Client, name, runtimeID, handler string, code []byte) {
	t.Helper()

	_, err := client.CreateFunction(context.Background(), &lambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lambdatypes.Runtime(runtimeID),
		Role:         aws.String("arn:aws:iam::123456789012:role/lambda-role"),
		Handler:      aws.String(handler),
		Code:         &lambdatypes.FunctionCode{ZipFile: code},
		Environment:  &lambdatypes.Environment{Variables: map[string]string{"GREETING": "hello"}},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
}

func invoke(t *testing.T, client *lambda.Client, name, payload string) map[string]any {
	t.Helper()

	res, err := client.Invoke(context.Background(), &lambda.InvokeInput{
		FunctionName: aws.String(name),
		Payload:      []byte(payload),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if res.FunctionError != nil {
		t.Fatalf("unexpected FunctionError %q, payload %s", *res.FunctionError, res.Payload)
	}

	var out map[string]any
	if err := json.Unmarshal(res.Payload, &out); err != nil {
		t.Fatalf("decode payload %s: %v", res.Payload, err)
	}

	return out
}

func assertHandlerOutput(t *testing.T, out map[string]any, wantDoubled float64, wantGreeting string, wantItems []string) {
	t.Helper()

	if got, ok := out["doubled"].(float64); !ok || got != wantDoubled {
		t.Fatalf("doubled: got %v, want %v", out["doubled"], wantDoubled)
	}

	if got, _ := out["greeting"].(string); got != wantGreeting {
		t.Fatalf("greeting: got %q, want %q", out["greeting"], wantGreeting)
	}

	items, _ := out["items"].([]any)
	if len(items) != len(wantItems) {
		t.Fatalf("items: got %v, want %v", out["items"], wantItems)
	}

	for i, want := range wantItems {
		if got, _ := items[i].(string); got != want {
			t.Fatalf("items[%d]: got %q, want %q", i, items[i], want)
		}
	}
}

func deleteAndAssertGone(t *testing.T, client *lambda.Client, name string) {
	t.Helper()

	if _, err := client.DeleteFunction(context.Background(), &lambda.DeleteFunctionInput{
		FunctionName: aws.String(name),
	}); err != nil {
		t.Fatalf("DeleteFunction: %v", err)
	}

	_, err := client.Invoke(context.Background(), &lambda.InvokeInput{
		FunctionName: aws.String(name),
		Payload:      []byte(`{}`),
	})
	if err == nil {
		t.Fatal("expected Invoke of the deleted function to fail")
	}
}

func zipFile(t *testing.T, name, content string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}

	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("zip write: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	return buf.Bytes()
}
