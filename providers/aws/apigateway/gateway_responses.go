package apigateway

import (
	"context"
	"sort"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

var _ driver.GatewayResponses = (*Mock)(nil)

const (
	msgGatewayResponseType     = "Invalid gateway response type specified"
	msgGatewayResponseNotFound = "Invalid Response type specified"
	msgGatewayStatus           = "Status code must be a three digit HTTP status code"
)

// Gateway response types and the status each defaults to. A zero status means
// the default response takes no fixed status (DEFAULT_4XX and DEFAULT_5XX).
//
//nolint:gochecknoglobals // immutable lookup table of the gateway response types
var gatewayResponseDefaults = map[string]int{
	respDefault4xx: 0, respDefault5xx: 0,
	"ACCESS_DENIED": 403, "API_CONFIGURATION_ERROR": 500, "AUTHORIZER_CONFIGURATION_ERROR": 500,
	"AUTHORIZER_FAILURE": 500, "BAD_REQUEST_BODY": 400, "BAD_REQUEST_PARAMETERS": 400,
	"EXPIRED_TOKEN": 403, "INTEGRATION_FAILURE": 504, "INTEGRATION_TIMEOUT": 504,
	"INVALID_API_KEY": 403, "INVALID_SIGNATURE": 403, "MISSING_AUTHENTICATION_TOKEN": 403,
	"QUOTA_EXCEEDED": 429, "REQUEST_TOO_LARGE": 413, "RESOURCE_NOT_FOUND": 404,
	"THROTTLED": 429, "UNAUTHORIZED": 401, "UNSUPPORTED_MEDIA_TYPE": 415, "WAF_FILTERED": 403,
}

// Response types the data plane raises.
const (
	respDefault4xx     = "DEFAULT_4XX"
	respDefault5xx     = "DEFAULT_5XX"
	respMissingToken   = "MISSING_AUTHENTICATION_TOKEN"
	respInvalidKey     = "INVALID_API_KEY"
	respThrottled      = "THROTTLED"
	respQuotaExceeded  = "QUOTA_EXCEEDED"
	respUnauthorized   = "UNAUTHORIZED"
	respAccessDenied   = "ACCESS_DENIED"
	respBadBody        = "BAD_REQUEST_BODY"
	respBadParameters  = "BAD_REQUEST_PARAMETERS"
	respAuthorizerFail = "AUTHORIZER_FAILURE"
	respAuthorizerCfg  = "AUTHORIZER_CONFIGURATION_ERROR"
	respAPIConfigError = "API_CONFIGURATION_ERROR"
	respIntegFailure   = "INTEGRATION_FAILURE"
	respIntegTimeout   = "INTEGRATION_TIMEOUT"
	respTooLarge       = "REQUEST_TOO_LARGE"
)

// defaultGatewayResponse is the response API Gateway returns when nothing
// overrides responseType: the fixed status (DEFAULT_4XX/5XX fall back to the
// class's own default) and the JSON message template.
func defaultGatewayResponse(responseType string) driver.GatewayResponse {
	status := gatewayResponseDefaults[responseType]
	gr := driver.GatewayResponse{ResponseType: responseType, DefaultResponse: true}

	if status != 0 {
		gr.StatusCode = strconv.Itoa(status)
		gr.ResponseTemplates = map[string]string{defaultContentType: `{"message":$context.error.messageString}`}
	}

	if responseType == respDefault4xx || responseType == respDefault5xx {
		gr.ResponseTemplates = map[string]string{defaultContentType: `{"message":$context.error.messageString}`}
	}

	return gr
}

func validGatewayResponseType(t string) bool {
	_, ok := gatewayResponseDefaults[t]

	return ok
}

// PutGatewayResponse overrides the response for one response type.
func (m *Mock) PutGatewayResponse(
	_ context.Context, restAPIID, responseType string, in *driver.PutGatewayResponseInput,
) (*driver.GatewayResponse, error) {
	if !validGatewayResponseType(responseType) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgGatewayResponseType)
	}

	if in.StatusCode != "" && !validStatus(in.StatusCode) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgGatewayStatus)
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	gr := &driver.GatewayResponse{
		ResponseType: responseType, StatusCode: in.StatusCode,
		ResponseParameters: copyStrMap(in.ResponseParameters), ResponseTemplates: copyStrMap(in.ResponseTemplates),
	}
	ad.gwResponses[responseType] = gr

	out := copyGatewayResponse(gr)

	return &out, nil
}

