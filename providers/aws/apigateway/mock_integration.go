package apigateway

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/jsonpath"
	"github.com/stackshy/cloudemu/v2/internal/vtl"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Gateway error statuses and bodies for mapping failures.
const (
	statusOK                   = 200
	statusInternalServerError  = 500
	statusUnsupportedMediaType = 415
	bodyInternalServerError    = `{"message": "Internal server error"}`
	bodyUnsupportedMediaType   = `{"message": "Unsupported Media Type"}`
	contentTypeJSON            = "application/json"
	headerContentType          = "Content-Type"
	headerRequestID            = "x-amzn-RequestId"
	headerErrorType            = "x-amzn-ErrorType"
)

// serveMock answers a MOCK integration: the request template (picked by
// Content-Type, subject to passthroughBehavior) yields a JSON document whose
// statusCode selects the integration response, whose template renders the
// body.
func (m *Mock) serveMock(ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute) *driver.ProxyResponse {
	mc := newMappingContext(req, route, m.opts.AccountID, idgen.UUID(), m.opts.Clock.Now())

	payload, templated, rejected := mapRequest(ctx, mc, &route.integration, req)
	if rejected != nil {
		return rejected
	}

	status, ok := mockStatusCode(payload, templated)
	if !ok {
		return gatewayError(statusInternalServerError, bodyInternalServerError, "InternalServerErrorException", mc.reqID)
	}

	return mapResponse(ctx, mc, route, strconv.Itoa(status), "", nil)
}

// mapRequest applies the integration request template. It returns the
// integration payload and whether a template produced it, or a 415/500
// response when the request is rejected.
func mapRequest(
	ctx context.Context, mc *mappingContext, ig *driver.Integration, req *driver.ProxyRequest,
) (payload string, templated bool, rejected *driver.ProxyResponse) {
	ct := mediaType(headerValue(req.Headers, headerContentType))
	if ct == "" {
		ct = contentTypeJSON
	}

	tmpl, found := lookupTemplate(ig.RequestTemplates, ct)
	if !found {
		if passthroughAllowed(ig) {
			return req.Body, false, nil
		}

		return "", false, gatewayError(statusUnsupportedMediaType, bodyUnsupportedMediaType,
			"UnsupportedMediaTypeException", mc.reqID)
	}

	out, err := mc.render(ctx, tmpl, req.Body)
	if err != nil {
		return "", false, gatewayError(statusInternalServerError, bodyInternalServerError,
			"InternalServerErrorException", mc.reqID)
	}

	return out, true, nil
}

// passthroughAllowed applies passthroughBehavior to a request whose content
// type has no template.
func passthroughAllowed(ig *driver.Integration) bool {
	switch ig.PassthroughBehavior {
	case driver.PassthroughNever:
		return false
	case driver.PassthroughWhenNoTemplates:
		return len(ig.RequestTemplates) == 0
	default:
		return true
	}
}

// mockStatusCode reads statusCode from a MOCK request payload. A payload that
// is not JSON is a configuration error only when a template produced it; a
// missing statusCode means 200.
func mockStatusCode(payload string, templated bool) (int, bool) {
	if strings.TrimSpace(payload) == "" {
		return statusOK, true
	}

	v, err := vtl.ParseJSON(payload)
	if err != nil {
		return statusOK, !templated
	}

	doc, ok := v.(*vtl.Map)
	if !ok {
		return statusOK, true
	}

	raw, ok := doc.Get("statusCode")
	if !ok {
		return statusOK, true
	}

	switch n := raw.(type) {
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}

	return 0, false
}

// mapResponse selects the integration response for backendStatus, renders its
// template against the backend body and applies its header mappings and any
// $context.responseOverride. With no matching and no default integration
// response the request fails with a 500, as API Gateway does.
func mapResponse(
	ctx context.Context, mc *mappingContext, route *resolvedRoute,
	backendStatus, backendBody string, backendHeaders map[string]string,
) *driver.ProxyResponse {
	ir := selectIntegrationResponse(route.integration.IntegrationResponses, backendStatus)
	if ir == nil {
		return gatewayError(statusInternalServerError, bodyInternalServerError, "InternalServerErrorException", mc.reqID)
	}

	status, err := strconv.Atoi(ir.StatusCode)
	if err != nil {
		return gatewayError(statusInternalServerError, bodyInternalServerError, "InternalServerErrorException", mc.reqID)
	}

	resp := &driver.ProxyResponse{
		StatusCode: status,
		Headers:    map[string]string{headerContentType: contentTypeJSON, headerRequestID: mc.reqID},
		Body:       backendBody,
	}

	if ct, tmpl, ok := selectResponseTemplate(ir.ResponseTemplates, headerValue(mc.req.Headers, "Accept")); ok {
		resp.Headers[headerContentType] = ct

		if strings.TrimSpace(tmpl) != "" {
			out, err := mc.render(ctx, tmpl, backendBody)
			if err != nil {
				return gatewayError(statusInternalServerError, bodyInternalServerError, "InternalServerErrorException", mc.reqID)
			}

			resp.Body = out
		}
	}

	for _, key := range sortedKeys(ir.ResponseParameters) {
		if v, ok := mc.resolveResponseSource(ir.ResponseParameters[key], backendBody, backendHeaders); ok {
			resp.Headers[headerName(key)] = v
		}
	}

	overrideStatus, overrideHeaders := mc.responseOverride()
	if overrideStatus != 0 {
		resp.StatusCode = overrideStatus
	}

	for k, v := range overrideHeaders {
		resp.Headers[k] = v
	}

	return resp
}

