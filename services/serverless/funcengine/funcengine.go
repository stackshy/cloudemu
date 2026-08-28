// Package funcengine holds the shared wiring that lets any serverless provider
// (AWS Lambda, Azure Functions, GCP Cloud Functions) back its functions with an
// opt-in config.FunctionEngine that runs real code. Keeping the Deploy/Invoke/
// Remove glue here means the three providers can't drift in how they translate
// between the portable driver types and the engine contract.
package funcengine

import (
	"context"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// okStatusCode is the HTTP status a successful (including handler-error)
// invocation reports; real Lambda returns 200 even when the handler raises and
// signals the failure through the function-error field instead.
const okStatusCode = 200

// Deploy deploys cfg's code to the engine and reports whether the function is
// now engine-backed — true only when an engine is configured and code was
// uploaded. Providers persist the returned flag and consult it on Invoke so a
// function created before an engine was wired keeps the stub behavior.
func Deploy(ctx context.Context, engine config.FunctionEngine, cfg *driver.FunctionConfig) (bool, error) {
	if engine == nil || len(cfg.Code) == 0 {
		return false, nil
	}

	err := engine.Deploy(ctx, config.FunctionDeployment{
		Name:      cfg.Name,
		Runtime:   cfg.Runtime,
		Handler:   cfg.Handler,
		Code:      cfg.Code,
		Env:       cfg.Environment,
		Timeout:   cfg.Timeout,
		Framework: cfg.Framework,
	})
	if err != nil {
		return false, err
	}

	return true, nil
}

// Invoke runs an engine-backed function and maps the engine result onto the
// portable InvokeOutput. A handler that raised is surfaced via Error (mirroring
// X-Amz-Function-Error); a returned Go error means the engine itself failed.
func Invoke(ctx context.Context, engine config.FunctionEngine, name string, event []byte) (*driver.InvokeOutput, error) {
	res, err := engine.Invoke(ctx, name, event)
	if err != nil {
		return nil, err
	}

	out := &driver.InvokeOutput{StatusCode: okStatusCode, Payload: res.Payload, Logs: res.Logs}
	if res.FunctionError != "" {
		out.Error = res.FunctionError
	}

	return out, nil
}

// Remove tears down the engine deployment backing name. It is a no-op when no
// engine is configured.
func Remove(ctx context.Context, engine config.FunctionEngine, name string) error {
	if engine == nil {
		return nil
	}

	return engine.Remove(ctx, name)
}
