package lambda

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// Function URL invoke constants: the payload format version and static
// requestContext fields every invoke event carries. Real Lambda supports only
// payload format 2.0 for Function URLs (the same envelope shape as an HTTP API
// v2 integration), always routed through a single "$default" route/stage.
const (
	functionURLPayloadVersion  = "2.0"
	functionURLDefaultRoute    = "$default"
	functionURLAnonymousCaller = "anonymous" // AuthType is parsed, never verified — see functionurl.go.
)

// functionURLTimeFormat is the requestContext.time format real API Gateway /
// Function URL events use: a Common Log Format-style timestamp.
const functionURLTimeFormat = "02/Jan/2006:15:04:05 +0000"

// functionURLRequestEvent is the Lambda Function URL invoke event, payload
// format version 2.0 (the same shape API Gateway HTTP APIs send):
// https://docs.aws.amazon.com/lambda/latest/dg/urls-invocation.html
type functionURLRequestEvent struct {
	Version               string                    `json:"version"`
	RouteKey              string                    `json:"routeKey"`
	RawPath               string                    `json:"rawPath"`
	RawQueryString        string                    `json:"rawQueryString"`
	Cookies               []string                  `json:"cookies,omitempty"`
	Headers               map[string]string         `json:"headers"`
	QueryStringParameters map[string]string         `json:"queryStringParameters,omitempty"`
	RequestContext        functionURLRequestContext `json:"requestContext"`
	Body                  string                    `json:"body,omitempty"`
	IsBase64Encoded       bool                      `json:"isBase64Encoded"`
}

// functionURLRequestContext is requestContext in functionURLRequestEvent.
type functionURLRequestContext struct {
	AccountID    string                 `json:"accountId"`
	APIID        string                 `json:"apiId"`
	DomainName   string                 `json:"domainName"`
	DomainPrefix string                 `json:"domainPrefix"`
	HTTP         functionURLRequestHTTP `json:"http"`
	RequestID    string                 `json:"requestId"`
	RouteKey     string                 `json:"routeKey"`
	Stage        string                 `json:"stage"`
	Time         string                 `json:"time"`
	TimeEpoch    int64                  `json:"timeEpoch"`
}

// functionURLRequestHTTP is requestContext.http in functionURLRequestEvent.
type functionURLRequestHTTP struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	Protocol  string `json:"protocol"`
	SourceIP  string `json:"sourceIp"`
	UserAgent string `json:"userAgent"`
}

// functionURLInvokeResponse is the shape a Function URL handler returns to
// control the HTTP response. Real Lambda flips into this structured
// translation only when the returned JSON object carries a "statusCode" key;
// StatusCode is a pointer so its presence/absence in the payload is
// distinguishable from a zero value.
type functionURLInvokeResponse struct {
	StatusCode      *int              `json:"statusCode"`
	Headers         map[string]string `json:"headers,omitempty"`
	Cookies         []string          `json:"cookies,omitempty"`
	Body            string            `json:"body"`
	IsBase64Encoded bool              `json:"isBase64Encoded"`
}

// serveFunctionURLInvoke handles a request addressed to a generated Function
// URL host (<url-id>.lambda-url.<region>.on.aws), translating it into a
// payload-format-2.0 invoke event and funneling it through the same
// h.fn.Invoke choke point the control-plane /invocations path uses.
func (h *Handler) serveFunctionURLInvoke(w http.ResponseWriter, r *http.Request) {
	resolver, ok := h.fn.(functionURLResolver)
	if !ok {
		http.Error(w, "function urls not supported", http.StatusNotImplemented)
		return
	}

	host := requestHost(r.Host)

	cfg, err := resolver.ResolveFunctionURL(r.Context(), host)
	if err != nil {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "no function is configured for this url")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "RequestEntityTooLargeException", err.Error())
		return
	}

	payload, err := json.Marshal(buildFunctionURLEvent(r, host, body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ServiceException", err.Error())
		return
	}

	out, err := h.fn.Invoke(r.Context(), sdrv.InvokeInput{
		FunctionName: cfg.FunctionName,
		Qualifier:    cfg.Qualifier,
		Payload:      payload,
		InvokeType:   "RequestResponse",
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeFunctionURLInvokeResponse(w, out)
}

// buildFunctionURLEvent translates an inbound HTTP request into the payload-
// format-2.0 Function URL invoke event.
func buildFunctionURLEvent(r *http.Request, host string, body []byte) functionURLRequestEvent {
	headers, cookies := extractCookies(canonicalHeaders(r.Header))
	bodyStr, isBase64 := encodeFunctionURLBody(body)
	urlID, _, _ := strings.Cut(host, functionURLHostMarker)
	now := time.Now().UTC()
	path := r.URL.EscapedPath()

	return functionURLRequestEvent{
		Version:               functionURLPayloadVersion,
		RouteKey:              functionURLDefaultRoute,
		RawPath:               path,
		RawQueryString:        r.URL.RawQuery,
		Cookies:               cookies,
		Headers:               headers,
		QueryStringParameters: queryStringParameters(r.URL.Query()),
		RequestContext: functionURLRequestContext{
			AccountID:    functionURLAnonymousCaller,
			APIID:        urlID,
			DomainName:   host,
			DomainPrefix: urlID,
			HTTP: functionURLRequestHTTP{
				Method:    r.Method,
				Path:      path,
				Protocol:  r.Proto,
				SourceIP:  sourceIP(r.RemoteAddr),
				UserAgent: r.Header.Get("User-Agent"),
			},
			RequestID: newFunctionURLRequestID(),
			RouteKey:  functionURLDefaultRoute,
			Stage:     functionURLDefaultRoute,
			Time:      now.Format(functionURLTimeFormat),
			TimeEpoch: now.UnixMilli(),
		},
		Body:            bodyStr,
		IsBase64Encoded: isBase64,
	}
}

// canonicalHeaders lower-cases header names and comma-joins multi-value
// headers, matching the payload-2.0 headers map real Lambda sends.
func canonicalHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		out[strings.ToLower(k)] = strings.Join(vals, ",")
	}

	return out
}

