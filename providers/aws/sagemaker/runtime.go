package sagemaker

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// InvokeEndpoint performs an emulated synchronous inference call. The endpoint
// must exist and be serving: InService, or Updating (the previous fleet keeps
// answering while a new configuration rolls out). The response echoes the
// request body, which is deterministic and adequate for exercising client
// serialization paths.
//
//nolint:gocritic // in matches the driver signature; copied on entry.
func (m *Mock) InvokeEndpoint(ctx context.Context, in driver.InvokeEndpointInput) (*driver.InvokeEndpointOutput, error) {
	ep, err := m.servingEndpoint(in.EndpointName)
	if err != nil {
		return nil, err
	}

	if in.InferenceComponentName != "" && !m.inferenceComponents.Has(in.InferenceComponentName) {
		return nil, errors.Newf(errors.NotFound, "inference component %q not found", in.InferenceComponentName)
	}

	variant, err := pickVariant(ep, in.TargetVariant)
	if err != nil {
		m.emitInvocationFailure(ctx, in.EndpointName, in.TargetVariant, http.StatusBadRequest, false)

		return nil, err
	}

	if status, failing := m.invocationFailure(in.EndpointName); failing {
		m.emitInvocationFailure(ctx, in.EndpointName, variant, status, true)

		return nil, &driver.ModelError{
			OriginalStatusCode: status,
			Message:            fmt.Sprintf("Received server error (%d) from primary with message \"model container failure\".", status),
		}
	}

	m.emitInvocation(ctx, in.EndpointName, variant, variantInstanceCount(ep, variant))

	body := in.Body
	if body == nil {
		body = []byte{}
	}

	return &driver.InvokeEndpointOutput{
		ContentType:    responseContentType(in.Accept, in.ContentType),
		Body:           body,
		InvokedVariant: variant,
	}, nil
}

// servingEndpoint returns the observed endpoint when it can answer invocations.
func (m *Mock) servingEndpoint(name string) (*driver.Endpoint, error) {
	m.epMu.Lock()
	ep, ok := m.liveEndpoint(name, m.opts.Clock.Now())
	m.epMu.Unlock()

	if !ok {
		return nil, errors.Newf(errors.NotFound, "Endpoint %s of account %s not found.", name, m.opts.AccountID)
	}

	if ep.Status != driver.EndpointInService && ep.Status != driver.EndpointUpdating {
		return nil, errors.Newf(errors.FailedPrecondition, "endpoint %q is not InService (status %s)", name, ep.Status)
	}

	return ep, nil
}

// pickVariant resolves the variant that serves a call: the one named by
// TargetVariant, otherwise the first variant carrying traffic.
func pickVariant(ep *driver.Endpoint, target string) (string, error) {
	if target != "" {
		for i := range ep.Variants {
			if ep.Variants[i].VariantName == target {
				return target, nil
			}
		}

		return "", errors.Newf(errors.InvalidArgument, "Variant %q is not a production variant of endpoint %q", target, ep.EndpointName)
	}

	for _, s := range ep.VariantSummaries {
		if s.CurrentWeight > 0 {
			return s.VariantName, nil
		}
	}

	if len(ep.Variants) > 0 {
		return ep.Variants[0].VariantName, nil
	}

	return "", nil
}

// responseContentType is the Content-Type of an invocation response. The
// emulated container echoes the request, so a concrete Accept is honored, and a
// wildcard or absent Accept falls back to the request ContentType (a real
// container answers in the type it produced, never the literal "*/*").
func responseContentType(accept, contentType string) string {
	if t := firstConcreteMediaType(accept); t != "" {
		return t
	}

	return orDefault(contentType, "application/octet-stream")
}

// firstConcreteMediaType returns the first media range in an Accept header that
// has no wildcard, without its parameters ("application/json;q=0.9" ->
// "application/json").
func firstConcreteMediaType(accept string) string {
	for _, part := range strings.Split(accept, ",") {
		mediaRange, _, _ := strings.Cut(part, ";")
		mt := strings.TrimSpace(mediaRange)

		if mt != "" && !strings.Contains(mt, "*") {
			return mt
		}
	}

	return ""
}

// maxInferenceIDLen is the documented maximum length of InferenceId.
const maxInferenceIDLen = 64

// InvokeEndpointAsync records an asynchronous invocation and returns the S3
// location under the endpoint configuration's async output path where the
// (echoed) result is considered to have been written. An endpoint whose
// configuration has no async output path cannot be invoked asynchronously.
func (m *Mock) InvokeEndpointAsync(ctx context.Context, in driver.InvokeEndpointAsyncInput) (*driver.InvokeEndpointAsyncOutput, error) {
	ep, err := m.servingEndpoint(in.EndpointName)
	if err != nil {
		return nil, err
	}

	if in.InputS3URI == "" {
		return nil, errors.New(errors.InvalidArgument, "InputLocation is required")
	}

	ec, ok := m.endpointConfigs.Get(ep.ConfigName)
	if !ok || ec.AsyncOutputS3URI == "" {
		return nil, errors.Newf(errors.InvalidArgument, "endpoint %q has no asynchronous inference configuration", in.EndpointName)
	}

	id := in.InferenceID
	if id == "" {
		id = idgen.GenerateID("")
	} else if len(id) > maxInferenceIDLen {
		return nil, errors.Newf(errors.InvalidArgument,
			"Value at 'inferenceId' failed to satisfy constraint: Member must have length less than or equal to %d", maxInferenceIDLen)
	}

	variant, _ := pickVariant(ep, "")
	m.emitInvocation(ctx, in.EndpointName, variant, variantInstanceCount(ep, variant))

	return &driver.InvokeEndpointAsyncOutput{
		OutputS3URI: strings.TrimSuffix(ec.AsyncOutputS3URI, "/") + "/" + id + ".out",
		InferenceID: id,
	}, nil
}

// clearInvocationFailure forgets a failure seeded on a deleted endpoint, so a
// re-created endpoint with the same name starts clean.
func (m *Mock) clearInvocationFailure(endpoint string) {
	m.invMu.Lock()
	defer m.invMu.Unlock()

	delete(m.invFailures, endpoint)
}

func (m *Mock) invocationFailure(endpoint string) (int, bool) {
	m.invMu.Lock()
	defer m.invMu.Unlock()

	status, ok := m.invFailures[endpoint]

	return status, ok
}
