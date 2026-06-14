package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/sagemaker/driver"
)

// InvokeEndpoint performs an emulated synchronous inference call. The endpoint
// must exist and be InService. The response echoes the request body, which is
// deterministic and adequate for exercising client serialization paths.
//
//nolint:gocritic // in matches the driver signature; copied on entry.
func (m *Mock) InvokeEndpoint(_ context.Context, in driver.InvokeEndpointInput) (*driver.InvokeEndpointOutput, error) {
	ep, ok := m.endpoints.Get(in.EndpointName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", in.EndpointName)
	}

	if ep.Status != driver.EndpointInService {
		return nil, errors.Newf(errors.FailedPrecondition, "endpoint %q is not InService (status %s)", in.EndpointName, ep.Status)
	}

	if in.InferenceComponentName != "" && !m.inferenceComponents.Has(in.InferenceComponentName) {
		return nil, errors.Newf(errors.NotFound, "inference component %q not found", in.InferenceComponentName)
	}

	m.emitInvocation(in.EndpointName)

	variant := ""
	if len(ep.Variants) > 0 {
		variant = ep.Variants[0].VariantName
	}

	body := in.Body
	if body == nil {
		body = []byte{}
	}

	return &driver.InvokeEndpointOutput{
		ContentType:    orDefault(in.Accept, orDefault(in.ContentType, "application/octet-stream")),
		Body:           body,
		InvokedVariant: variant,
	}, nil
}

// InvokeEndpointAsync records an asynchronous invocation and returns the S3
// location where the (echoed) result is considered to have been written.
//

func (m *Mock) InvokeEndpointAsync(_ context.Context, in driver.InvokeEndpointAsyncInput) (*driver.InvokeEndpointAsyncOutput, error) {
	ep, ok := m.endpoints.Get(in.EndpointName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", in.EndpointName)
	}

	if ep.Status != driver.EndpointInService {
		return nil, errors.Newf(errors.FailedPrecondition, "endpoint %q is not InService (status %s)", in.EndpointName, ep.Status)
	}

	m.emitInvocation(in.EndpointName)

	id := idgen.GenerateID("")

	return &driver.InvokeEndpointAsyncOutput{
		OutputS3URI: "s3://sagemaker-async-results/" + in.EndpointName + "/" + id + ".out",
		InferenceID: id,
	}, nil
}
