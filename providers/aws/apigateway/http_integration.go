package apigateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// errBackendConfig is an integration configuration error (a missing stage variable
// or path mapping, or an unusable endpoint URL).
var errBackendConfig = errors.New("integration configuration error")

// HTTPDoer is the outbound HTTP seam of HTTP and HTTP_PROXY integrations. The
// default is a client that does not follow redirects; tests and callers can wire
// their own with SetHTTPClient.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// maxBackendBody caps the backend response API Gateway relays (10 MB).
const maxBackendBody = 10 << 20

// pathPlaceholder matches a {name} or {name+} placeholder in an integration URI.
var pathPlaceholder = regexp.MustCompile(`\{([A-Za-z0-9_.:-]+)\+?\}`)

// hopHeaders are never forwarded to an HTTP backend.
//
//nolint:gochecknoglobals // immutable set of hop-by-hop header names
var hopHeaders = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true, "proxy-authorization": true, "te": true,
	"trailer": true, "transfer-encoding": true, "upgrade": true, "host": true, "content-length": true,
}

// SetHTTPClient wires the client HTTP and HTTP_PROXY integrations call out with.
func (m *Mock) SetHTTPClient(c HTTPDoer) { m.httpClient = c }

func defaultHTTPClient() HTTPDoer {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// integrationParams are the path, query and header values an integration's
// request parameters map from the method request.
type integrationParams struct {
	path, query, header map[string]string
}

// mapIntegrationParams evaluates integration.request.{path|querystring|header}.X
// against the method request: a method.request.* source, a stageVariables.* or
// context.* reference, or a quoted literal.
func mapIntegrationParams(req *driver.ProxyRequest, route *resolvedRoute) integrationParams {
	out := integrationParams{path: map[string]string{}, query: map[string]string{}, header: map[string]string{}}

	for dest, src := range route.integration.RequestParameters {
		rest, ok := strings.CutPrefix(dest, "integration.request.")
		if !ok {
			continue
		}

		loc, name, ok := strings.Cut(rest, ".")
		if !ok {
			continue
		}

		v, found := paramSource(src, req, route)
		if !found {
			continue
		}

		switch loc {
		case locPath:
			out.path[name] = v
		case locQuery:
			out.query[name] = v
		case locHeader:
			out.header[name] = v
		}
	}

	return out
}

func paramSource(src string, req *driver.ProxyRequest, route *resolvedRoute) (string, bool) {
	switch {
	case len(src) >= 2 && strings.HasPrefix(src, "'") && strings.HasSuffix(src, "'"):
		return src[1 : len(src)-1], true
	case strings.HasPrefix(src, "stageVariables."):
		v, ok := route.stageVariables[strings.TrimPrefix(src, "stageVariables.")]

		return v, ok
	case strings.HasPrefix(src, "method.request.path."):
		v, ok := route.pathParameters[strings.TrimPrefix(src, "method.request.path.")]

		return v, ok
	case strings.HasPrefix(src, "method.request.querystring."):
		v, ok := req.Query[strings.TrimPrefix(src, "method.request.querystring.")]

		return v, ok
	case strings.HasPrefix(src, "method.request.header."):
		v := headerValue(req.Headers, strings.TrimPrefix(src, "method.request.header."))

		return v, v != ""
	default:
		return "", false
	}
}

func buildBackendURL(route *resolvedRoute, req *driver.ProxyRequest, params integrationParams) (*url.URL, error) {
	uri, missing := expandStageVariables(route.integration.URI, route.stageVariables)
	if missing != "" {
		return nil, fmt.Errorf("%w: stage variable %s is not defined", errBackendConfig, missing)
	}

	proxy := route.integration.Type == driver.IntegrationHTTPProxy

	uri, err := fillPathPlaceholders(uri, route, params, proxy)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(uri)
	if err != nil || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) || u.Host == "" {
		return nil, fmt.Errorf("%w: invalid HTTP endpoint", errBackendConfig)
	}

	q := u.Query()

	if proxy {
		addRequestQuery(q, req)
	}

	for k, v := range params.query {
		q.Set(k, v)
	}

	u.RawQuery = q.Encode()

	return u, nil
}

// fillPathPlaceholders replaces each {name} of the integration URI with its mapped
// (or, for HTTP_PROXY, the request's own) path parameter, escaped.
func fillPathPlaceholders(uri string, route *resolvedRoute, params integrationParams, proxy bool) (string, error) {
	unresolved := ""

	out := pathPlaceholder.ReplaceAllStringFunc(uri, func(ph string) string {
		name := strings.TrimSuffix(strings.Trim(ph, "{}"), "+")
		if v, ok := params.path[name]; ok {
			return escapePathSegment(v)
		}

		if v, ok := route.pathParameters[name]; ok && proxy {
			return escapePathSegment(v)
		}

		unresolved = name

		return ph
	})

	if unresolved != "" {
		return "", fmt.Errorf("%w: path parameter %s is not mapped", errBackendConfig, unresolved)
	}

	return out, nil
}

