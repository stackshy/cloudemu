package apigateway

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Per-segment match scores, so a literal path beats a "{param}" placeholder,
// which in turn beats a "{proxy+}" greedy catch-all (AWS route precedence).
const (
	scoreLiteral = 3
	scoreParam   = 2
	scoreProxy   = 1
)

// routeMatch is the resolved resource+method for a data-plane request.
type routeMatch struct {
	resource       *driver.Resource
	method         *driver.Method
	pathParameters map[string]string
}

// matchRoute resolves httpMethod+path against the resource tree, honoring
// literal > {param} > {proxy+} precedence, and returns the best match plus its
// captured path parameters. It only matches a resource that has a usable method
// (the exact method or the "ANY" catch-all).
func matchRoute(resources map[string]*driver.Resource, httpMethod, path string) (routeMatch, bool) {
	reqSegs := splitPath(path)

	var (
		best      routeMatch
		bestScore = -1
		found     bool
	)

	for _, r := range resources {
		params, score, ok := matchTemplate(splitPath(r.Path), reqSegs)
		if !ok {
			continue
		}

		mth := pickMethod(r, httpMethod)
		if mth == nil {
			continue
		}

		if score > bestScore {
			best = routeMatch{resource: r, method: mth, pathParameters: params}
			bestScore = score
			found = true
		}
	}

	return best, found
}

// pickMethod returns the resource's method for httpMethod, falling back to the
// "ANY" catch-all, or nil when neither is configured.
func pickMethod(r *driver.Resource, httpMethod string) *driver.Method {
	if mth, ok := r.Methods[normalizeMethod(httpMethod)]; ok {
		return mth
	}

	if mth, ok := r.Methods[driver.MethodANY]; ok {
		return mth
	}

	return nil
}

// matchTemplate matches a resource path template's segments against the request
// segments, returning captured parameters and a specificity score. A "{proxy+}"
// segment greedily captures one or more trailing request segments.
func matchTemplate(tmpl, req []string) (params map[string]string, score int, ok bool) {
	params = map[string]string{}

	for i, seg := range tmpl {
		switch {
		case isGreedy(seg):
			if i >= len(req) {
				return nil, 0, false // {proxy+} needs at least one trailing segment
			}

			params[greedyName(seg)] = strings.Join(req[i:], "/")

			return params, score + scoreProxy, true
		case isParam(seg):
			if i >= len(req) {
				return nil, 0, false
			}

			params[paramName(seg)] = req[i]
			score += scoreParam
		default:
			if i >= len(req) || req[i] != seg {
				return nil, 0, false
			}

			score += scoreLiteral
		}
	}

	// A non-greedy template matches only when it consumed every request segment.
	if len(tmpl) != len(req) {
		return nil, 0, false
	}

	return params, score, true
}

// splitPath splits a "/a/b" path into ["a","b"], dropping empty segments so the
// root ("/" or "") yields an empty slice.
func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "/")
}

func isParam(seg string) bool {
	return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
}

func isGreedy(seg string) bool {
	return isParam(seg) && strings.HasSuffix(seg, "+}")
}

// paramName returns the capture name of a "{name}" segment.
func paramName(seg string) string {
	return strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
}

// greedyName returns the capture name of a "{name+}" segment (e.g. "proxy").
func greedyName(seg string) string {
	return strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}"), "+")
}
