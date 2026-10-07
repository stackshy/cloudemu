package apigateway

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

const (
	contentYAML      = "application/yaml"
	exportSwaggerVer = "2.0"
	exportOASVer     = "3.0.1"
	queryIn          = "query"
)

// GetExport exports a stage as an OpenAPI definition. swagger gives 2.0 and
// oas30 gives 3.0.1; the integrations and apigateway extensions add the
// x-amazon-apigateway-* members that make the document importable again.
func (m *Mock) GetExport(_ context.Context, in *driver.GetExportInput) (*driver.Export, error) {
	if in.ExportType != driver.ExportSwagger && in.ExportType != driver.ExportOAS30 {
		return nil, cerrors.New(cerrors.InvalidArgument, "Invalid export type: must be swagger or oas30")
	}

	ad, err := m.getAPI(in.RestAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	doc, err := m.exportDocument(ad, in)
	ad.mu.RUnlock()

	if err != nil {
		return nil, err
	}

	if mediaType(in.Accept) == contentYAML {
		b, yerr := yaml.Marshal(doc)
		if yerr != nil {
			return nil, cerrors.Newf(cerrors.Internal, "export: %v", yerr)
		}

		return &driver.Export{ContentType: contentYAML, Body: b}, nil
	}

	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "export: %v", err)
	}

	return &driver.Export{ContentType: contentTypeJSON, Body: b}, nil
}

// exportDocument builds the document from the stage's deployed tree. ad.mu is
// held for reading.
func (m *Mock) exportDocument(ad *apiData, in *driver.GetExportInput) (map[string]any, error) {
	st, ok := ad.stages[in.StageName]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid stage identifier specified %s", in.StageName)
	}

	ext := extensionSet(in.Extensions)
	host := ad.api.ID + ".execute-api." + m.opts.Region + ".amazonaws.com"
	version := ad.api.Version

	if version == "" {
		version = time.Unix(ad.api.CreatedDate, 0).UTC().Format(time.RFC3339)
	}

	info := map[string]any{"title": ad.api.Name, "version": version}
	if ad.api.Description != "" {
		info["description"] = ad.api.Description
	}

	doc := map[string]any{"info": info, "paths": exportPaths(ad.trees[st.DeploymentID], in.ExportType, ext)}

	if in.ExportType == driver.ExportSwagger {
		doc["swagger"], doc["host"], doc["basePath"], doc["schemes"] = exportSwaggerVer, host, "/"+in.StageName, []string{"https"}
	} else {
		doc["openapi"] = exportOASVer
		doc["servers"] = []any{map[string]any{
			"url":       "https://" + host + "/{basePath}",
			"variables": map[string]any{"basePath": map[string]any{"default": in.StageName}},
		}}
	}

	m.exportModelsAndExtensions(doc, ad, in.ExportType, ext)

	return doc, nil
}

func extensionSet(list []string) map[string]bool {
	set := map[string]bool{}

	for _, e := range list {
		set[strings.ToLower(strings.TrimSpace(e))] = true
	}

	return set
}

// exportModelsAndExtensions adds the model definitions and the optional
// authorizer and API-wide extensions.
func (*Mock) exportModelsAndExtensions(doc map[string]any, ad *apiData, exportType string, ext map[string]bool) {
	schemas := map[string]any{}

	for name, mod := range ad.models {
		var s any

		if json.Unmarshal([]byte(mod.Schema), &s) == nil {
			schemas[name] = s
		}
	}

	if exportType == driver.ExportSwagger {
		doc["definitions"] = schemas
	} else {
		doc["components"] = map[string]any{"schemas": schemas}
	}

	if ext["authorizers"] || ext["apigateway"] {
		addAuthorizerSchemes(doc, ad, exportType)
	}

	if ext["apigateway"] {
		addAPIExtensions(doc, ad)
	}
}

func addAuthorizerSchemes(doc map[string]any, ad *apiData, exportType string) {
	schemes := map[string]any{}

	for _, az := range ad.authorizers {
		schemes[az.Name] = authorizerScheme(az)
	}

	if len(schemes) == 0 {
		return
	}

	if exportType == driver.ExportSwagger {
		doc["securityDefinitions"] = schemes

		return
	}

	comps, _ := doc["components"].(map[string]any)
	comps["securitySchemes"] = schemes
}

func authorizerScheme(az *driver.Authorizer) map[string]any {
	cfg := map[string]any{"type": strings.ToLower(az.Type)}
	if az.Type == driver.AuthorizerCognito {
		cfg["type"] = "cognito_user_pools"
		cfg["providerARNs"] = az.ProviderARNs
	}

	if az.AuthorizerURI != "" {
		cfg["authorizerUri"] = az.AuthorizerURI
	}

	if az.AuthorizerCredentials != "" {
		cfg["authorizerCredentials"] = az.AuthorizerCredentials
	}

	if az.IdentityValidationExpression != "" {
		cfg["identityValidationExpression"] = az.IdentityValidationExpression
	}

	if az.AuthorizerResultTTLInSeconds != nil {
		cfg["authorizerResultTtlInSeconds"] = *az.AuthorizerResultTTLInSeconds
	}

	scheme := map[string]any{"type": "apiKey", "name": "Authorization", "in": "header", extAuthorizer: cfg}

	if src, ok := strings.CutPrefix(az.IdentitySource, "method.request.header."); ok {
		scheme["name"] = src
	}

	return scheme
}