// extractCookies pulls the Cookie header (if any) out of headers and splits it
// into the top-level cookies array, matching payload-2.0's convention of
// surfacing cookies separately rather than as a raw header.
func extractCookies(headers map[string]string) (remaining map[string]string, cookies []string) {
	raw, ok := headers["cookie"]
	if !ok {
		return headers, nil
	}

	delete(headers, "cookie")

	for _, part := range strings.Split(raw, "; ") {
		if part != "" {
			cookies = append(cookies, part)
		}
	}

	return headers, cookies
}

// queryStringParameters comma-joins multi-value query parameters into the
// single-valued map payload-2.0 uses (unlike the multi-value maps of payload
// format 1.0).
func queryStringParameters(q url.Values) map[string]string {
	if len(q) == 0 {
		return nil
	}

	out := make(map[string]string, len(q))
	for k, vals := range q {
		out[k] = strings.Join(vals, ",")
	}

	return out
}

// sourceIP extracts the client IP from RemoteAddr, dropping the port.
func sourceIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}

	return host
}

// encodeFunctionURLBody returns the request body as a UTF-8 string, or
// base64-encoded with isBase64Encoded=true when it isn't valid UTF-8 (binary
// payloads), matching real Lambda's encoding rule for Function URL events.
func encodeFunctionURLBody(body []byte) (string, bool) {
	if len(body) == 0 {
		return "", false
	}

	if utf8.Valid(body) {
		return string(body), false
	}

	return base64.StdEncoding.EncodeToString(body), true
}

// newFunctionURLRequestID generates a random hex request id for the invoke
// event's requestContext, standing in for the invocation's real AWS request id.
func newFunctionURLRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000000000000000000000000"
	}

	return hex.EncodeToString(b[:])
}

// writeFunctionURLInvokeResponse translates the handler's InvokeOutput into
// the HTTP response a real Function URL sends. A handler error becomes a 502
// with the fixed body real Lambda returns for a Function URL invoke — unlike
// the raw Invoke API, which reports a handler error via the
// X-Amz-Function-Error header on an HTTP 200. A response payload with a
// "statusCode" key is translated per the structured
// {statusCode,headers,cookies,body,isBase64Encoded} contract; any other
// payload (no such key present) is returned as-is with a 200, matching real
// Function URL BUFFERED behavior for a handler that returns a bare value.
func writeFunctionURLInvokeResponse(w http.ResponseWriter, out *sdrv.InvokeOutput) {
	if out.Error != "" {
		w.Header().Set("Content-Type", contentTypeJSON)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"Internal Server Error"}`))

		return
	}

	var resp functionURLInvokeResponse
	if err := json.Unmarshal(out.Payload, &resp); err != nil || resp.StatusCode == nil {
		w.Header().Set("Content-Type", contentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out.Payload)

		return
	}

	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}

	for _, c := range resp.Cookies {
		w.Header().Add("Set-Cookie", c)
	}

	body := []byte(resp.Body)

	if resp.IsBase64Encoded {
		if decoded, err := base64.StdEncoding.DecodeString(resp.Body); err == nil {
			body = decoded
		}
	}

	w.WriteHeader(*resp.StatusCode)
	_, _ = w.Write(body)
}
