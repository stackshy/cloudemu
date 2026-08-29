package lambda

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
)

// TestInvokeSyncReturnsOutput verifies InvokeSync runs the handler and returns
// its output payload with no functionError.
func TestInvokeSyncReturnsOutput(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	m.RegisterHandler("my-func", func(_ context.Context, payload []byte) ([]byte, error) {
		return append([]byte("echo:"), payload...), nil
	})

	out, funcErr, err := m.InvokeSync(ctx, "arn:aws:lambda:us-east-1:000000000000:function:my-func", []byte("hi"))
	if err != nil || funcErr != "" {
		t.Fatalf("InvokeSync err=%v funcErr=%q", err, funcErr)
	}

	if string(out) != "echo:hi" {
		t.Fatalf("InvokeSync output = %q, want echo:hi", out)
	}
}

// TestInvokeSyncFunctionError surfaces a raising handler as a non-empty
// functionError (which the Step Functions Task maps to States.TaskFailed).
func TestInvokeSyncFunctionError(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	m.RegisterHandler("my-func", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, context.Canceled
	})

	_, funcErr, err := m.InvokeSync(ctx, "my-func", []byte("{}"))
	if err != nil {
		t.Fatalf("InvokeSync err = %v, want nil (handler failure is a functionError)", err)
	}

	if funcErr == "" {
		t.Fatal("InvokeSync functionError is empty, want the handler error surfaced")
	}
}

// TestInvokeSyncRecursionGuardBoundary asserts InvokeSync at MaxDepth drops the
// invocation and reports a bounded functionError instead of invoking.
func TestInvokeSyncRecursionGuardBoundary(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	ran := false
	m.RegisterHandler("my-func", func(_ context.Context, _ []byte) ([]byte, error) {
		ran = true
		return []byte("{}"), nil
	})

	atMax := recursionguard.WithDepth(ctx, recursionguard.MaxDepth)

	_, funcErr, err := m.InvokeSync(atMax, "my-func", []byte("{}"))
	if err != nil {
		t.Fatalf("InvokeSync at MaxDepth err = %v", err)
	}

	if funcErr == "" {
		t.Fatal("InvokeSync at MaxDepth returned no functionError, want the recursion-limit error")
	}

	if ran {
		t.Fatal("handler ran at MaxDepth, want the invocation dropped")
	}
}

// TestInvokeSyncSelfRecursionTerminates proves a handler that re-invokes itself
// through InvokeSync terminates at recursionguard.MaxDepth (never a stack
// overflow), because InvokeSync increments the ctx depth on each hop.
func TestInvokeSyncSelfRecursionTerminates(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	maxSeen := 0
	m.RegisterHandler("my-func", func(hctx context.Context, _ []byte) ([]byte, error) {
		if d := recursionguard.Depth(hctx); d > maxSeen {
			maxSeen = d
		}
		// Re-enter through the same sync seam; the guard bounds the chain.
		_, _, err := m.InvokeSync(hctx, "my-func", []byte("{}"))
		return []byte("{}"), err
	})

	if _, _, err := m.InvokeSync(ctx, "my-func", []byte("{}")); err != nil {
		t.Fatalf("InvokeSync self-recursion err = %v", err)
	}

	if maxSeen < recursionguard.MaxDepth {
		t.Fatalf("max handler depth = %d, want to reach MaxDepth %d", maxSeen, recursionguard.MaxDepth)
	}
}
