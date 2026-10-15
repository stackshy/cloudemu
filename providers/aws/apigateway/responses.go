package apigateway

import (
	"context"
	"regexp"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Error messages for method and integration responses.
const (
	msgResponseNotFound = "Invalid Response status code specified"
	msgResponseExists   = "Response already exists for this resource"
	msgInvalidStatus    = "Invalid status code specified"
)

// responseStatusPattern is the StatusCode shape from the API model.
var responseStatusPattern = regexp.MustCompile(`^[1-5]\d\d$`)

// PutMethodResponse declares statusCode on a method.
func (m *Mock) PutMethodResponse(
	_ context.Context, restAPIID, resourceID, httpMethod, statusCode string, in driver.PutMethodResponseInput,
) (*driver.MethodResponse, error) {
	if !responseStatusPattern.MatchString(statusCode) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgInvalidStatus)
	}

	if err := validateMethodResponseParams(in.ResponseParameters); err != nil {
		return nil, err
	}

	var out driver.MethodResponse

	err := m.withMethod(restAPIID, resourceID, httpMethod, func(mth *driver.Method) error {
		if _, exists := mth.MethodResponses[statusCode]; exists {
			return cerrors.New(cerrors.AlreadyExists, msgResponseExists)
		}

		mr := &driver.MethodResponse{
			StatusCode:         statusCode,
			ResponseParameters: copyBoolMap(in.ResponseParameters),
			ResponseModels:     copyStrMap(in.ResponseModels),
		}

		if mth.MethodResponses == nil {
			mth.MethodResponses = map[string]*driver.MethodResponse{}
		}

		mth.MethodResponses[statusCode] = mr
		out = copyMethodResponse(mr)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &out, nil
}

// GetMethodResponse returns a declared method response.
func (m *Mock) GetMethodResponse(
	_ context.Context, restAPIID, resourceID, httpMethod, statusCode string,
) (*driver.MethodResponse, error) {
	mth, err := m.lookupMethod(restAPIID, resourceID, httpMethod)
	if err != nil {
		return nil, err
	}

	mr, ok := mth.MethodResponses[statusCode]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgResponseNotFound)
	}

	return mr, nil
}

// UpdateMethodResponse patches a method response's parameters and models.
func (m *Mock) UpdateMethodResponse(
	_ context.Context, restAPIID, resourceID, httpMethod, statusCode string, ops []driver.PatchOperation,
) (*driver.MethodResponse, error) {
	var out driver.MethodResponse

	err := m.withMethod(restAPIID, resourceID, httpMethod, func(mth *driver.Method) error {
		mr, ok := mth.MethodResponses[statusCode]
		if !ok {
			return cerrors.New(cerrors.NotFound, msgResponseNotFound)
		}

		next := copyMethodResponse(mr)

		for _, op := range ops {
			applyMapPatch(op, "/responseParameters/", func(k, v string, remove bool) {
				next.ResponseParameters = patchBoolMap(next.ResponseParameters, k, v, remove)
			})
			applyMapPatch(op, "/responseModels/", func(k, v string, remove bool) {
				next.ResponseModels = patchStrMap(next.ResponseModels, k, v, remove)
			})
		}

		if err := validateMethodResponseParams(next.ResponseParameters); err != nil {
			return err
		}

		*mr = next
		out = copyMethodResponse(mr)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &out, nil
}

// DeleteMethodResponse removes a method response.
func (m *Mock) DeleteMethodResponse(_ context.Context, restAPIID, resourceID, httpMethod, statusCode string) error {
	return m.withMethod(restAPIID, resourceID, httpMethod, func(mth *driver.Method) error {
		if _, ok := mth.MethodResponses[statusCode]; !ok {
			return cerrors.New(cerrors.NotFound, msgResponseNotFound)
		}

		delete(mth.MethodResponses, statusCode)

		return nil
	})
}

// PutIntegrationResponse creates or replaces the integration response for
// statusCode. The method must already declare that status code.
func (m *Mock) PutIntegrationResponse(
	_ context.Context, restAPIID, resourceID, httpMethod, statusCode string, in driver.PutIntegrationResponseInput,
) (*driver.IntegrationResponse, error) {
	if !responseStatusPattern.MatchString(statusCode) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgInvalidStatus)
	}

	if err := validateContentHandling(in.ContentHandling); err != nil {
		return nil, err
	}

	var out driver.IntegrationResponse

	err := m.withMethod(restAPIID, resourceID, httpMethod, func(mth *driver.Method) error {
		if mth.Integration == nil {
			return cerrors.New(cerrors.NotFound, msgIntegrationNotFound)
		}

		mr, ok := mth.MethodResponses[statusCode]
		if !ok {
			return cerrors.New(cerrors.NotFound, msgResponseNotFound)
		}

		ir := &driver.IntegrationResponse{
			StatusCode:         statusCode,
			SelectionPattern:   in.SelectionPattern,
			ResponseParameters: copyStrMap(in.ResponseParameters),
			ResponseTemplates:  copyStrMap(in.ResponseTemplates),
			ContentHandling:    in.ContentHandling,
		}

		if err := validateIntegrationResponse(ir, mr); err != nil {
			return err
		}

		if mth.Integration.IntegrationResponses == nil {
			mth.Integration.IntegrationResponses = map[string]*driver.IntegrationResponse{}
		}

		mth.Integration.IntegrationResponses[statusCode] = ir
		out = copyIntegrationResponse(ir)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &out, nil
}

// GetIntegrationResponse returns an integration response.
func (m *Mock) GetIntegrationResponse(
	_ context.Context, restAPIID, resourceID, httpMethod, statusCode string,
) (*driver.IntegrationResponse, error) {
	mth, err := m.lookupMethod(restAPIID, resourceID, httpMethod)
	if err != nil {
		return nil, err
	}

	if mth.Integration == nil {
		return nil, cerrors.New(cerrors.NotFound, msgIntegrationNotFound)
	}

	ir, ok := mth.Integration.IntegrationResponses[statusCode]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgResponseNotFound)
	}

	return ir, nil
}

