package apigateway

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// validateRequest applies the method's request validator: required request
// parameters (400 BAD_REQUEST_PARAMETERS) and the body against the model of its
// content type (400 BAD_REQUEST_BODY).
func (m *Mock) validateRequest(req *driver.ProxyRequest, route *resolvedRoute, reqID string) *driver.ProxyResponse {
	v := route.validator
	if v == nil {
		return nil
	}

	if v.ValidateRequestParameters {
		if missing := missingRequiredParams(req, route); len(missing) > 0 {
			return m.gatewayResponse(route, req, reqID, respBadParameters,
				"Missing required request parameters: ["+strings.Join(missing, ", ")+"]")
		}
	}

	if v.ValidateRequestBody && !bodyMatchesModel(req, route) {
		return m.gatewayResponse(route, req, reqID, respBadBody, "Invalid request body")
	}

	return nil
}

// missingRequiredParams lists the required method request parameters the request
// does not carry, sorted.
func missingRequiredParams(req *driver.ProxyRequest, route *resolvedRoute) []string {
	var missing []string

	for key, required := range route.method.RequestParameters {
		if !required {
			continue
		}

		loc, name, ok := splitMethodParam(key)
		if ok && !hasRequestParam(req, route, loc, name) {
			missing = append(missing, name)
		}
	}

	sort.Strings(missing)

	return missing
}

// splitMethodParam splits method.request.{location}.{name}.
func splitMethodParam(key string) (loc, name string, ok bool) {
	rest, found := strings.CutPrefix(key, "method.request.")
	if !found {
		return "", "", false
	}

	loc, name, ok = strings.Cut(rest, ".")

	return loc, name, ok && name != ""
}

func hasRequestParam(req *driver.ProxyRequest, route *resolvedRoute, loc, name string) bool {
	switch loc {
	case locHeader:
		return headerValue(req.Headers, name) != ""
	case locQuery:
		_, ok := req.Query[name]

		return ok
	case locPath:
		_, ok := route.pathParameters[name]

		return ok
	default:
		return true
	}
}

// bodyMatchesModel validates the request body against the model registered for
// its content type. A method with no model for the content type accepts any
// body, as API Gateway does.
func bodyMatchesModel(req *driver.ProxyRequest, route *resolvedRoute) bool {
	ct := mediaType(headerValue(req.Headers, headerContentType))
	if ct == "" {
		ct = contentTypeJSON
	}

	schema, ok := route.schemas[ct]
	if !ok || strings.TrimSpace(schema) == "" {
		return true
	}

	var doc any
	if err := json.Unmarshal([]byte(req.Body), &doc); err != nil {
		return false
	}

	var sch map[string]any
	if err := json.Unmarshal([]byte(schema), &sch); err != nil {
		return true
	}

	return validateSchema(doc, sch, route.models, sch, 0)
}
