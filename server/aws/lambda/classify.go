package lambda

import (
	"net/http"
	"strings"
)

// opID names the Lambda API operation a request runs. classify picks it from
// the request, and ServeHTTP and IAMChecks both use that choice, so the
// operation IAM authorizes is the one that runs. opUnknown is every request
// the handler answers with an error and no side effect.
type opID = string

// The Lambda operations this handler serves.
const (
	opUnknown opID = ""

	opInvokeFunctionURL opID = "InvokeFunctionUrl"

	opListFunctions               opID = "ListFunctions"
	opCreateFunction              opID = "CreateFunction"
	opGetFunction                 opID = "GetFunction"
	opDeleteFunction              opID = "DeleteFunction"
	opInvoke                      opID = "Invoke"
	opGetFunctionConfiguration    opID = "GetFunctionConfiguration"
	opUpdateFunctionConfiguration opID = "UpdateFunctionConfiguration"
	opUpdateFunctionCode          opID = "UpdateFunctionCode"
	opPublishVersion              opID = "PublishVersion"
	opListVersionsByFunction      opID = "ListVersionsByFunction"
	opCreateAlias                 opID = "CreateAlias"
	opListAliases                 opID = "ListAliases"
	opGetAlias                    opID = "GetAlias"
	opUpdateAlias                 opID = "UpdateAlias"
	opDeleteAlias                 opID = "DeleteAlias"
	opAddPermission               opID = "AddPermission"
	opGetPolicy                   opID = "GetPolicy"
	opRemovePermission            opID = "RemovePermission"

	opTagResource   opID = "TagResource"
	opUntagResource opID = "UntagResource"
	opListTags      opID = "ListTags"

	opCreateEventSourceMapping opID = "CreateEventSourceMapping"
	opListEventSourceMappings  opID = "ListEventSourceMappings"
	opGetEventSourceMapping    opID = "GetEventSourceMapping"
	opUpdateEventSourceMapping opID = "UpdateEventSourceMapping"
	opDeleteEventSourceMapping opID = "DeleteEventSourceMapping"

	opListLayers                   opID = "ListLayers"
	opGetLayerVersionByArn         opID = "GetLayerVersionByArn"
	opPublishLayerVersion          opID = "PublishLayerVersion"
	opListLayerVersions            opID = "ListLayerVersions"
	opGetLayerVersion              opID = "GetLayerVersion"
	opDeleteLayerVersion           opID = "DeleteLayerVersion"
	opAddLayerVersionPermission    opID = "AddLayerVersionPermission"
	opGetLayerVersionPolicy        opID = "GetLayerVersionPolicy"
	opRemoveLayerVersionPermission opID = "RemoveLayerVersionPermission"

	opCreateFunctionURLConfig opID = "CreateFunctionUrlConfig"
	opGetFunctionURLConfig    opID = "GetFunctionUrlConfig"
	opUpdateFunctionURLConfig opID = "UpdateFunctionUrlConfig"
	opDeleteFunctionURLConfig opID = "DeleteFunctionUrlConfig"
	opListFunctionURLConfigs  opID = "ListFunctionUrlConfigs"

	opPutEventInvokeConfig    opID = "PutFunctionEventInvokeConfig"
	opUpdateEventInvokeConfig opID = "UpdateFunctionEventInvokeConfig"
	opGetEventInvokeConfig    opID = "GetFunctionEventInvokeConfig"
	opDeleteEventInvokeConfig opID = "DeleteFunctionEventInvokeConfig"
	opListEventInvokeConfigs  opID = "ListFunctionEventInvokeConfigs"

	opPutProvisionedConcurrency    opID = "PutProvisionedConcurrencyConfig"
	opGetProvisionedConcurrency    opID = "GetProvisionedConcurrencyConfig"
	opDeleteProvisionedConcurrency opID = "DeleteProvisionedConcurrencyConfig"
	opListProvisionedConcurrency   opID = "ListProvisionedConcurrencyConfigs"

	opGetFunctionCodeSigningConfig    opID = "GetFunctionCodeSigningConfig"
	opDeleteFunctionCodeSigningConfig opID = "DeleteFunctionCodeSigningConfig"

	opPutFunctionConcurrency    opID = "PutFunctionConcurrency"
	opGetFunctionConcurrency    opID = "GetFunctionConcurrency"
	opDeleteFunctionConcurrency opID = "DeleteFunctionConcurrency"
)

// route is the group of operations that share a URL prefix and the
// preconditions ServeHTTP checks before running one of them.
type route int

