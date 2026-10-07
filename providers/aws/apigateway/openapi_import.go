package apigateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

var _ driver.OpenAPI = (*Mock)(nil)

const (
	msgImportBody    = "Failed to parse the API definition: it must be an OpenAPI 2.0 or 3.0 document in JSON or YAML"
	msgImportTitle   = "Unable to create the API: the definition has no info.title"
	maxImportBytes   = 6 << 20
	extIntegration   = "x-amazon-apigateway-integration"
	extAnyMethod     = "x-amazon-apigateway-any-method"
	extAuthorizer    = "x-amazon-apigateway-authorizer"
	extValidators    = "x-amazon-apigateway-request-validators"
	extValidator     = "x-amazon-apigateway-request-validator"
	extGatewayResp   = "x-amazon-apigateway-gateway-responses"
	extBinaryMedia   = "x-amazon-apigateway-binary-media-types"
	extKeySource     = "x-amazon-apigateway-api-key-source"
	defaultSelection = "default"
)

// openAPIMethods are the path item keys that define an operation.
//
//nolint:gochecknoglobals // immutable list of the OpenAPI operation keys
var openAPIMethods = []string{"get", "put", "post", "delete", "options", "head", "patch"}

// importer carries the state of one import into an API.
type importer struct {
	m          *Mock
	ctx        context.Context
	apiID      string
	doc        map[string]any
	warnings   []string
	authorizer map[string]string // security scheme name -> authorizer id
	validators map[string]string // validator name -> id
	resources  map[string]string // resource path -> id
	overwrite  bool
}

// parseOpenAPI decodes a JSON or YAML OpenAPI document.
func parseOpenAPI(body []byte) (map[string]any, error) {
	if len(body) == 0 || len(body) > maxImportBytes {
		return nil, cerrors.New(cerrors.InvalidArgument, msgImportBody)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil || doc == nil {
		return nil, cerrors.New(cerrors.InvalidArgument, msgImportBody)
	}

	_, swagger := doc["swagger"]
	_, oas3 := doc["openapi"]

	if !swagger && !oas3 {
		return nil, cerrors.New(cerrors.InvalidArgument, msgImportBody)
	}

	return doc, nil
}

// ImportRestAPI creates a REST API from an OpenAPI 2.0 or 3.0 definition.
func (m *Mock) ImportRestAPI(ctx context.Context, in *driver.ImportRestAPIInput) (*driver.ImportResult, error) {
	doc, err := parseOpenAPI(in.Body)
	if err != nil {
		return nil, err
	}

	info, _ := doc["info"].(map[string]any)

	title, _ := info["title"].(string)
	if title == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, msgImportTitle)
	}

	create := &driver.CreateRestAPIInput{Name: title, Description: strVal(info["description"]), Version: strVal(info["version"])}
	create.BinaryMediaTypes = stringList(doc[extBinaryMedia])
	create.APIKeySource = strVal(doc[extKeySource])

	if t := in.Parameters["endpointConfigurationTypes"]; t != "" {
		create.EndpointConfigurationTypes = []string{t}
	}

	api, err := m.CreateRestAPI(ctx, create)
	if err != nil {
		return nil, err
	}

	imp := &importer{m: m, ctx: ctx, apiID: api.ID, doc: doc, authorizer: map[string]string{}, validators: map[string]string{}}

	if runErr := imp.run(); runErr != nil {
		_ = m.DeleteRestAPI(ctx, api.ID)

		return nil, runErr
	}

	if in.FailOnWarnings && len(imp.warnings) > 0 {
		_ = m.DeleteRestAPI(ctx, api.ID)

		return nil, cerrors.Newf(cerrors.InvalidArgument, "Warnings found during import: %s", strings.Join(imp.warnings, "; "))
	}

	out, err := m.GetRestAPI(ctx, api.ID)
	if err != nil {
		return nil, err
	}

	return &driver.ImportResult{API: out, Warnings: imp.warnings}, nil
}

