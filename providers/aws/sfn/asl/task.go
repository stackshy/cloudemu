package asl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// lambdaInvokeResource is the optimized Task integration ARN for a synchronous
// Lambda invoke; its Parameters carry FunctionName + Payload.
const lambdaInvokeResource = "arn:aws:states:::lambda:invoke"

// lambdaOKStatus is the StatusCode a successful lambda:invoke response envelope
// reports.
const lambdaOKStatus = 200

// taskHandler runs a Task state. The whole state — its InputPath/Parameters
// input pipeline, the recursion-guarded Lambda invoke, and its
// ResultSelector/ResultPath/OutputPath result pipeline — runs under Retry, so a
// matching Retrier re-runs the state (re-invoking the function) exactly as real
// AWS does. Any failure the Retriers do not recover, INCLUDING a state-internal
// I/O-pipeline error (States.ParameterPathFailure / States.ResultPathMatchFailure
// / an unsupported Resource), is routed through the state's Catch: a matching
// Catcher transitions to its Next with the {Error,Cause} error output merged at
// its ResultPath; otherwise it propagates to ExecutionFailed.
func taskHandler(ctx context.Context, it *interp, st *State, raw any) (out any, next string, terminal bool, err error) {
	it.enterState(st, raw)

	out, se := it.runWithRetry(st, func() (any, *stateError) {
		return it.runTaskOnce(ctx, st, raw)
	})
	if se != nil {
		return catchOrFail(st, raw, se)
	}

	it.exitState(st, out)

	return out, st.Next, st.End, nil
}

// runTaskOnce is one Task attempt: it runs the input pipeline, resolves and
// invokes the Lambda through the recursion-guarded seam, then runs the result
// pipeline. Every failure (I/O or invoke) is a *stateError so Retry/Catch see it.
func (it *interp) runTaskOnce(ctx context.Context, st *State, raw any) (any, *stateError) {
	input, se := it.stateInput(st, raw)
	if se != nil {
		return nil, se
	}

	funcRef, payload, err := resolveLambdaCall(st, input)
	if err != nil {
		return nil, asStateError(err)
	}

	result, se := it.invokeLambdaTask(ctx, st, funcRef, payload)
	if se != nil {
		return nil, se
	}

	return it.resultPipeline(st, raw, result)
}

// invokeLambdaTask performs one Lambda invocation attempt, emitting the
// LambdaFunctionScheduled/Started and Succeeded/Failed sub-events. With no seam
// wired (library-only construction) it echoes the payload as the result.
func (it *interp) invokeLambdaTask(ctx context.Context, st *State, funcRef string, payload []byte) (any, *stateError) {
	it.emit(historyLambdaScheduled(st, payload))
	it.emit(&driver.HistoryEvent{Type: "LambdaFunctionStarted"})

	if it.invokeLambda == nil {
		result := wrapTaskResult(st, payload)
		it.emit(historyLambdaSucceeded(result))

		return result, nil
	}

	outBytes, funcErr, err := it.invokeLambda(ctx, funcRef, payload)
	if se := taskFailure(err, funcErr); se != nil {
		it.emit(historyLambdaFailed(se))

		return nil, se
	}

	result := wrapTaskResult(st, outBytes)
	it.emit(historyLambdaSucceeded(result))

	return result, nil
}

// taskFailure maps a transport error or a Lambda functionError to States.TaskFailed
// (feeding Retry/Catch), or returns nil when the invocation succeeded.
func taskFailure(err error, funcErr string) *stateError {
	switch {
	case err != nil:
		return &stateError{Code: "States.TaskFailed", Cause: err.Error()}
	case funcErr != "":
		return &stateError{Code: "States.TaskFailed", Cause: funcErr}
	default:
		return nil
	}
}

// resolveLambdaCall parses the Task Resource, returning the target function
// reference and the payload to deliver. It supports the optimized
// arn:aws:states:::lambda:invoke integration (payload from Parameters.Payload)
// and the bare Lambda-function-ARN form (payload is the effective input). Any
// other Resource fails loudly, feeding Retry/Catch rather than panicking.
func resolveLambdaCall(st *State, params any) (funcRef string, payload []byte, err error) {
	switch {
	case st.Resource == lambdaInvokeResource:
		return optimizedLambdaCall(params)
	case isLambdaFunctionARN(st.Resource):
		return st.Resource, valueToBytes(params), nil
	default:
		return "", nil, &stateError{Code: "States.Runtime",
			Cause: fmt.Sprintf("unsupported Task Resource %q (only Lambda is supported)", st.Resource)}
	}
}

// optimizedLambdaCall extracts FunctionName and Payload from the Parameters of
// an arn:aws:states:::lambda:invoke Task.
func optimizedLambdaCall(params any) (funcRef string, payload []byte, err error) {
	m, ok := params.(map[string]any)
	if !ok {
		return "", nil, &stateError{Code: "States.Runtime",
			Cause: "lambda:invoke Task requires Parameters with FunctionName"}
	}

	fn, _ := m["FunctionName"].(string)
	if fn == "" {
		return "", nil, &stateError{Code: "States.Runtime",
			Cause: "lambda:invoke Task Parameters is missing FunctionName"}
	}

	return fn, valueToBytes(m["Payload"]), nil
}

// wrapTaskResult shapes the raw Lambda output into the state's result: the
// optimized lambda:invoke integration wraps it in the Lambda invoke response
// envelope, while the bare-ARN form yields the function output directly.
func wrapTaskResult(st *State, outBytes []byte) any {
	out := bytesToValue(outBytes)
	if st.Resource == lambdaInvokeResource {
		return map[string]any{"ExecutedVersion": "$LATEST", "Payload": out, "StatusCode": float64(lambdaOKStatus)}
	}

	return out
}

// isLambdaFunctionARN reports whether res is a bare Lambda function ARN.
func isLambdaFunctionARN(res string) bool {
	return strings.HasPrefix(res, "arn:aws:lambda:") && strings.Contains(res, ":function:")
}

// valueToBytes marshals a payload value to JSON, defaulting a nil/failed value
// to an empty object.
func valueToBytes(v any) []byte {
	if v == nil {
		return []byte(emptyObject)
	}

	b, err := json.Marshal(v)
	if err != nil {
		return []byte(emptyObject)
	}

	return b
}

// bytesToValue parses a JSON payload into a Go value, defaulting empty/invalid
// bytes to an empty object.
func bytesToValue(b []byte) any {
	if len(b) == 0 {
		return map[string]any{}
	}

	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return map[string]any{}
	}

	return v
}