// addRequestQuery copies the request's whole query string (HTTP_PROXY passes it
// through untouched).
func addRequestQuery(q url.Values, req *driver.ProxyRequest) {
	multi := req.MultiValueQuery
	if len(multi) == 0 {
		multi = make(map[string][]string, len(req.Query))
		for k, v := range req.Query {
			multi[k] = []string{v}
		}
	}

	for k, vals := range multi {
		for _, v := range vals {
			q.Add(k, v)
		}
	}
}

// backendMethod is the HTTP method used toward the backend: the integration's
// own, or the request's when it is ANY or unset.
func backendMethod(route *resolvedRoute, req *driver.ProxyRequest) string {
	method := strings.ToUpper(route.integration.IntegrationHTTPMethod)
	if method == "" || method == driver.MethodANY {
		return req.HTTPMethod
	}

	return method
}

// serveHTTP calls an HTTP or HTTP_PROXY backend. HTTP_PROXY relays the request
// and response untouched; HTTP applies the request and response mapping
// templates and integration responses.
func (m *Mock) serveHTTP(
	ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, reqID string, lg *execLog,
) (*driver.ProxyResponse, time.Duration) {
	params := mapIntegrationParams(req, route)

	target, err := buildBackendURL(route, req, params)
	if err != nil {
		lg.errorf("Execution failed due to configuration error: %s", err.Error())

		return m.gatewayResponse(route, req, reqID, respAPIConfigError, msgInternal), noIntegration
	}

	lg.infof("Endpoint request URI: %s", target.String())

	proxy := route.integration.Type == driver.IntegrationHTTPProxy
	mc := newMappingContext(req, route, m.opts.AccountID, reqID, m.opts.Clock.Now())
	mc.templates = m.templates

	body, rejected := m.backendBody(ctx, req, route, mc, proxy)
	if rejected != nil {
		return rejected, noIntegration
	}

	bctx, cancel := context.WithTimeout(ctx, time.Duration(route.integration.TimeoutInMillis)*time.Millisecond)
	defer cancel()

	out, err := http.NewRequestWithContext(bctx, backendMethod(route, req), target.String(), bytes.NewReader(body))
	if err != nil {
		return m.gatewayResponse(route, req, reqID, respAPIConfigError, msgInternal), noIntegration
	}

	copyBackendHeaders(out.Header, req, params, proxy)

	start := m.opts.Clock.Now()
	resp, err := m.httpClient.Do(out)
	integration := m.opts.Clock.Since(start)

	if err != nil {
		lg.errorf("Execution failed due to an integration error: %s", err.Error())

		return m.backendFailure(bctx, route, req, reqID, err), integration
	}

	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBackendBody))
	if err != nil {
		return m.gatewayResponse(route, req, reqID, respIntegFailure, "Network error communicating with endpoint"), integration
	}

	lg.infof("Endpoint response status code: %d", resp.StatusCode)

	if proxy {
		return relayProxyResponse(resp, data, reqID), integration
	}

	return mapResponse(ctx, mc, route, strconv.Itoa(resp.StatusCode), string(data), firstHeaderValues(resp.Header)), integration
}

// backendBody is the request body sent to the backend: the raw (decoded) body for
// HTTP_PROXY, the request template output for HTTP.
func (*Mock) backendBody(
	ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, mc *mappingContext, proxy bool,
) ([]byte, *driver.ProxyResponse) {
	if proxy {
		if req.IsBase64Encoded {
			if b, err := base64.StdEncoding.DecodeString(req.Body); err == nil {
				return b, nil
			}
		}

		return []byte(req.Body), nil
	}

	payload, _, rejected := mapRequest(ctx, mc, &route.integration, req)
	if rejected != nil {
		return nil, rejected
	}

	return []byte(payload), nil
}

func copyBackendHeaders(dst http.Header, req *driver.ProxyRequest, params integrationParams, proxy bool) {
	if proxy {
		for k, v := range req.Headers {
			if !hopHeaders[strings.ToLower(k)] {
				dst.Set(k, v)
			}
		}
	} else if ct := headerValue(req.Headers, headerContentType); ct != "" {
		dst.Set(headerContentType, ct)
	}

	for k, v := range params.header {
		dst.Set(k, v)
	}

	dst.Set("X-Amzn-Trace-Id", "Root=1-"+idgen.UUID())
}

// backendFailure maps a transport error to API Gateway's 504 responses.
func (m *Mock) backendFailure(
	bctx context.Context, route *resolvedRoute, req *driver.ProxyRequest, reqID string, err error,
) *driver.ProxyResponse {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(bctx.Err(), context.DeadlineExceeded) {
		return m.gatewayResponse(route, req, reqID, respIntegTimeout, "Endpoint request timed out")
	}

	return m.gatewayResponse(route, req, reqID, respIntegFailure, "Network error communicating with endpoint")
}

func relayProxyResponse(resp *http.Response, data []byte, reqID string) *driver.ProxyResponse {
	out := &driver.ProxyResponse{
		StatusCode: resp.StatusCode, Headers: map[string]string{headerRequestID: reqID}, Body: string(data),
		MultiValueHeaders: map[string][]string{},
	}

	for k, vals := range resp.Header {
		if hopHeaders[strings.ToLower(k)] {
			continue
		}

		out.MultiValueHeaders[k] = append([]string(nil), vals...)
	}

	return out
}

func firstHeaderValues(h http.Header) map[string]string {
	out := make(map[string]string, len(h))

	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}

	return out
}