func addAPIExtensions(doc map[string]any, ad *apiData) {
	if len(ad.api.BinaryMediaTypes) > 0 {
		doc[extBinaryMedia] = ad.api.BinaryMediaTypes
	}

	if ad.api.APIKeySource != "" && ad.api.APIKeySource != "HEADER" {
		doc[extKeySource] = ad.api.APIKeySource
	}

	if len(ad.validators) > 0 {
		vals := map[string]any{}

		for _, v := range ad.validators {
			vals[v.Name] = map[string]any{
				"validateRequestBody": v.ValidateRequestBody, "validateRequestParameters": v.ValidateRequestParameters,
			}
		}

		doc[extValidators] = vals
	}

	if len(ad.gwResponses) > 0 {
		resps := map[string]any{}

		for typ, gr := range ad.gwResponses {
			resps[typ] = map[string]any{
				"statusCode": gr.StatusCode, "responseParameters": gr.ResponseParameters, "responseTemplates": gr.ResponseTemplates,
			}
		}

		doc[extGatewayResp] = resps
	}
}

// exportPaths renders the deployed resource tree as OpenAPI paths.
func exportPaths(tree map[string]*driver.Resource, exportType string, ext map[string]bool) map[string]any {
	paths := map[string]any{}

	ids := make([]string, 0, len(tree))
	for id := range tree {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	for _, id := range ids {
		res := tree[id]
		if len(res.Methods) == 0 {
			continue
		}

		item := map[string]any{}

		for _, method := range sortedMethodNames(res.Methods) {
			key := strings.ToLower(method)
			if method == driver.MethodANY {
				key = extAnyMethod
			}

			item[key] = exportOperation(res.Methods[method], exportType, ext)
		}

		paths[res.Path] = item
	}

	return paths
}

func sortedMethodNames(ms map[string]*driver.Method) []string {
	out := make([]string, 0, len(ms))
	for k := range ms {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

func exportOperation(mth *driver.Method, exportType string, ext map[string]bool) map[string]any {
	op := map[string]any{"responses": exportResponses(mth)}

	if mth.OperationName != "" {
		op["operationId"] = mth.OperationName
	}

	if params := exportParams(mth); len(params) > 0 {
		op["parameters"] = params
	}

	if exportType == driver.ExportSwagger {
		op["produces"] = []string{contentTypeJSON}
	}

	if mth.APIKeyRequired {
		op["security"] = []any{map[string]any{"api_key": []string{}}}
	}

	if ext["integrations"] || ext["apigateway"] {
		if mth.Integration != nil {
			op[extIntegration] = exportIntegration(mth.Integration)
		}
	}

	return op
}

func exportResponses(mth *driver.Method) map[string]any {
	out := map[string]any{}

	for code := range mth.MethodResponses {
		out[code] = map[string]any{"description": code + " response"}
	}

	if len(out) == 0 {
		out["200"] = map[string]any{"description": "200 response"}
	}

	return out
}

func exportParams(mth *driver.Method) []any {
	keys := make([]string, 0, len(mth.RequestParameters))
	for k := range mth.RequestParameters {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]any, 0, len(keys))

	for _, k := range keys {
		loc, name, ok := splitMethodParam(k)
		if !ok {
			continue
		}

		in := loc
		if loc == locQuery {
			in = queryIn
		}

		out = append(out, map[string]any{"name": name, "in": in, "required": mth.RequestParameters[k], "type": "string"})
	}

	return out
}

func exportIntegration(ig *driver.Integration) map[string]any {
	out := map[string]any{
		"type": strings.ToLower(ig.Type), "httpMethod": ig.IntegrationHTTPMethod, "uri": ig.URI,
		"passthroughBehavior": strings.ToLower(ig.PassthroughBehavior), "timeoutInMillis": ig.TimeoutInMillis,
	}

	if ig.Credentials != "" {
		out["credentials"] = ig.Credentials
	}

	if len(ig.RequestParameters) > 0 {
		out["requestParameters"] = ig.RequestParameters
	}

	if len(ig.RequestTemplates) > 0 {
		out["requestTemplates"] = ig.RequestTemplates
	}

	if ig.ConnectionType == connectionVpcLink {
		out["connectionType"], out["connectionId"] = ig.ConnectionType, ig.ConnectionID
	}

	if len(ig.IntegrationResponses) > 0 {
		resps := map[string]any{}

		for code, ir := range ig.IntegrationResponses {
			key := ir.SelectionPattern
			if key == "" {
				key = defaultSelection
			}

			resps[key] = map[string]any{"statusCode": code, "responseParameters": ir.ResponseParameters, "responseTemplates": ir.ResponseTemplates}
		}

		out["responses"] = resps
	}

	return out
}
