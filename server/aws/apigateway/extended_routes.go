package apigateway

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Top-level (account scoped) path prefixes of the optional capabilities.
const (
	apiKeysPrefix     = "/apikeys"
	usagePlansPrefix  = "/usageplans"
	domainNamesPrefix = "/domainnames"
	vpcLinksPrefix    = "/vpclinks"
)

// Path segments of the usage plan and domain name sub-resources.
const (
	segKeys         = "keys"
	segBasePathMaps = "basepathmappings"
)

// Sub-resource segments under /restapis/{id} owned by optional capabilities.
const (
	subAuthorizers = "authorizers"
	subModels      = "models"
	subValidators  = "requestvalidators"
	subGwResponses = "gatewayresponses"
	subExports     = "exports"
)

// ownsAccountPath reports whether p belongs to an account-scoped resource family.
func ownsAccountPath(p string) bool {
	for _, prefix := range []string{apiKeysPrefix, usagePlansPrefix, domainNamesPrefix, vpcLinksPrefix} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}

	return false
}

// serveAccountResource routes /apikeys, /usageplans, /domainnames and /vpclinks.
func (h *Handler) serveAccountResource(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	segs := strings.Split(strings.Trim(p, "/"), "/")

	switch segs[0] {
	case "apikeys":
		h.serveAPIKeys(w, r, segs[1:])
	case "usageplans":
		h.serveUsagePlans(w, r, segs[1:])
	case "domainnames":
		h.serveDomainNames(w, r, segs[1:])
	default:
		h.serveVpcLinks(w, r, segs[1:])
	}
}

func notImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "NotFoundException", "unsupported API Gateway path")
}

// serveListOf writes a paged list: GET collection of a family.
func serveListOf[T, R any](
	w http.ResponseWriter, r *http.Request,
	list func(page driver.PageInput) (items []T, position string, err error), render func(*T) R,
) {
	page, ok := pageInput(w, r)
	if !ok {
		return
	}

	items, position, err := list(page)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listResponse[R]{Position: position, Item: make([]R, 0, len(items))}
	for i := range items {
		out.Item = append(out.Item, render(&items[i]))
	}

	writeJSON(w, http.StatusOK, out)
}

// serveCreateOf decodes the request body and creates one resource (201).
func serveCreateOf[Req, T, R any](
	w http.ResponseWriter, r *http.Request, create func(req *Req) (*T, error), render func(*T) R,
) {
	var req Req
	if !decodeJSON(w, r, &req) {
		return
	}

	v, err := create(&req)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, render(v))
}

// queryBool reads a boolean query parameter.
func queryBool(r *http.Request, name string) bool {
	b, _ := strconv.ParseBool(r.URL.Query().Get(name))

	return b
}

// serveAPIChild routes the optional per-API children: authorizers, models,
// request validators and gateway responses. It reports whether sub is one.
func (h *Handler) serveAPIChild(w http.ResponseWriter, r *http.Request, id, sub, item string) bool {
	switch sub {
	case subAuthorizers:
		h.serveAuthorizers(w, r, id, item)
	case subModels:
		h.serveModels(w, r, id, item)
	case subValidators:
		h.serveValidators(w, r, id, item)
	case subGwResponses:
		h.serveGatewayResponses(w, r, id, item)
	default:
		return false
	}

	return true
}

// serveRestAPIImport handles POST /restapis?mode=import (ImportRestApi) and
// PUT /restapis/{id}?mode=merge|overwrite (PutRestApi).
func (h *Handler) serveRestAPIImport(w http.ResponseWriter, r *http.Request, id string) {
	oa, ok := h.ag.(driver.OpenAPI)
	if !ok {
		notImplemented(w)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "BadRequestException", err.Error())
		return
	}

	q := r.URL.Query()
	params := map[string]string{}

	for k, v := range q {
		if strings.HasPrefix(k, "parameters.") && len(v) > 0 {
			params[strings.TrimPrefix(k, "parameters.")] = v[0]
		}
	}

	failOnWarnings := queryBool(r, "failonwarnings")

	var res *driver.ImportResult

	if id == "" {
		res, err = oa.ImportRestAPI(r.Context(), &driver.ImportRestAPIInput{Body: body, FailOnWarnings: failOnWarnings, Parameters: params})
	} else {
		res, err = oa.PutRestAPI(r.Context(), id, &driver.PutRestAPIInput{
			Mode: q.Get("mode"), Body: body, FailOnWarnings: failOnWarnings, Parameters: params,
		})
	}

	if err != nil {
		writeErr(w, err)
		return
	}

	out := toRestAPIResponse(res.API)
	status := http.StatusCreated

	if id != "" {
		status = http.StatusOK
	}

	writeJSON(w, status, struct {
		restAPIResponse
		Warnings []string `json:"warnings,omitempty"`
	}{out, res.Warnings})
}

// serveExport handles GET /restapis/{id}/stages/{stage}/exports/{type}.
func (h *Handler) serveExport(w http.ResponseWriter, r *http.Request, segs []string) {
	oa, ok := h.ag.(driver.OpenAPI)
	if !ok || r.Method != http.MethodGet {
		notImplemented(w)
		return
	}

	q := r.URL.Query()

	exp, err := oa.GetExport(r.Context(), &driver.GetExportInput{
		RestAPIID: segs[0], StageName: segs[2], ExportType: segs[4],
		Accept: r.Header.Get("Accept"), Extensions: strings.Split(q.Get("extensions"), ","),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("Content-Type", exp.ContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(exp.Body)
}

// domainTarget is the API stage a custom domain request resolves to.
type domainTarget struct {
	apiID, stage, rest string
}

// resolveDomain maps a request to a registered custom domain to its target.
func (h *Handler) resolveDomain(r *http.Request) (domainTarget, bool) {
	dr, ok := h.ag.(driver.DomainResolver)
	if !ok || r.Host == "" {
		return domainTarget{}, false
	}

	host := r.Host
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}

	apiID, stage, rest, found := dr.ResolveDomain(host, r.URL.Path)

	return domainTarget{apiID: apiID, stage: stage, rest: rest}, found
}

// isCustomDomain reports whether the request is addressed to a registered custom
// domain name (its Host), which serves the API its base path mapping names.
func (h *Handler) isCustomDomain(r *http.Request) bool {
	_, ok := h.resolveDomain(r)

	return ok
}

// serveCustomDomain routes a request to a custom domain to the API stage its
// base path mapping names.
func (h *Handler) serveCustomDomain(w http.ResponseWriter, r *http.Request) {
	t, ok := h.resolveDomain(r)
	if !ok {
		writeError(w, http.StatusForbidden, "ForbiddenException", "Missing Authentication Token")
		return
	}

	h.route(w, r, t.apiID, t.stage, t.rest)
}
