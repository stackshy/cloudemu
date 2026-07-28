package bedrock

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// StartAsyncInvoke starts an asynchronous model invocation. It completes
// synchronously: the invocation is recorded already in the Completed state so
// Get/List calls are deterministic.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) StartAsyncInvoke(_ context.Context, cfg driver.StartAsyncInvokeConfig) (*driver.AsyncInvoke, error) {
	switch {
	case cfg.ModelID == "":
		return nil, errors.New(errors.InvalidArgument, "modelId is required")
	case len(cfg.ModelInput) == 0:
		return nil, errors.New(errors.InvalidArgument, "modelInput is required")
	case cfg.Output.S3URI == "":
		return nil, errors.New(errors.InvalidArgument, "outputDataConfig.s3OutputDataConfig.s3Uri is required")
	}

	modelARN := m.resolveModelARN(cfg.ModelID)
	if modelARN == "" {
		return nil, errors.Newf(errors.InvalidArgument, "model %q not found", cfg.ModelID)
	}

	now := m.now()
	arn := idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "async-invoke/"+idgen.GenerateID(""))

	inv := &driver.AsyncInvoke{
		InvocationARN:      arn,
		ModelARN:           modelARN,
		ClientRequestToken: cfg.ClientRequestToken,
		Status:             driver.AsyncCompleted,
		Output:             cfg.Output,
		SubmitTime:         now,
		LastModifiedTime:   now,
		EndTime:            now,
	}
	m.asyncInvokes.Set(arn, inv)
	m.setTags(arn, m.tagsFromMap(cfg.Tags))

	result := *inv

	return &result, nil
}

// GetAsyncInvoke returns an async invocation by its invocation ARN.
func (m *Mock) GetAsyncInvoke(_ context.Context, invocationARN string) (*driver.AsyncInvoke, error) {
	inv, ok := m.asyncInvokes.Get(invocationARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "async invocation %q not found", invocationARN)
	}

	result := *inv

	return &result, nil
}

// ListAsyncInvokes lists all async invocations.
func (m *Mock) ListAsyncInvokes(_ context.Context) ([]driver.AsyncInvoke, error) {
	all := m.asyncInvokes.All()
	out := make([]driver.AsyncInvoke, 0, len(all))

	for _, inv := range all {
		out = append(out, *inv)
	}

	return out, nil
}
