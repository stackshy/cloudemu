package apigateway

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/stackshy/cloudemu/v2/internal/jsonpath"
	"github.com/stackshy/cloudemu/v2/internal/vtl"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// templateTimeout bounds one mapping-template render.
const templateTimeout = 2 * time.Second

// $input.params() location keys and the $input.body property.
const (
	locPath   = "path"
	locQuery  = "querystring"
	locHeader = "header"
	propBody  = "body"
)

// requestTimeLayout is $context.requestTime's CLF format.
const requestTimeLayout = "02/Jan/2006:15:04:05 -0700"

// mappingContext is the per-request state the $input, $context,
// $stageVariables and $util variables are built from.
type mappingContext struct {
	req     *driver.ProxyRequest
	route   *resolvedRoute
	account string
	reqID   string
	now     time.Time
	// context is the $context map. It is shared by the request and response
	// templates, so $context.responseOverride set in either survives.
	context *vtl.Map
	// templates caches parsed templates; nil parses on every render.
	templates *vtl.Cache
}

func newMappingContext(req *driver.ProxyRequest, route *resolvedRoute, account, reqID string, now time.Time) *mappingContext {
	mc := &mappingContext{req: req, route: route, account: account, reqID: reqID, now: now}
	mc.context = mc.buildContext()

	return mc
}

func (mc *mappingContext) buildContext() *vtl.Map {
	identity := vtl.NewMap()
	identity.Put("sourceIp", mc.req.SourceIP)
	identity.Put("userAgent", headerValue(mc.req.Headers, "User-Agent"))

	override := vtl.NewMap()
	override.Put(locHeader, vtl.NewMap())

	ctx := vtl.NewMap()
	for _, kv := range [][2]string{
		{"accountId", mc.account},
		{"apiId", mc.route.apiID},
		{"domainName", mc.req.Host},
		{"domainPrefix", strings.SplitN(mc.req.Host, ".", 2)[0]}, //nolint:mnd // first DNS label
		{"extendedRequestId", mc.reqID},
		{"httpMethod", mc.req.HTTPMethod},
		{locPath, "/" + mc.req.StageName + mc.req.Path},
		{"protocol", orDefault(mc.req.Protocol, "HTTP/1.1")},
		{"requestId", mc.reqID},
		{"requestTime", mc.now.UTC().Format(requestTimeLayout)},
		{"resourceId", mc.route.resourceID},
		{"resourcePath", mc.route.resourcePath},
		{"stage", mc.req.StageName},
	} {
		ctx.Put(kv[0], kv[1])
	}

	ctx.Put("requestTimeEpoch", mc.now.UnixMilli())
	ctx.Put("identity", identity)
	ctx.Put("responseOverride", override)

	return ctx
}

// render evaluates a mapping template with body as $input's payload. Parsing
// (cached per template source) and rendering share one deadline.
func (mc *mappingContext) render(ctx context.Context, src, body string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, templateTimeout)
	defer cancel()

	parse := vtl.Parse
	if mc.templates != nil {
		parse = mc.templates.Parse
	}

	tmpl, err := parse(src)
	if err != nil {
		return "", err
	}

	vars := map[string]any{
		"input":          &inputObject{ctx: ctx, body: body, params: mc.params()},
		"context":        mc.context,
		"stageVariables": vtl.StringMap(mc.route.stageVariables),
		"util":           utilObject{},
	}

	res, err := tmpl.Render(ctx, vars, vtl.RenderOptions{})
	if err != nil {
		return "", err
	}

	return res.Output, nil
}

// params is $input.params(): the request's path, querystring and header maps.
func (mc *mappingContext) params() *vtl.Map {
	p := vtl.NewMap()
	p.Put(locPath, vtl.StringMap(mc.route.pathParameters))
	p.Put(locQuery, vtl.StringMap(mc.req.Query))
	p.Put(locHeader, vtl.StringMap(mc.req.Headers))

	return p
}

// responseOverride returns the status and headers a template set through
// $context.responseOverride.
func (mc *mappingContext) responseOverride() (status int, headers map[string]string) {
	ov, _ := mc.context.Get("responseOverride")

	om, ok := ov.(*vtl.Map)
	if !ok {
		return 0, nil
	}

	if s, ok := om.Get("status"); ok {
		switch v := s.(type) {
		case int64:
			status = int(v)
		case string:
			_, _ = fmt.Sscanf(v, "%d", &status)
		}
	}

	if hv, ok := om.Get(locHeader); ok {
		if hm, ok := hv.(*vtl.Map); ok && hm.Len() > 0 {
			headers = map[string]string{}

			for _, k := range hm.Keys() {
				v, _ := hm.Get(k)
				headers[k] = vtl.Stringify(v)
			}
		}
	}

	return status, headers
}

