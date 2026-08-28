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

// taskHandler runs a Task state. It resolves the Lambda call from the Resource
// ARN and the InputPath->Parameters-shaped payload, invokes the function through
// the recursion-guarded seam under Retry, and on success runs the result through
// ResultSelector->ResultPath->OutputPath. A failure that no Catcher handles
// propagates to ExecutionFailed; a caught failure transitions to the Catcher's
// Next with the {Error,Cause} error output merged at its ResultPath.
func taskHandler(ctx context.Context, it *interp, st *State, raw any) (out any, next string, terminal bool, err error) {
	it.enterState(st, raw)

	funcRef, payload, perr := it.prepareTask(st, raw)
	if perr != nil {
		return nil, "", false, perr
	}

	result, se := it.invokeTaskWithRetry(ctx, st, funcRef, payload)
	if se != nil {
		return handleTaskError(st, raw, se)
	}

	return it.finishTask(st, raw, result)
}

// prepareTask runs the input pipeline (InputPath->Parameters) and resolves the
// target function reference and the payload delivered to it.
func (it *interp) prepareTask(st *State, raw any) (funcRef string, payload []byte, err error) {
	filtered, ferr := it.applyInputPath(st, raw)
	if ferr != nil {
		return "", nil, ferr
	}

	params, perr := it.applyParameters(st, filtered)
	if perr != nil {
		return "", nil, perr
	}

	return resolveLambdaCall(st, params)
}

// finishTask runs the result pipeline on a successful task result and emits the
// TaskStateExited event.
func (it *interp) finishTask(st *State, raw, result any) (out any, next string, terminal bool, err error) {
	selected, err := it.applyResultSelector(st, result)
	if err != nil {
		return nil, "", false, err
	}

	merged, err := it.applyResultPath(st, raw, selected)
	if err != nil {
		return nil, "", false, err
	}

	out, err = it.applyOutputPath(st, merged)
	if err != nil {
		return nil, "", false, err
	}

	it.exitState(st, out)

	return out, st.Next, st.End, nil
}

// handleTaskError routes a task failure to a matching Catcher (transitioning to
// its Next), or propagates it to ExecutionFailed when no Catcher matches.
func handleTaskError(st *State, raw any, se *stateError) (out any, next string, terminal bool, err error) {
	if caughtOut, catchNext, ok := tryCatch(st, raw, se); ok {
		return caughtOut, catchNext, false, nil
	}

	return nil, "", false, se
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