// PutRestAPI updates an existing API from a definition: overwrite replaces every
// resource, model, authorizer, validator and gateway response; merge keeps what
// the definition does not mention and replaces what it does.
func (m *Mock) PutRestAPI(ctx context.Context, restAPIID string, in *driver.PutRestAPIInput) (*driver.ImportResult, error) {
	mode := orDefault(in.Mode, driver.ImportMerge)
	if mode != driver.ImportMerge && mode != driver.ImportOverwrite {
		return nil, cerrors.New(cerrors.InvalidArgument, "mode must be merge or overwrite")
	}

	doc, err := parseOpenAPI(in.Body)
	if err != nil {
		return nil, err
	}

	if _, getErr := m.getAPI(restAPIID); getErr != nil {
		return nil, getErr
	}

	if mode == driver.ImportOverwrite {
		m.clearAPIContent(restAPIID)
	}

	imp := &importer{
		m: m, ctx: ctx, apiID: restAPIID, doc: doc, authorizer: map[string]string{}, validators: map[string]string{},
		overwrite: mode == driver.ImportOverwrite,
	}

	if runErr := imp.run(); runErr != nil {
		return nil, runErr
	}

	if in.FailOnWarnings && len(imp.warnings) > 0 {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "Warnings found during import: %s", strings.Join(imp.warnings, "; "))
	}

	api, err := m.GetRestAPI(ctx, restAPIID)
	if err != nil {
		return nil, err
	}

	return &driver.ImportResult{API: api, Warnings: imp.warnings}, nil
}

// clearAPIContent removes everything an overwrite import replaces, keeping the
// root resource, deployments and stages.
func (m *Mock) clearAPIContent(apiID string) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	root := ad.api.RootResourceID

	for id := range ad.resources {
		if id != root {
			delete(ad.resources, id)
		}
	}

	ad.resources[root].Methods = map[string]*driver.Method{}
	ad.apiExt = newAPIExt()
}

func (i *importer) warn(format string, args ...any) {
	i.warnings = append(i.warnings, fmt.Sprintf(format, args...))
}

// run imports each part of the definition in dependency order.
func (i *importer) run() error {
	i.importModels()
	i.importValidators()
	i.importAuthorizers()
	i.importGatewayResponses()

	root, err := i.m.GetResources(i.ctx, i.apiID)
	if err != nil {
		return err
	}

	i.resources = map[string]string{}

	for k := range root {
		i.resources[root[k].Path] = root[k].ID
	}

	paths, _ := i.doc["paths"].(map[string]any)

	for _, p := range sortedAnyKeys(paths) {
		item, _ := paths[p].(map[string]any)
		if err := i.importPath(p, item); err != nil {
			return err
		}
	}

	return nil
}

func (i *importer) importModels() {
	defs, _ := i.doc["definitions"].(map[string]any)

	if comps, ok := i.doc["components"].(map[string]any); ok {
		defs, _ = comps["schemas"].(map[string]any)
	}

	for _, name := range sortedAnyKeys(defs) {
		if !modelNamePattern.MatchString(name) {
			i.warn("model %q skipped: names must be alphanumeric", name)

			continue
		}

		schema, _ := json.Marshal(defs[name])
		_, err := i.m.CreateModel(i.ctx, i.apiID, &driver.CreateModelInput{Name: name, Schema: string(schema)})

		if err != nil && !cerrors.IsAlreadyExists(err) {
			i.warn("model %q skipped: %s", name, cerrors.Message(err))
		}
	}
}

func (i *importer) importValidators() {
	vals, _ := i.doc[extValidators].(map[string]any)

	for _, name := range sortedAnyKeys(vals) {
		cfg, _ := vals[name].(map[string]any)

		v, err := i.m.CreateRequestValidator(i.ctx, i.apiID, &driver.CreateRequestValidatorInput{
			Name: name, ValidateRequestBody: boolVal(cfg["validateRequestBody"]),
			ValidateRequestParameters: boolVal(cfg["validateRequestParameters"]),
		})
		if err != nil {
			i.warn("request validator %q skipped: %s", name, cerrors.Message(err))

			continue
		}

		i.validators[name] = v.ID
	}
}

// importAuthorizers creates the authorizers declared as security schemes with
// the x-amazon-apigateway-authorizer extension.
func (i *importer) importAuthorizers() {
	schemes, _ := i.doc["securityDefinitions"].(map[string]any)
	if comps, ok := i.doc["components"].(map[string]any); ok {
		schemes, _ = comps["securitySchemes"].(map[string]any)
	}

	for _, name := range sortedAnyKeys(schemes) {
		scheme, _ := schemes[name].(map[string]any)

		ext, ok := scheme[extAuthorizer].(map[string]any)
		if !ok {
			continue
		}

		in := authorizerFromExt(name, scheme, ext)

		az, err := i.m.CreateAuthorizer(i.ctx, i.apiID, in)
		if err != nil {
			i.warn("authorizer %q skipped: %s", name, cerrors.Message(err))

			continue
		}

		i.authorizer[name] = az.ID
	}
}