func validStatus(s string) bool {
	const statusLen = 3

	if len(s) != statusLen {
		return false
	}

	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}

	return s[0] >= '1' && s[0] <= '5'
}

// GetGatewayResponse returns the effective response of a type: the override, or
// the default.
func (m *Mock) GetGatewayResponse(_ context.Context, restAPIID, responseType string) (*driver.GatewayResponse, error) {
	if !validGatewayResponseType(responseType) {
		return nil, cerrors.New(cerrors.NotFound, msgGatewayResponseNotFound)
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := effectiveGatewayResponse(ad, responseType)

	return &out, nil
}

func effectiveGatewayResponse(ad *apiData, responseType string) driver.GatewayResponse {
	if gr, ok := ad.gwResponses[responseType]; ok {
		return copyGatewayResponse(gr)
	}

	return defaultGatewayResponse(responseType)
}

// GetGatewayResponses lists every response type with its effective response,
// ordered by type.
func (m *Mock) GetGatewayResponses(
	_ context.Context, restAPIID string, page driver.PageInput,
) (*driver.GatewayResponsePage, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	types := make([]string, 0, len(gatewayResponseDefaults))
	for t := range gatewayResponseDefaults {
		types = append(types, t)
	}

	sort.Strings(types)

	ad.mu.RLock()

	all := make([]driver.GatewayResponse, 0, len(types))
	for _, t := range types {
		all = append(all, effectiveGatewayResponse(ad, t))
	}

	ad.mu.RUnlock()

	items, next, err := pageOf(all, page)
	if err != nil {
		return nil, err
	}

	return &driver.GatewayResponsePage{Items: items, Position: next}, nil
}

// UpdateGatewayResponse patches /statusCode, /responseParameters/{name} and
// /responseTemplates/{contentType}; it creates the override from the default.
func (m *Mock) UpdateGatewayResponse(
	_ context.Context, restAPIID, responseType string, ops []driver.PatchOperation,
) (*driver.GatewayResponse, error) {
	if !validGatewayResponseType(responseType) {
		return nil, cerrors.New(cerrors.NotFound, msgGatewayResponseNotFound)
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	upd := effectiveGatewayResponse(ad, responseType)
	upd.DefaultResponse = false

	for _, op := range ops {
		if err := applyGatewayResponsePatch(&upd, op); err != nil {
			return nil, err
		}
	}

	ad.gwResponses[responseType] = &upd
	out := copyGatewayResponse(&upd)

	return &out, nil
}

func applyGatewayResponsePatch(gr *driver.GatewayResponse, op driver.PatchOperation) error {
	switch {
	case op.Path == "/statusCode":
		if !validStatus(op.Value) {
			return cerrors.New(cerrors.InvalidArgument, msgGatewayStatus)
		}

		gr.StatusCode = op.Value
	case strings.HasPrefix(op.Path, "/responseParameters/"):
		gr.ResponseParameters = patchMapEntry(gr.ResponseParameters, op, "/responseParameters/")
	case strings.HasPrefix(op.Path, "/responseTemplates/"):
		gr.ResponseTemplates = patchMapEntry(gr.ResponseTemplates, op, "/responseTemplates/")
	default:
		return invalidPatchPath(op, "/statusCode", "/responseParameters/{name}", "/responseTemplates/{contentType}")
	}

	return nil
}

// patchMapEntry sets or removes one entry of a string map addressed by the path
// token after prefix.
func patchMapEntry(in map[string]string, op driver.PatchOperation, prefix string) map[string]string {
	key := unescapePointer(strings.TrimPrefix(op.Path, prefix))
	out := copyStrMap(in)

	if out == nil {
		out = map[string]string{}
	}

	if op.Op == opRemove {
		delete(out, key)
	} else {
		out[key] = op.Value
	}

	return out
}

// DeleteGatewayResponse restores the default response of a type.
func (m *Mock) DeleteGatewayResponse(_ context.Context, restAPIID, responseType string) error {
	if !validGatewayResponseType(responseType) {
		return cerrors.New(cerrors.NotFound, msgGatewayResponseNotFound)
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.gwResponses[responseType]; !ok {
		return cerrors.New(cerrors.NotFound, msgGatewayResponseNotFound)
	}

	delete(ad.gwResponses, responseType)

	return nil
}

func copyGatewayResponse(gr *driver.GatewayResponse) driver.GatewayResponse {
	out := *gr
	out.ResponseParameters = copyStrMap(gr.ResponseParameters)
	out.ResponseTemplates = copyStrMap(gr.ResponseTemplates)

	return out
}
