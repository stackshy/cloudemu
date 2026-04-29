package chaos

import (
	"context"

	serverlessdriver "github.com/stackshy/cloudemu/serverless/driver"
)

// chaosServerless wraps a serverless driver. Hot-path: function CRUD + Invoke.
// Versions/aliases/layers/concurrency/event source mappings delegate through.
type chaosServerless struct {
	serverlessdriver.Serverless
	engine *Engine
}

// WrapServerless returns a serverless driver that consults engine on function
// lifecycle and invocation calls.
func WrapServerless(inner serverlessdriver.Serverless, engine *Engine) serverlessdriver.Serverless {
	return &chaosServerless{Serverless: inner, engine: engine}
}

//nolint:gocritic // cfg is a value type by interface contract
func (c *chaosServerless) CreateFunction(
	ctx context.Context, cfg serverlessdriver.FunctionConfig,
) (*serverlessdriver.FunctionInfo, error) {
	if err := applyChaos(ctx, c.engine, "serverless", "CreateFunction"); err != nil {
		return nil, err
	}

	return c.Serverless.CreateFunction(ctx, cfg)
}

func (c *chaosServerless) DeleteFunction(ctx context.Context, name string) error {
	if err := applyChaos(ctx, c.engine, "serverless", "DeleteFunction"); err != nil {
		return err
	}

	return c.Serverless.DeleteFunction(ctx, name)
}

func (c *chaosServerless) GetFunction(ctx context.Context, name string) (*serverlessdriver.FunctionInfo, error) {
	if err := applyChaos(ctx, c.engine, "serverless", "GetFunction"); err != nil {
		return nil, err
	}

	return c.Serverless.GetFunction(ctx, name)
}

func (c *chaosServerless) ListFunctions(ctx context.Context) ([]serverlessdriver.FunctionInfo, error) {
	if err := applyChaos(ctx, c.engine, "serverless", "ListFunctions"); err != nil {
		return nil, err
	}

	return c.Serverless.ListFunctions(ctx)
}

//nolint:gocritic // cfg is a value type by interface contract
func (c *chaosServerless) UpdateFunction(
	ctx context.Context, name string, cfg serverlessdriver.FunctionConfig,
) (*serverlessdriver.FunctionInfo, error) {
	if err := applyChaos(ctx, c.engine, "serverless", "UpdateFunction"); err != nil {
		return nil, err
	}

	return c.Serverless.UpdateFunction(ctx, name, cfg)
}

func (c *chaosServerless) Invoke(
	ctx context.Context, input serverlessdriver.InvokeInput,
) (*serverlessdriver.InvokeOutput, error) {
	if err := applyChaos(ctx, c.engine, "serverless", "Invoke"); err != nil {
		return nil, err
	}

	return c.Serverless.Invoke(ctx, input)
}