const (
	routeFunctions route = iota
	routeFunctionURLInvoke
	routeTags
	routeEventSourceMappings
	routeLayers
	routeFunctionURL
	routeEventInvokeConfig
	routeProvisionedConcurrency
	routeCodeSigning
	routeConcurrency
)

// opArgs is what classify extracts from the request.
type opArgs struct {
	route route
	// parts are the path segments after the route's prefix, split on "/".
	// For routeFunctions an empty slice is the /functions collection.
	parts []string
	// name is the function or layer name the operation acts on, exactly as
	// dispatch passes it to the driver. For the /2015-03-31/functions tree it
	// is the bare name resolveFunctionRef returns.
	name string
	// qualifier is the version or alias the request names: the qualifier
	// resolveFunctionRef reconciles for the /functions tree, and ?Qualifier=
	// on the other routes that read it.
	qualifier string
	// item is the alias name, statement id, event source mapping UUID, tag
	// resource ARN or layer version the operation names.
	item string
	// qualifierConflict is a FunctionName qualifier that disagrees with
	// ?Qualifier=, which dispatch answers with a ValidationException.
	qualifierConflict bool
	// refRegion and refAccount are the region and account a FunctionName (or
	// tag resource) ARN names, "" when it does not name one.
	refRegion, refAccount string
}

// functionRefScope returns the region and account a function reference
// names: both for a full ARN (arn:aws:lambda:<region>:<account>:function:...),
// the account for a partial ARN (<account>:function:...), and neither for a
// plain name.
func functionRefScope(ref string) (region, account string) {
	const arnFields = 6 // arn, partition, service, region, account, rest

	if strings.HasPrefix(ref, "arn:") {
		parts := strings.SplitN(ref, ":", arnFields)
		if len(parts) == arnFields {
			return parts[3], parts[4]
		}

		return "", ""
	}

	if i := strings.Index(ref, ":function:"); i >= 0 {
		return "", ref[:i]
	}

	return "", ""
}

// Function sub-resource path segments.
const (
	subInvocations   = "invocations"
	subConfiguration = "configuration"
	subCode          = "code"
	subURL           = "url"
	subURLs          = "urls"
)

// Segment counts of a /2015-03-31/functions/... path.
const (
	partsResource    = 1 // /functions/{name}
	partsSubresource = 2 // /functions/{name}/{sub}
	partsSubItem     = 3 // /functions/{name}/{sub}/{id}
)

// classify returns the operation r runs and its arguments. It reads only the
// Host, the URL path and query, and the method, and follows the dispatch
// order: a Function URL host first, then each version-prefixed sub-API, then
// the /2015-03-31/functions tree. It does not modify r.
func classify(r *http.Request) (opID, opArgs) {
	if isFunctionURLHost(r.Host) {
		return opInvokeFunctionURL, opArgs{route: routeFunctionURLInvoke}
	}

	path := r.URL.Path

	switch {
	case strings.HasPrefix(path, tagsPrefix):
		return classifyTags(r, trimRoute(path, tagsPrefix))
	case strings.HasPrefix(path, esmPrefix):
		return classifyEventSourceMappings(r, trimRoute(path, esmPrefix))
	case strings.HasPrefix(path, layersPrefix):
		return classifyLayers(r, trimRoute(path, layersPrefix))
	case strings.HasPrefix(path, functionURLPrefix):
		return classifyFunctionURL(r, trimRoute(path, functionURLPrefix))
	case strings.HasPrefix(path, eventInvokeConfigPrefix):
		return classifyEventInvokeConfig(r, trimRoute(path, eventInvokeConfigPrefix))
	case isProvisionedConcurrencyPath(path):
		return classifyProvisionedConcurrency(r)
	case isCodeSigningPath(path):
		return classifyCodeSigning(r)
	}

	if name, ok := concurrencyFunctionName(path); ok {
		return methodOp(r.Method, map[string]opID{
			http.MethodPut: opPutFunctionConcurrency, http.MethodGet: opGetFunctionConcurrency,
			http.MethodDelete: opDeleteFunctionConcurrency,
		}), opArgs{route: routeConcurrency, name: name}
	}

	return classifyFunctions(r)
}

// trimRoute strips a route prefix and the slash after it.
func trimRoute(path, prefix string) string {
	return strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")
}

// methodOp returns the operation ops binds to method, or opUnknown.
func methodOp(method string, ops map[string]opID) opID {
	return ops[method]
}