// UpdateIntegrationResponse patches an integration response.
func (m *Mock) UpdateIntegrationResponse(
	_ context.Context, restAPIID, resourceID, httpMethod, statusCode string, ops []driver.PatchOperation,
) (*driver.IntegrationResponse, error) {
	var out driver.IntegrationResponse

	err := m.withMethod(restAPIID, resourceID, httpMethod, func(mth *driver.Method) error {
		if mth.Integration == nil {
			return cerrors.New(cerrors.NotFound, msgIntegrationNotFound)
		}

		ir, ok := mth.Integration.IntegrationResponses[statusCode]
		if !ok {
			return cerrors.New(cerrors.NotFound, msgResponseNotFound)
		}

		next := copyIntegrationResponse(ir)
		for _, op := range ops {
			applyIntegrationResponsePatch(&next, op)
		}

		if err := validateContentHandling(next.ContentHandling); err != nil {
			return err
		}

		if err := validateIntegrationResponse(&next, mth.MethodResponses[statusCode]); err != nil {
			return err
		}

		*ir = next
		out = copyIntegrationResponse(ir)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &out, nil
}

func applyIntegrationResponsePatch(ir *driver.IntegrationResponse, op driver.PatchOperation) {
	switch op.Path {
	case "/selectionPattern":
		ir.SelectionPattern = patchRef(op)
	case pathContentHandling:
		ir.ContentHandling = patchRef(op)
	default:
		applyMapPatch(op, "/responseTemplates/", func(k, v string, remove bool) {
			ir.ResponseTemplates = patchStrMap(ir.ResponseTemplates, k, v, remove)
		})
		applyMapPatch(op, "/responseParameters/", func(k, v string, remove bool) {
			ir.ResponseParameters = patchStrMap(ir.ResponseParameters, k, v, remove)
		})
	}
}

// DeleteIntegrationResponse removes an integration response.
func (m *Mock) DeleteIntegrationResponse(_ context.Context, restAPIID, resourceID, httpMethod, statusCode string) error {
	return m.withMethod(restAPIID, resourceID, httpMethod, func(mth *driver.Method) error {
		if mth.Integration == nil {
			return cerrors.New(cerrors.NotFound, msgIntegrationNotFound)
		}

		if _, ok := mth.Integration.IntegrationResponses[statusCode]; !ok {
			return cerrors.New(cerrors.NotFound, msgResponseNotFound)
		}

		delete(mth.Integration.IntegrationResponses, statusCode)

		return nil
	})
}

// withMethod runs fn on the live method under the API's write lock.
func (m *Mock) withMethod(restAPIID, resourceID, httpMethod string, fn func(*driver.Method) error) error {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	res, ok := ad.resources[resourceID]
	if !ok {
		return cerrors.New(cerrors.NotFound, msgResourceNotFound)
	}

	mth, ok := res.Methods[normalizeMethod(httpMethod)]
	if !ok {
		return cerrors.New(cerrors.NotFound, msgMethodNotFound)
	}

	return fn(mth)
}

// applyMapPatch calls set for an op whose path is prefix + an escaped map key.
// remove is true for a remove op.
func applyMapPatch(op driver.PatchOperation, prefix string, set func(key, value string, remove bool)) {
	if len(op.Path) <= len(prefix) || op.Path[:len(prefix)] != prefix {
		return
	}

	set(unescapePointer(op.Path[len(prefix):]), op.Value, op.Op == opRemove)
}

func patchStrMap(m map[string]string, k, v string, remove bool) map[string]string {
	if remove {
		delete(m, k)

		return m
	}

	if m == nil {
		m = map[string]string{}
	}

	m[k] = v

	return m
}

func patchBoolMap(m map[string]bool, k, v string, remove bool) map[string]bool {
	if remove {
		delete(m, k)

		return m
	}

	if m == nil {
		m = map[string]bool{}
	}

	m[k] = parseBool(v)

	return m
}

func copyBoolMap(in map[string]bool) map[string]bool {
	if in == nil {
		return nil
	}

	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyMethodResponse(mr *driver.MethodResponse) driver.MethodResponse {
	return driver.MethodResponse{
		StatusCode:         mr.StatusCode,
		ResponseParameters: copyBoolMap(mr.ResponseParameters),
		ResponseModels:     copyStrMap(mr.ResponseModels),
	}
}

func copyIntegrationResponse(ir *driver.IntegrationResponse) driver.IntegrationResponse {
	out := *ir
	out.ResponseParameters = copyStrMap(ir.ResponseParameters)
	out.ResponseTemplates = copyStrMap(ir.ResponseTemplates)

	return out
}

// copyIntegration deep-copies an integration and its responses.
func copyIntegration(ig *driver.Integration) driver.Integration {
	out := *ig
	out.RequestParameters = copyStrMap(ig.RequestParameters)
	out.RequestTemplates = copyStrMap(ig.RequestTemplates)
	out.CacheKeyParameters = append([]string(nil), ig.CacheKeyParameters...)

	if ig.IntegrationResponses != nil {
		out.IntegrationResponses = make(map[string]*driver.IntegrationResponse, len(ig.IntegrationResponses))

		for code, ir := range ig.IntegrationResponses {
			cp := copyIntegrationResponse(ir)
			out.IntegrationResponses[code] = &cp
		}
	}

	return out
}

// copyMethod deep-copies a method, its responses and its integration.
func copyMethod(mth *driver.Method) driver.Method {
	out := *mth
	out.RequestParameters = copyBoolMap(mth.RequestParameters)
	out.RequestModels = copyStrMap(mth.RequestModels)
	out.AuthorizationScopes = copyStrSlice(mth.AuthorizationScopes)

	if mth.MethodResponses != nil {
		out.MethodResponses = make(map[string]*driver.MethodResponse, len(mth.MethodResponses))

		for code, mr := range mth.MethodResponses {
			cp := copyMethodResponse(mr)
			out.MethodResponses[code] = &cp
		}
	}

	if mth.Integration != nil {
		ig := copyIntegration(mth.Integration)
		out.Integration = &ig
	}

	return out
}

// sortedResponseCodes returns an integration's response status codes in
// ascending order, so selection is deterministic.
func sortedResponseCodes(irs map[string]*driver.IntegrationResponse) []string {
	codes := make([]string, 0, len(irs))
	for c := range irs {
		codes = append(codes, c)
	}

	sort.Strings(codes)

	return codes
}