// selectIntegrationResponse returns the first integration response (by status
// code) whose selection pattern fully matches match, else the default one
// (empty pattern), else nil.
func selectIntegrationResponse(irs map[string]*driver.IntegrationResponse, match string) *driver.IntegrationResponse {
	var def *driver.IntegrationResponse

	for _, code := range sortedResponseCodes(irs) {
		ir := irs[code]

		if ir.SelectionPattern == "" {
			if def == nil {
				def = ir
			}

			continue
		}

		re, err := regexp.Compile("^(?:" + ir.SelectionPattern + ")$")
		if err == nil && re.MatchString(match) {
			return ir
		}
	}

	return def
}

// selectResponseTemplate picks a response template by the request's Accept
// header, falling back to application/json and then to the first content type.
func selectResponseTemplate(templates map[string]string, accept string) (contentType, tmpl string, ok bool) {
	if len(templates) == 0 {
		return "", "", false
	}

	for _, part := range strings.Split(accept, ",") {
		if ct := mediaType(part); ct != "" {
			if key, found := templateKey(templates, ct); found {
				return key, templates[key], true
			}
		}
	}

	if key, found := templateKey(templates, contentTypeJSON); found {
		return key, templates[key], true
	}

	key := sortedKeys(templates)[0]

	return key, templates[key], true
}

// lookupTemplate finds the template for a content type, case-insensitively.
func lookupTemplate(templates map[string]string, ct string) (string, bool) {
	key, ok := templateKey(templates, ct)
	if !ok {
		return "", false
	}

	return templates[key], true
}

func templateKey(templates map[string]string, ct string) (string, bool) {
	for _, k := range sortedKeys(templates) {
		if strings.EqualFold(k, ct) {
			return k, true
		}
	}

	return "", false
}

// mediaType strips parameters and whitespace from a Content-Type or Accept
// entry and lower-cases it.
func mediaType(v string) string {
	if i := strings.IndexByte(v, ';'); i >= 0 {
		v = v[:i]
	}

	return strings.ToLower(strings.TrimSpace(v))
}

// resolveResponseSource evaluates an integration response parameter source:
// a 'static' value, an integration.response header or body, a stage variable
// or a $context value.
func (mc *mappingContext) resolveResponseSource(src, body string, headers map[string]string) (string, bool) {
	const (
		headerPrefix    = "integration.response.header."
		multiHeaderPref = "integration.response.multivalueheader."
		bodyRef         = "integration.response.body"
		stagePrefix     = "stageVariables."
		contextPrefix   = "context."
	)

	switch {
	case len(src) >= 2 && src[0] == '\'' && src[len(src)-1] == '\'':
		return src[1 : len(src)-1], true
	case strings.HasPrefix(src, headerPrefix):
		v := headerValue(headers, strings.TrimPrefix(src, headerPrefix))

		return v, v != ""
	case strings.HasPrefix(src, multiHeaderPref):
		v := headerValue(headers, strings.TrimPrefix(src, multiHeaderPref))

		return v, v != ""
	case src == bodyRef:
		return body, true
	case strings.HasPrefix(src, bodyRef+"."):
		return bodyPath(body, "$"+strings.TrimPrefix(src, bodyRef))
	case strings.HasPrefix(src, stagePrefix):
		v, ok := mc.route.stageVariables[strings.TrimPrefix(src, stagePrefix)]

		return v, ok
	case strings.HasPrefix(src, contextPrefix):
		return contextValue(mc.context, strings.TrimPrefix(src, contextPrefix))
	}

	return "", false
}

func bodyPath(body, path string) (string, bool) {
	doc, err := vtl.ParseJSON(body)
	if err != nil {
		return "", false
	}

	v, ok, err := jsonpath.Eval(path, doc)
	if err != nil || !ok || v == nil {
		return "", false
	}

	switch v.(type) {
	case *vtl.Map, *vtl.List:
		return vtl.ToJSON(v), true
	default:
		return vtl.Stringify(v), true
	}
}

// contextValue walks a dotted path (e.g. identity.sourceIp) into $context.
func contextValue(ctx *vtl.Map, path string) (string, bool) {
	var cur any = ctx

	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(*vtl.Map)
		if !ok {
			return "", false
		}

		if cur, ok = m.Get(part); !ok {
			return "", false
		}
	}

	s := vtl.Stringify(cur)

	return s, s != ""
}

// gatewayError is an error API Gateway itself produces.
func gatewayError(status int, body, errType, reqID string) *driver.ProxyResponse {
	return &driver.ProxyResponse{
		StatusCode: status,
		Headers: map[string]string{
			headerContentType: contentTypeJSON, headerErrorType: errType, headerRequestID: reqID,
		},
		Body: body,
	}
}