// classifyTags handles /2017-03-31/tags/{arn}.
func classifyTags(r *http.Request, arn string) (opID, opArgs) {
	op := methodOp(r.Method, map[string]opID{
		http.MethodPost: opTagResource, http.MethodDelete: opUntagResource, http.MethodGet: opListTags,
	})

	a := opArgs{route: routeTags, item: arn, name: functionNameFromARN(arn)}
	a.refRegion, a.refAccount = functionRefScope(arn)

	return op, a
}

// classifyEventSourceMappings handles /2015-03-31/event-source-mappings[/{uuid}].
func classifyEventSourceMappings(r *http.Request, uuid string) (opID, opArgs) {
	a := opArgs{route: routeEventSourceMappings, item: uuid}

	if uuid == "" {
		return methodOp(r.Method, map[string]opID{
			http.MethodPost: opCreateEventSourceMapping, http.MethodGet: opListEventSourceMappings,
		}), a
	}

	return methodOp(r.Method, map[string]opID{
		http.MethodGet: opGetEventSourceMapping, http.MethodPut: opUpdateEventSourceMapping,
		http.MethodDelete: opDeleteEventSourceMapping,
	}), a
}

// classifyLayers handles /2018-10-31/layers[/...].
func classifyLayers(r *http.Request, rest string) (opID, opArgs) {
	a := opArgs{route: routeLayers}

	if rest == "" {
		if r.Method != http.MethodGet {
			return opUnknown, a
		}

		if r.URL.Query().Get("find") == findLayerVersion {
			return opGetLayerVersionByArn, a
		}

		return opListLayers, a
	}

	a.parts = strings.Split(rest, "/")
	if len(a.parts) < partsVersions || a.parts[1] != subVersions {
		return opUnknown, a
	}

	a.name = a.parts[0]

	switch len(a.parts) {
	case partsVersions:
		return methodOp(r.Method, map[string]opID{
			http.MethodPost: opPublishLayerVersion, http.MethodGet: opListLayerVersions,
		}), a
	case partsVersion:
		a.item = a.parts[2]

		return methodOp(r.Method, map[string]opID{
			http.MethodGet: opGetLayerVersion, http.MethodDelete: opDeleteLayerVersion,
		}), a
	}

	return classifyLayerPolicy(r, &a)
}

// classifyLayerPolicy handles .../layers/{name}/versions/{v}/policy[/{sid}].
func classifyLayerPolicy(r *http.Request, a *opArgs) (opID, opArgs) {
	if a.parts[3] != subPolicy {
		return opUnknown, *a
	}

	a.item = a.parts[2]

	switch len(a.parts) {
	case partsPolicy:
		return methodOp(r.Method, map[string]opID{
			http.MethodPost: opAddLayerVersionPermission, http.MethodGet: opGetLayerVersionPolicy,
		}), *a
	case partsPolicyStmt:
		if r.Method == http.MethodDelete {
			return opRemoveLayerVersionPermission, *a
		}
	}

	return opUnknown, *a
}

// classifyFunctionURL handles /2021-10-31/functions/{name}/url and /urls.
func classifyFunctionURL(r *http.Request, rest string) (opID, opArgs) {
	a := opArgs{route: routeFunctionURL, parts: strings.Split(rest, "/")}

	const wantParts = 2 // {name}/url or {name}/urls
	if len(a.parts) != wantParts || a.parts[0] == "" {
		return opUnknown, a
	}

	a.name = a.parts[0]
	a.qualifier = r.URL.Query().Get("Qualifier")

	switch a.parts[1] {
	case subURL:
		return methodOp(r.Method, map[string]opID{
			http.MethodPost: opCreateFunctionURLConfig, http.MethodPut: opUpdateFunctionURLConfig,
			http.MethodGet: opGetFunctionURLConfig, http.MethodDelete: opDeleteFunctionURLConfig,
		}), a
	case subURLs:
		if r.Method == http.MethodGet {
			return opListFunctionURLConfigs, a
		}
	}

	return opUnknown, a
}

// classifyEventInvokeConfig handles /2019-09-25/functions/{name}/event-invoke-config[/list].
func classifyEventInvokeConfig(r *http.Request, rest string) (opID, opArgs) {
	a := opArgs{route: routeEventInvokeConfig, parts: strings.Split(rest, "/")}

	const (
		itemParts = 2 // {name}/event-invoke-config
		listParts = 3 // {name}/event-invoke-config/list
	)

	if len(a.parts) < itemParts || a.parts[0] == "" || a.parts[1] != eventInvokeConfigSuffix {
		return opUnknown, a
	}

	a.name = a.parts[0]
	a.qualifier = r.URL.Query().Get("Qualifier")

	if len(a.parts) == listParts && a.parts[2] == eventInvokeListSegment {
		if r.Method == http.MethodGet {
			return opListEventInvokeConfigs, a
		}

		return opUnknown, a
	}

	if len(a.parts) != itemParts {
		return opUnknown, a
	}

	return methodOp(r.Method, map[string]opID{
		http.MethodPut: opPutEventInvokeConfig, http.MethodPost: opUpdateEventInvokeConfig,
		http.MethodGet: opGetEventInvokeConfig, http.MethodDelete: opDeleteEventInvokeConfig,
	}), a
}

