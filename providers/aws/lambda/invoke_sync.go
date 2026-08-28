package lambda

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// InvokeSync synchronously invokes the function identified by its ARN (or bare
// name) and returns the output payload plus a functionError set when the handler
// ran but raised (StatusCode 500 / a non-empty out.Error — the same X-Amz-
// Function-Error semantics Invoke reports). It backs the Step Functions
// Task->Lambda seam (sfn.LambdaSyncInvoker).
//
// Like InvokeExternal, it is a synchronous Lambda-delivery entry point that
// bypasses the Invoke wire path's choke points, so it MUST carry the same
// recursive-loop guard: a Task whose Lambda starts an execution that Tasks back
// into Lambda re-enters here on the same goroutine (invoke -> handler ->
// StartExecution -> Task -> InvokeSync -> ...). ctx carries the re-entrant
// delivery depth (see internal/recursionguard); once it reaches
// recursionguard.MaxDepth — matching AWS Lambda's own recursive-loop detection
// (~16 invocations within one chain of requests) — the invocation is dropped
// and reported as a bounded functionError (which the Task maps to
// States.TaskFailed) instead of recursing the process into an unrecoverable
// stack overflow.
func (m *Mock) InvokeSync(
	ctx context.Context, functionARN string, payload []byte,
) (output []byte, functionError string, err error) {
	name := functionNameFromARN(functionARN)
	if _, ok := m.funcs.Get(name); !ok {
		return nil, "", cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	depth := recursionguard.Depth(ctx)
	if depth >= recursionguard.MaxDepth {
		return nil, fmt.Sprintf(
			"Lambda recursive invocation limit (%d) reached for function %s", recursionguard.MaxDepth, name), nil
	}

	ctx = recursionguard.WithDepth(ctx, depth+1)

	out, invErr := m.Invoke(ctx, driver.InvokeInput{
		FunctionName: name, Payload: payload, InvokeType: "RequestResponse",
	})
	if invErr != nil {
		return nil, "", invErr
	}

	return out.Payload, out.Error, nil
}