func authorizerFromExt(name string, scheme, ext map[string]any) *driver.CreateAuthorizerInput {
	in := &driver.CreateAuthorizerInput{
		Name: name, Type: strings.ToUpper(strVal(ext["type"])), AuthorizerURI: strVal(ext["authorizerUri"]),
		AuthorizerCredentials: strVal(ext["authorizerCredentials"]), IdentityValidationExpression: strVal(ext["identityValidationExpression"]),
		IdentitySource: strVal(ext["identitySource"]), ProviderARNs: stringList(ext["providerARNs"]),
		AuthType: strVal(scheme["x-amazon-apigateway-authtype"]),
	}

	if in.Type == authTypeCognito {
		in.Type = driver.AuthorizerCognito
	}

	if in.IdentitySource == "" && strVal(scheme["in"]) == locHeader && strVal(scheme["name"]) != "" {
		in.IdentitySource = "method.request.header." + strVal(scheme["name"])
	}

	if ttl, ok := ext["authorizerResultTtlInSeconds"].(int); ok {
		in.AuthorizerResultTTLInSeconds = &ttl
	}

	return in
}

func (i *importer) importGatewayResponses() {
	resps, _ := i.doc[extGatewayResp].(map[string]any)

	for _, typ := range sortedAnyKeys(resps) {
		cfg, _ := resps[typ].(map[string]any)

		_, err := i.m.PutGatewayResponse(i.ctx, i.apiID, typ, &driver.PutGatewayResponseInput{
			StatusCode: strVal(cfg["statusCode"]), ResponseParameters: stringMap(cfg["responseParameters"]),
			ResponseTemplates: stringMap(cfg["responseTemplates"]),
		})
		if err != nil {
			i.warn("gateway response %q skipped: %s", typ, cerrors.Message(err))
		}
	}
}

// importPath creates the resource chain of a path and each operation on it.
func (i *importer) importPath(path string, item map[string]any) error {
	rid, err := i.ensureResource(path)
	if err != nil {
		return err
	}

	for _, verb := range append(append([]string{}, openAPIMethods...), extAnyMethod) {
		op, ok := item[verb].(map[string]any)
		if !ok {
			continue
		}

		method := strings.ToUpper(verb)
		if verb == extAnyMethod {
			method = driver.MethodANY
		}

		if err := i.importOperation(rid, path, method, op); err != nil {
			return err
		}
	}

	return nil
}

// ensureResource creates every missing segment of path and returns the id of the
// last one.
func (i *importer) ensureResource(path string) (string, error) {
	if id, ok := i.resources[path]; ok {
		return id, nil
	}

	segs := splitPath(path)
	parent := i.resources["/"]
	cur := ""

	for _, seg := range segs {
		cur += "/" + seg

		if id, ok := i.resources[cur]; ok {
			parent = id

			continue
		}

		res, err := i.m.CreateResource(i.ctx, i.apiID, parent, seg)
		if err != nil {
			return "", err
		}

		i.resources[cur], parent = res.ID, res.ID
	}

	return parent, nil
}

// importOperation defines one method: its parameters, security, validator and
// integration.
func (i *importer) importOperation(rid, path, method string, op map[string]any) error {
	in := driver.PutMethodInput{
		AuthorizationType: "NONE", OperationName: strVal(op["operationId"]), RequestParameters: map[string]bool{},
	}

	for _, p := range anyList(op["parameters"]) {
		pm, _ := p.(map[string]any)
		if loc, name := paramLocation(pm); loc != "" {
			in.RequestParameters["method.request."+loc+"."+name] = boolVal(pm["required"])
		}
	}

	i.applySecurity(&in, op)

	if name := strVal(op[extValidator]); name != "" {
		in.RequestValidatorID = i.validators[name]
	} else if name := strVal(i.doc[extValidator]); name != "" {
		in.RequestValidatorID = i.validators[name]
	}

	if i.overwrite {
		_ = i.m.DeleteMethod(i.ctx, i.apiID, rid, method)
	} else if _, err := i.m.GetMethod(i.ctx, i.apiID, rid, method); err == nil {
		_ = i.m.DeleteMethod(i.ctx, i.apiID, rid, method)
	}

	if _, err := i.m.PutMethod(i.ctx, i.apiID, rid, method, in); err != nil {
		return cerrors.Newf(cerrors.InvalidArgument, "Unable to put method %s on %s: %s", method, path, cerrors.Message(err))
	}

	return i.importIntegration(rid, path, method, op)
}