// inputObject is $input.
type inputObject struct {
	// ctx is the render's deadline, which bounds JSONPath walks.
	ctx    context.Context
	body   string
	params *vtl.Map
	parsed any
	done   bool
}

func (in *inputObject) Get(name string) (any, bool) {
	if name == propBody {
		return in.body, true
	}

	return nil, false
}

func (in *inputObject) Call(name string, args []any) (res any, found bool, callErr error) {
	switch name {
	case locPath:
		v, err := in.path(stringArg(args))

		return v, true, err
	case "json":
		v, err := in.path(stringArg(args))
		if err != nil {
			return nil, true, err
		}

		s, err := vtl.ToJSON(v)

		return s, true, err
	case "params":
		if len(args) == 0 {
			return in.params, true, nil
		}

		return in.param(stringArg(args)), true, nil
	case propBody:
		return in.body, true, nil
	}

	return nil, false, nil
}

// path evaluates a JSONPath against the JSON body. An empty body is treated as
// an empty object, as API Gateway does. A path with a wildcard or recursive
// descent returns the list of matches.
func (in *inputObject) path(p string) (any, error) {
	if !in.done {
		in.done = true

		if strings.TrimSpace(in.body) == "" {
			in.parsed = vtl.NewMap()
		} else if v, err := vtl.ParseJSON(in.body); err == nil {
			in.parsed = v
		} else {
			in.parsed = in.body
		}
	}

	matches, indefinite, err := jsonpath.EvalAll(in.ctx, p, in.parsed)
	if err != nil {
		return nil, err
	}

	if indefinite {
		return vtl.NewList(matches...), nil
	}

	if len(matches) == 0 {
		return nil, nil
	}

	return matches[0], nil
}

// param looks a name up in the path, querystring and header maps, in that
// order, and returns an empty string when it is absent.
func (in *inputObject) param(name string) any {
	for _, loc := range []string{locPath, locQuery, locHeader} {
		m, _ := in.params.Get(loc)

		lm, ok := m.(*vtl.Map)
		if !ok {
			continue
		}

		if loc == locHeader {
			for _, k := range lm.Keys() {
				if strings.EqualFold(k, name) {
					v, _ := lm.Get(k)

					return v
				}
			}

			continue
		}

		if v, ok := lm.Get(name); ok {
			return v
		}
	}

	return ""
}

// utilObject is $util.
type utilObject struct{}

func (utilObject) Get(string) (any, bool) { return nil, false }

func (utilObject) Call(name string, args []any) (res any, found bool, callErr error) {
	s := stringArg(args)

	switch name {
	case "escapeJavaScript":
		return escapeJavaScript(s), true, nil
	case "parseJson":
		v, err := vtl.ParseJSON(s)
		if err != nil {
			return nil, true, fmt.Errorf("$util.parseJson: %w", err)
		}

		return v, true, nil
	case "urlEncode":
		return javaURLEncode(s), true, nil
	case "urlDecode":
		v, err := url.QueryUnescape(s)
		if err != nil {
			return nil, true, fmt.Errorf("$util.urlDecode: %w", err)
		}

		return v, true, nil
	case "base64Encode":
		return base64.StdEncoding.EncodeToString([]byte(s)), true, nil
	case "base64Decode":
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, true, fmt.Errorf("$util.base64Decode: %w", err)
		}

		return string(b), true, nil
	}

	return nil, false, nil
}

func stringArg(args []any) string {
	if len(args) == 0 {
		return ""
	}

	return vtl.Stringify(args[0])
}

// javaURLEncode matches Java's URLEncoder.encode with UTF-8, which
// $util.urlEncode uses: unlike Go's QueryEscape it leaves '*' alone and
// encodes '~'.
func javaURLEncode(s string) string {
	return strings.NewReplacer("%2A", "*", "~", "%7E").Replace(url.QueryEscape(s))
}

// escapeJavaScript matches Apache Commons StringEscapeUtils.escapeJavaScript,
// which $util.escapeJavaScript uses: quotes, backslash and '/' are escaped,
// control characters use their short or \uXXXX form, and non-ASCII characters
// become \uXXXX.
func escapeJavaScript(s string) string {
	var b strings.Builder

	for _, r := range s {
		switch r {
		case '"', '\'', '\\', '/':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			writeEscapedRune(&b, r)
		}
	}

	return b.String()
}

func writeEscapedRune(b *strings.Builder, r rune) {
	const (
		firstPrintable = 0x20
		lastASCII      = 0x7f
	)

	if r >= firstPrintable && r <= lastASCII {
		b.WriteRune(r)

		return
	}

	if hi, lo := utf16.EncodeRune(r); hi != unicode.ReplacementChar {
		fmt.Fprintf(b, `\u%04X\u%04X`, hi, lo)

		return
	}

	fmt.Fprintf(b, `\u%04X`, r)
}

// headerValue looks a header up case-insensitively.
func headerValue(headers map[string]string, name string) string {
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}

	return ""
}
