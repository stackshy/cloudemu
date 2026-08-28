package apigateway

import (
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// serveHostDataPlane handles a request to "{apiId}.execute-api.<...>" whose path
// is /{stage}/{resourcePath}.
func (h *Handler) serveHostDataPlane(w http.ResponseWriter, r *http.Request) {
	apiID := strings.Split(r.Host, executeAPIMarker)[0]

	stage, resourcePath, ok := splitStagePath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusForbidden, "ForbiddenException", "Missing Authentication Token")
		return
	}

	h.route(w, r, apiID, stage, resourcePath)
}

// servePathDataPlane handles /restapis/{apiId}/{stage}/_user_request_/{resourcePath}.
func (h *Handler) servePathDataPlane(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, controlPrefix), "/")
	segs := strings.Split(rest, "/")

	// segs: {apiId}, {stage}, _user_request_, [resourcePath...]
	const minSegs = 3
	if len(segs) < minSegs || segs[2] != userRequestMark {
		writeError(w, http.StatusForbidden, "ForbiddenException", "Missing Authentication Token")
		return
	}

	resourcePath := "/" + strings.Join(segs[minSegs:], "/")

	h.route(w, r, segs[0], segs[1], resourcePath)
}

// route builds the proxy request, invokes the integration, and writes the mapped
// response.
func (h *Handler) route(w http.ResponseWriter, r *http.Request, apiID, stage, resourcePath string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "RequestEntityTooLargeException", err.Error())
		return
	}

	req := &driver.ProxyRequest{
		RestAPIID:         apiID,
		StageName:         stage,
		HTTPMethod:        r.Method,
		Path:              normalizePath(resourcePath),
		Headers:           firstValues(r.Header),
		MultiValueHeaders: map[string][]string(r.Header),
		Query:             firstValues(r.URL.Query()),
		MultiValueQuery:   map[string][]string(r.URL.Query()),
		Body:              string(body),
		SourceIP:          clientIP(r.RemoteAddr),
		Host:              r.Host,
		Protocol:          r.Proto,
	}

	resp, err := h.ag.InvokeRoute(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "ApiGatewayException", err.Error())
		return
	}

	writeProxyResponse(w, resp)
}

// writeProxyResponse writes a ProxyResponse as the HTTP response.
func writeProxyResponse(w http.ResponseWriter, resp *driver.ProxyResponse) {
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}

	for k, vals := range resp.MultiValueHeaders {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}

	w.WriteHeader(status)
	_, _ = io.WriteString(w, resp.Body)
}

// splitStagePath splits "/{stage}/{resourcePath}" into the stage and the
// resource path (leading "/"). A path with no stage segment is rejected.
func splitStagePath(path string) (stage, resourcePath string, ok bool) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "", "", false
	}

	const stageAndRest = 2

	segs := strings.SplitN(trimmed, "/", stageAndRest)
	stage = segs[0]

	if len(segs) == stageAndRest {
		return stage, "/" + segs[1], true
	}

	return stage, "/", true
}

// normalizePath ensures a leading "/" and collapses an empty path to "/".
func normalizePath(p string) string {
	if p == "" {
		return "/"
	}

	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}

	return p
}

// firstValues collapses a multi-value header/query map to its first value each,
// the single-value view the proxy event's headers/queryStringParameters use.
func firstValues(m map[string][]string) map[string]string {
	if len(m) == 0 {
		return nil
	}

	out := make(map[string]string, len(m))

	for k, v := range m {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}

	return out
}

// clientIP extracts the host portion of a RemoteAddr ("ip:port"), falling back
// to the raw value when it has no port.
func clientIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}

	return remoteAddr
}
