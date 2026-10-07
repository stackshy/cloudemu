package apigateway

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// headerLambdaStub marks a response produced from the Lambda mock's echo stub.
const headerLambdaStub = "X-Cloudemu-Lambda-Stub"

// gatewayErrorTypes maps a gateway response type to the x-amzn-ErrorType value
// API Gateway sends with it.
//
//nolint:gochecknoglobals // immutable lookup table of error type names
var gatewayErrorTypes = map[string]string{
	respMissingToken: "MissingAuthenticationTokenException", respInvalidKey: "ForbiddenException",
	respAccessDenied: "AccessDeniedException", respUnauthorized: "UnauthorizedException",
	respThrottled: "TooManyRequestsException", respQuotaExceeded: "LimitExceededException",
	respBadBody: "BadRequestException", respBadParameters: "BadRequestException",
	respAuthorizerFail: "AuthorizerFailureException", respAuthorizerCfg: "AuthorizerConfigurationException",
	respAPIConfigError: "ApiConfigurationException", respIntegFailure: "IntegrationFailureException",
	respIntegTimeout: "IntegrationTimeoutException", respTooLarge: "RequestTooLargeException",
}

// effectiveOverride picks the gateway response in force for responseType: the
// type's own override, else the DEFAULT_4XX/DEFAULT_5XX override of its class.
func effectiveOverride(route *resolvedRoute, responseType string) (driver.GatewayResponse, bool) {
	if gr, ok := route.overrides[responseType]; ok {
		return gr, true
	}

	class := respDefault4xx
	if gatewayResponseDefaults[responseType] >= status5xxMin {
		class = respDefault5xx
	}

	gr, ok := route.overrides[class]

	return gr, ok
}

// gatewayResponse builds the response API Gateway itself returns for an error of
// responseType, applying the API's gateway response override (status, headers
// and body template) when there is one. message is the default error text.
func (*Mock) gatewayResponse(
	route *resolvedRoute, req *driver.ProxyRequest, reqID, responseType, message string,
) *driver.ProxyResponse {
	status := gatewayResponseDefaults[responseType]
	body := `{"message":` + jsonQuote(message) + `}`
	headers := map[string]string{
		headerContentType: contentTypeJSON, headerRequestID: reqID, headerErrorType: gatewayErrorTypes[responseType],
	}

	if gr, ok := effectiveOverride(route, responseType); ok {
		if n, err := strconv.Atoi(gr.StatusCode); err == nil && n != 0 {
			status = n
		}

		if tmpl, ct, found := pickGatewayTemplate(gr.ResponseTemplates); found {
			body = renderGatewayTemplate(tmpl, message, responseType, reqID)
			headers[headerContentType] = ct
		}

		applyGatewayHeaders(headers, gr.ResponseParameters, req)
	}

	return &driver.ProxyResponse{StatusCode: status, Headers: headers, Body: body}
}

// pickGatewayTemplate selects the application/json template, else the only or
// first one (the "$default" key is the catch-all).
func pickGatewayTemplate(templates map[string]string) (tmpl, contentType string, ok bool) {
	if t, found := templates[contentTypeJSON]; found {
		return t, contentTypeJSON, true
	}

	if t, found := templates["$default"]; found {
		return t, contentTypeJSON, true
	}

	for _, ct := range sortedKeys(templates) {
		return templates[ct], ct, true
	}

	return "", "", false
}

// renderGatewayTemplate substitutes the $context.error.* and $context.requestId
// variables a gateway response template may use.
func renderGatewayTemplate(tmpl, message, responseType, reqID string) string {
	return strings.NewReplacer(
		"$context.error.messageString", jsonQuote(message),
		"$context.error.message", message,
		"$context.error.responseType", responseType,
		"$context.requestId", reqID,
	).Replace(tmpl)
}

// applyGatewayHeaders sets the headers a gateway response maps:
// gatewayresponse.header.X from a literal ('value') or a method.request source.
func applyGatewayHeaders(headers, params map[string]string, req *driver.ProxyRequest) {
	for _, key := range sortedKeys(params) {
		name, ok := strings.CutPrefix(key, "gatewayresponse.header.")
		if !ok {
			continue
		}

		src := params[key]

		switch {
		case len(src) >= 2 && strings.HasPrefix(src, "'") && strings.HasSuffix(src, "'"):
			headers[name] = src[1 : len(src)-1]
		case strings.HasPrefix(src, "method.request.header."):
			headers[name] = headerValue(req.Headers, strings.TrimPrefix(src, "method.request.header."))
		case strings.HasPrefix(src, "method.request.querystring."):
			headers[name] = req.Query[strings.TrimPrefix(src, "method.request.querystring.")]
		}
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)

	return string(b)
}

// expandStageVariables substitutes ${stageVariables.name} in s. When a variable
// is not defined it returns that variable's name as missing.
func expandStageVariables(s string, vars map[string]string) (out, missing string) {
	const open = "${stageVariables."

	var b strings.Builder

	for {
		i := strings.Index(s, open)
		if i < 0 {
			b.WriteString(s)

			return b.String(), missing
		}

		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			b.WriteString(s)

			return b.String(), missing
		}

		b.WriteString(s[:i])

		name := s[i+len(open) : i+end]
		if v, ok := vars[name]; ok {
			b.WriteString(v)
		} else if missing == "" {
			missing = name
		}

		s = s[i+end+1:]
	}
}

// authorizerContext is requestContext.authorizer: the principal id, the
// authorizer's context values and, for Cognito, the token claims.
func authorizerContext(route *resolvedRoute) map[string]any {
	if route.principalID == "" && len(route.authContext) == 0 {
		return nil
	}

	out := make(map[string]any, len(route.authContext))
	for k, v := range route.authContext {
		out[k] = v
	}

	if route.principalID != "" {
		out["principalId"] = route.principalID
	}

	return out
}

// escapePathSegment escapes one URL path segment, keeping "/" as a separator for
// a greedy parameter.
func escapePathSegment(s string) string {
	parts := strings.Split(s, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}

	return strings.Join(parts, "/")
}
