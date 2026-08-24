package lambda

import (
	"context"
	"testing"
)

// TestInvokeExternalByARN verifies InvokeExternal resolves a function by ARN
// (including a version qualifier) and invokes it with the event payload, and
// that an unknown ARN is a no-op (backing S3 -> Lambda notifications).
func TestInvokeExternalByARN(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	var got string
	m.RegisterHandler("my-func", func(_ context.Context, payload []byte) ([]byte, error) {
		got = string(payload)
		return []byte("ok"), nil
	})

	// Unknown ARN: no error, handler not invoked.
	if err := m.InvokeExternal(ctx, "arn:aws:lambda:us-east-1:0:function:absent", []byte("x")); err != nil {
		t.Fatalf("InvokeExternal unknown: %v", err)
	}

	if got != "" {
		t.Fatalf("handler ran for unknown ARN: %q", got)
	}

	arn := "arn:aws:lambda:us-east-1:000000000000:function:my-func:PROD"
	if err := m.InvokeExternal(ctx, arn, []byte(`{"event":"s3"}`)); err != nil {
		t.Fatalf("InvokeExternal: %v", err)
	}

	if got != `{"event":"s3"}` {
		t.Fatalf("handler payload = %q, want the S3 event", got)
	}
}

func TestFunctionNameFromARN(t *testing.T) {
	cases := map[string]string{
		"arn:aws:lambda:us-east-1:0:function:fn":       "fn",
		"arn:aws:lambda:us-east-1:0:function:fn:PROD":  "fn",
		"arn:aws:lambda:us-east-1:0:function:fn:$LATE": "fn",
		"fn":                                           "fn",
	}
	for in, want := range cases {
		if got := functionNameFromARN(in); got != want {
			t.Fatalf("functionNameFromARN(%q) = %q, want %q", in, got, want)
		}
	}
}