// paramLocation maps an OpenAPI parameter to its method request location.
func paramLocation(p map[string]any) (loc, name string) {
	name = strVal(p["name"])

	switch strVal(p["in"]) {
	case queryIn:
		return locQuery, name
	case "header":
		return locHeader, name
	case "path":
		return locPath, name
	default:
		return "", ""
	}
}

// applySecurity maps an operation's security requirement to the method's
// authorization type and API key requirement.
func (i *importer) applySecurity(in *driver.PutMethodInput, op map[string]any) {
	sec, ok := op["security"]
	if !ok {
		sec = i.doc["security"]
	}

	for _, req := range anyList(sec) {
		reqMap, _ := req.(map[string]any)

		for _, name := range sortedAnyKeys(reqMap) {
			if name == "api_key" {
				in.APIKeyRequired = true

				continue
			}

			id, found := i.authorizer[name]
			if !found {
				continue
			}

			in.AuthorizerID = id
			in.AuthorizationType = authTypeCustom

			if az, err := i.m.GetAuthorizer(i.ctx, i.apiID, id); err == nil && az.Type == driver.AuthorizerCognito {
				in.AuthorizationType = authTypeCognito
				in.AuthorizationScopes = stringList(reqMap[name])
			}
		}
	}
}

// importIntegration reads x-amazon-apigateway-integration into the method's
// integration, method responses and integration responses.
func (i *importer) importIntegration(rid, path, method string, op map[string]any) error {
	ext, ok := op[extIntegration].(map[string]any)
	if !ok {
		i.warn("%s %s has no x-amazon-apigateway-integration; it cannot be deployed", method, path)

		return nil
	}

	in := driver.PutIntegrationInput{
		Type: strings.ToUpper(strVal(ext["type"])), IntegrationHTTPMethod: strVal(ext["httpMethod"]), URI: strVal(ext["uri"]),
		PassthroughBehavior: strings.ToUpper(strVal(ext["passthroughBehavior"])), Credentials: strVal(ext["credentials"]),
		RequestParameters: stringMap(ext["requestParameters"]), RequestTemplates: stringMap(ext["requestTemplates"]),
		ContentHandling: strVal(ext["contentHandling"]), CacheNamespace: strVal(ext["cacheNamespace"]),
		ConnectionType: strVal(ext["connectionType"]), ConnectionID: strVal(ext["connectionId"]),
	}

	if n, ok := ext["timeoutInMillis"].(int); ok {
		in.TimeoutInMillis = n
	}

	if _, err := i.m.PutIntegration(i.ctx, i.apiID, rid, method, in); err != nil {
		return cerrors.Newf(cerrors.InvalidArgument, "Unable to put integration on %s %s: %s", method, path, cerrors.Message(err))
	}

	i.importResponses(rid, method, op, ext)

	return nil
}

func (i *importer) importResponses(rid, method string, op, ext map[string]any) {
	declared, _ := op["responses"].(map[string]any)
	for _, code := range sortedAnyKeys(declared) {
		if _, err := strconv.Atoi(code); err != nil {
			continue
		}

		_, _ = i.m.PutMethodResponse(i.ctx, i.apiID, rid, method, code, driver.PutMethodResponseInput{})
	}

	resps, _ := ext["responses"].(map[string]any)

	for _, pattern := range sortedAnyKeys(resps) {
		cfg, _ := resps[pattern].(map[string]any)

		code := strVal(cfg["statusCode"])
		if code == "" {
			continue
		}

		if _, err := i.m.GetMethodResponse(i.ctx, i.apiID, rid, method, code); err != nil {
			_, _ = i.m.PutMethodResponse(i.ctx, i.apiID, rid, method, code, driver.PutMethodResponseInput{})
		}

		sel := pattern
		if pattern == defaultSelection {
			sel = ""
		}

		_, _ = i.m.PutIntegrationResponse(i.ctx, i.apiID, rid, method, code, driver.PutIntegrationResponseInput{
			SelectionPattern: sel, ResponseParameters: stringMap(cfg["responseParameters"]),
			ResponseTemplates: stringMap(cfg["responseTemplates"]), ContentHandling: strVal(cfg["contentHandling"]),
		})
	}
}

// --- decoded-document helpers ---

func strVal(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return ""
	}
}

func boolVal(v any) bool {
	b, _ := v.(bool)

	return b
}

func anyList(v any) []any {
	l, _ := v.([]any)

	return l
}

func stringList(v any) []string {
	var out []string

	for _, e := range anyList(v) {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

func stringMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}

	out := make(map[string]string, len(m))
	for k, e := range m {
		out[k] = strVal(e)
	}

	return out
}

func sortedAnyKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