// classifyProvisionedConcurrency handles /2019-09-30/functions/{name}/provisioned-concurrency.
func classifyProvisionedConcurrency(r *http.Request) (opID, opArgs) {
	a := opArgs{route: routeProvisionedConcurrency}

	name, ok := provisionedConcurrencyFunctionName(r.URL.Path)
	if !ok {
		return opUnknown, a
	}

	a.name = name

	if r.Method == http.MethodGet && r.URL.Query().Get(provisionedConcurrencyListParam) == provisionedConcurrencyListParamAll {
		return opListProvisionedConcurrency, a
	}

	a.qualifier = r.URL.Query().Get("Qualifier")

	return methodOp(r.Method, map[string]opID{
		http.MethodPut: opPutProvisionedConcurrency, http.MethodGet: opGetProvisionedConcurrency,
		http.MethodDelete: opDeleteProvisionedConcurrency,
	}), a
}

// classifyCodeSigning handles /2020-06-30/functions/{name}/code-signing-config.
func classifyCodeSigning(r *http.Request) (opID, opArgs) {
	name, _ := codeSigningFunctionName(r.URL.Path)
	a := opArgs{route: routeCodeSigning, name: name}

	return methodOp(r.Method, map[string]opID{
		http.MethodGet: opGetFunctionCodeSigningConfig, http.MethodDelete: opDeleteFunctionCodeSigningConfig,
	}), a
}

// classifyFunctions handles the /2015-03-31/functions tree.
func classifyFunctions(r *http.Request) (opID, opArgs) {
	a := opArgs{route: routeFunctions}

	rest := trimRoute(r.URL.Path, pathPrefix)
	if rest == "" {
		return methodOp(r.Method, map[string]opID{
			http.MethodGet: opListFunctions, http.MethodPost: opCreateFunction,
		}), a
	}

	a.parts = strings.Split(rest, "/")
	a.refRegion, a.refAccount = functionRefScope(a.parts[0])

	name, embedded := splitFunctionNameQualifier(a.parts[0])
	qualifier, ok := reconcileQualifier(embedded, r.URL.Query().Get("Qualifier"))
	a.name, a.qualifier, a.qualifierConflict = name, qualifier, !ok

	if !ok {
		return opUnknown, a
	}

	switch len(a.parts) {
	case partsResource:
		return methodOp(r.Method, map[string]opID{http.MethodGet: opGetFunction, http.MethodDelete: opDeleteFunction}), a
	case partsSubresource:
		return subresourceOp(r.Method, a.parts[1]), a
	case partsSubItem:
		a.item = a.parts[2]
		return subItemOp(r.Method, a.parts[1]), a
	}

	return opUnknown, a
}

// subresourceOps binds each /functions/{name}/{sub} segment to its methods.
//
//nolint:gochecknoglobals // static lookup table
var subresourceOps = map[string]map[string]opID{
	subInvocations:   {http.MethodPost: opInvoke},
	subConfiguration: {http.MethodGet: opGetFunctionConfiguration, http.MethodPut: opUpdateFunctionConfiguration},
	subCode:          {http.MethodPut: opUpdateFunctionCode},
	subVersions:      {http.MethodPost: opPublishVersion, http.MethodGet: opListVersionsByFunction},
	subAliases:       {http.MethodPost: opCreateAlias, http.MethodGet: opListAliases},
	subPolicy:        {http.MethodPost: opAddPermission, http.MethodGet: opGetPolicy},
}

// subresourceOp returns the operation of /functions/{name}/{sub}.
func subresourceOp(method, sub string) opID {
	return subresourceOps[sub][method]
}

// subItemOp returns the operation of /functions/{name}/{sub}/{id}.
func subItemOp(method, sub string) opID {
	switch sub {
	case subAliases:
		return methodOp(method, map[string]opID{
			http.MethodGet: opGetAlias, http.MethodPut: opUpdateAlias, http.MethodDelete: opDeleteAlias,
		})
	case subPolicy:
		if method == http.MethodDelete {
			return opRemovePermission
		}
	}

	return opUnknown
}
