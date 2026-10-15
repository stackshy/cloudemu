package lambda

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// Condition keys this handler sets (Service Authorization Reference, AWS
// Lambda, "Condition keys").
const (
	condFunctionARN       = "lambda:FunctionArn"
	condFunctionURLAuth   = "lambda:FunctionUrlAuthType"
	condInvokedViaURL     = "lambda:InvokedViaFunctionUrl"
	condPrincipal         = "lambda:Principal"
	condLayer             = "lambda:Layer"
	condSubnetIDs         = "lambda:SubnetIds"
	condSecurityGroupIDs  = "lambda:SecurityGroupIds"
	condCodeSigningConfig = "lambda:CodeSigningConfigArn"
	condTagKeys           = "aws:TagKeys"
	condRequestTagPrefix  = "aws:RequestTag/"
	condResourceTagPrefix = "aws:ResourceTag/"
)

// The IAM actions whose name differs from the operation, or that an
// operation needs on top of its own.
const (
	actionInvokeFunction    = "lambda:InvokeFunction"
	actionInvokeFunctionURL = "lambda:InvokeFunctionUrl"
	actionGetLayerVersion   = "lambda:GetLayerVersion"
	actionTagResource       = "lambda:TagResource"
)

// IAMChecks names the IAM actions and resource ARNs a request needs.
func (h *Handler) IAMChecks(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, bool) {
	checks, _, ok := h.IAMChecksWithContext(r, s)

	return checks, ok
}

// IAMChecksWithContext names the IAM actions and resource ARNs a request
// needs, and the Lambda condition keys known from it. It takes the operation
// from classify, the same function ServeHTTP dispatches on, so the authorized
// operation is always the one that runs. ok=false is returned only for
// requests the handler answers with an error and no side effect.
//
// The actions and resources follow the Service Authorization Reference for
// AWS Lambda: Invoke needs lambda:InvokeFunction, GetLayerVersionByArn needs
// lambda:GetLayerVersion, and a request that names a version or alias is
// authorized on the qualified function ARN. Resource ARNs are built from the
// server's own partition, region and account and the name dispatch uses, so
// a FunctionName ARN for another account never becomes the checked resource.
func (h *Handler) IAMChecksWithContext(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, map[string]string, bool) {
	op, a := classify(r)

	rule, ok := iamRules[op]
	if !ok || foreignRef(&a, s.AccountID, s.Region) {
		return nil, nil, false
	}

	if s.Partition == "" {
		s.Partition = "aws"
	}

	c := &checkSet{h: h, r: r, scope: s, args: &a, cond: map[string]string{}}
	if !rule(c, op) {
		return nil, nil, false
	}

	return c.checks, c.cond, true
}

// checkSet collects the checks and condition keys of one request.
type checkSet struct {
	h      *Handler
	r      *http.Request
	scope  awsauthz.Scope
	args   *opArgs
	checks []awsauthz.Check
	cond   map[string]string
}

func (c *checkSet) add(action, resource string, mode awsauthz.CheckMode) {
	c.checks = append(c.checks, awsauthz.Check{Action: action, Resource: resource, Mode: mode})
}

// functionARN is the ARN of function name in this account, qualified when
// qualifier is set.
func (c *checkSet) functionARN(name, qualifier string) string {
	arn := c.scope.ARN(serviceName, "function:"+name)
	if qualifier != "" {
		arn += ":" + qualifier
	}

	return arn
}

// target is the function, version or alias ARN of an operation that acts on
// the version or alias the request names: its FunctionName (with any
// qualifier embedded in it) and its Qualifier. $LATEST is the unpublished
// function itself, which dispatch serves as the unqualified function, so it
// is checked on the unqualified ARN.
func (c *checkSet) target() string {
	name, embedded := splitFunctionNameQualifier(c.args.name)

	qualifier := c.args.qualifier
	if qualifier == "" {
		qualifier = embedded
	}

	if qualifier == latestVersion {
		qualifier = ""
	}

	return c.functionARN(name, qualifier)
}

// unqualified is the unqualified ARN of the function the request names.
func (c *checkSet) unqualified() string {
	name, _ := splitFunctionNameQualifier(c.args.name)

	return c.functionARN(name, "")
}

func (c *checkSet) set(key, value string) {
	if value != "" {
		c.cond[key] = value
	}
}

func (c *checkSet) setList(key string, values []string) {
	c.set(key, strings.Join(values, iamdriver.ConditionValueSeparator))
}

// requestTags sets aws:RequestTag/* and aws:TagKeys from tags.
func (c *checkSet) requestTags(tags map[string]string) {
	keys := make([]string, 0, len(tags))
	for k, v := range tags {
		keys = append(keys, k)
		c.cond[condRequestTagPrefix+k] = v
	}

	c.setList(condTagKeys, keys)
}

// resourceTags sets aws:ResourceTag/* from the tags of the function name.
func (c *checkSet) resourceTags(name string) {
	tagger, ok := c.h.fn.(functionTagger)
	if !ok {
		return
	}

	bare, _ := splitFunctionNameQualifier(name)

	tags, err := tagger.ListFunctionTags(c.r.Context(), bare)
	if err != nil {
		return
	}

	for k, v := range tags {
		c.cond[condResourceTagPrefix+k] = v
	}
}

// layers adds lambda:GetLayerVersion on each layer version a function
// imports, and sets lambda:Layer.
func (c *checkSet) layers(arns []string) {
	for _, arn := range arns {
		c.add(actionGetLayerVersion, c.layerVersionARN(arn), awsauthz.Required)
	}

	c.setList(condLayer, arns)
}

// layerVersionARN rebuilds a layer version ARN in this account, or "" (an
// unknown resource) when arn is not one.
func (c *checkSet) layerVersionARN(arn string) string {
	name, version, ok := parseLayerARN(arn)
	if !ok {
		return ""
	}

	return c.scope.ARN(serviceName, "layer:"+name+":"+strconv.Itoa(version))
}

// vpc sets lambda:SubnetIds and lambda:SecurityGroupIds.
func (c *checkSet) vpc(v *vpcConfigEnvelope) {
	if v == nil {
		return
	}

	c.setList(condSubnetIDs, v.SubnetIDs)
	c.setList(condSecurityGroupIDs, v.SecurityGroupIDs)
}

// iamRule adds the checks of one operation. It returns false when the
// handler will reject the request without side effects.
type iamRule func(c *checkSet, op opID) bool

// action is the IAM action of an operation whose action is its own name.
func action(op opID) string { return serviceName + ":" + op }

// onTarget: the operation's own action on the function (version or alias)
// the request names.
func onTarget(c *checkSet, op opID) bool {
	c.add(action(op), c.target(), awsauthz.Required)
	c.resourceTags(c.args.name)

	return true
}

// onFunction: the operation's own action on the unqualified function.
func onFunction(c *checkSet, op opID) bool {
	c.add(action(op), c.unqualified(), awsauthz.Required)
	c.resourceTags(c.args.name)

	return true
}

// onAny: the operation's own action, which takes no resource.
func onAny(c *checkSet, op opID) bool {
	c.add(action(op), "*", awsauthz.Required)
	return true
}

// iamRules maps every operation the handler runs to its checks. Operations
// absent here are the error paths, reported as unknown.
//
// The resource is always the one dispatch acts on. Operations that read or
// act on the version or alias the request names (GetFunction, Invoke,
// DeleteFunction, the policy, URL, event invoke and provisioned concurrency
// operations) are checked on that qualified ARN. The others act on the
// function whatever qualifier the FunctionName carries (configuration, code,
// versions, aliases, tags, concurrency), so they are checked on the
// unqualified function ARN.
//
//nolint:gochecknoglobals // static lookup table
var iamRules = map[opID]iamRule{
	opInvokeFunctionURL: functionURLInvokeChecks,

	opListFunctions:               onAny,
	opCreateFunction:              createFunctionChecks,
	opGetFunction:                 onTarget,
	opDeleteFunction:              onTarget,
	opInvoke:                      invokeChecks,
	opGetFunctionConfiguration:    onTarget,
	opUpdateFunctionConfiguration: updateConfigurationChecks,
	opUpdateFunctionCode:          onFunction,
	opPublishVersion:              onFunction,
	opListVersionsByFunction:      onFunction,
	opCreateAlias:                 onFunction,
	opListAliases:                 onFunction,
	opGetAlias:                    onFunction,
	opUpdateAlias:                 onFunction,
	opDeleteAlias:                 onFunction,
	opAddPermission:               addPermissionChecks,
	opGetPolicy:                   onTarget,
	opRemovePermission:            removePermissionChecks,

	opTagResource:   tagChecks,
	opUntagResource: tagChecks,
	opListTags:      tagChecks,

	opCreateEventSourceMapping: createESMChecks,
	opListEventSourceMappings:  onAny,
	opGetEventSourceMapping:    esmChecks,
	opUpdateEventSourceMapping: esmChecks,
	opDeleteEventSourceMapping: esmChecks,

	opListLayers:                   onAny,
	opGetLayerVersionByArn:         layerByARNChecks,
	opPublishLayerVersion:          publishLayerChecks,
	opListLayerVersions:            onAny,
	opGetLayerVersion:              layerVersionChecks,
	opDeleteLayerVersion:           layerVersionChecks,
	opAddLayerVersionPermission:    layerVersionChecks,
	opGetLayerVersionPolicy:        layerVersionChecks,
	opRemoveLayerVersionPermission: layerVersionChecks,

	opCreateFunctionURLConfig: urlConfigBodyChecks,
	opUpdateFunctionURLConfig: urlConfigBodyChecks,
	opGetFunctionURLConfig:    urlConfigStoredChecks,
	opDeleteFunctionURLConfig: urlConfigStoredChecks,
	opListFunctionURLConfigs:  onFunction,

	opPutEventInvokeConfig:    onTarget,
	opUpdateEventInvokeConfig: onTarget,
	opGetEventInvokeConfig:    onTarget,
	opDeleteEventInvokeConfig: onTarget,
	opListEventInvokeConfigs:  onFunction,

	opPutProvisionedConcurrency:    onTarget,
	opGetProvisionedConcurrency:    onTarget,
	opDeleteProvisionedConcurrency: onTarget,
	opListProvisionedConcurrency:   onFunction,

	opGetFunctionCodeSigningConfig:    onFunction,
	opDeleteFunctionCodeSigningConfig: onFunction,

	opPutFunctionConcurrency:    onFunction,
	opGetFunctionConcurrency:    onFunction,
	opDeleteFunctionConcurrency: onFunction,
}

// invokeChecks: lambda:InvokeFunction on the function, version or alias. The
// function's resource-based policy can also grant it, so an implicit
// identity deny is finished by the handler (authorizeInvoke).
func invokeChecks(c *checkSet, _ opID) bool {
	c.add(actionInvokeFunction, c.target(), awsauthz.ResourcePolicy)
	c.resourceTags(c.args.name)

	return true
}

// createFunctionChecks: lambda:CreateFunction, plus lambda:TagResource when
// the request tags the function and lambda:GetLayerVersion on each layer.
func createFunctionChecks(c *checkSet, _ opID) bool {
	var req struct {
		FunctionName         string             `json:"FunctionName"`
		Tags                 map[string]string  `json:"Tags"`
		Layers               []string           `json:"Layers"`
		VpcConfig            *vpcConfigEnvelope `json:"VpcConfig"`
		CodeSigningConfigArn string             `json:"CodeSigningConfigArn"`
	}

	if !awsauthz.JSONBody(c.r, &req) {
		return false
	}

	name, invalid := createFunctionName(req.FunctionName, c.scope.AccountID, c.scope.Region)
	if invalid != "" {
		return false
	}

	arn := c.functionARN(name, "")

	c.add(action(opCreateFunction), arn, awsauthz.Required)

	if len(req.Tags) > 0 {
		c.add(actionTagResource, arn, awsauthz.Required)
		c.requestTags(req.Tags)
	}

	c.layers(req.Layers)
	c.vpc(req.VpcConfig)
	c.set(condCodeSigningConfig, req.CodeSigningConfigArn)

	return true
}

// updateConfigurationChecks: lambda:UpdateFunctionConfiguration, plus
// lambda:GetLayerVersion on each layer the request sets.
func updateConfigurationChecks(c *checkSet, op opID) bool {
	var req struct {
		Layers    []string           `json:"Layers"`
		VpcConfig *vpcConfigEnvelope `json:"VpcConfig"`
	}

	if !awsauthz.JSONBody(c.r, &req) {
		return false
	}

	onFunction(c, op)
	c.layers(req.Layers)
	c.vpc(req.VpcConfig)

	return true
}

// addPermissionChecks: lambda:AddPermission, with lambda:Principal and
// lambda:FunctionUrlAuthType from the statement being added.
func addPermissionChecks(c *checkSet, op opID) bool {
	var req addPermissionRequest
	if !awsauthz.JSONBody(c.r, &req) {
		return false
	}

	onTarget(c, op)
	c.set(condPrincipal, req.Principal)
	c.set(condFunctionURLAuth, req.FunctionURLAuthType)

	return true
}

// removePermissionChecks: lambda:RemovePermission, with lambda:Principal and
// lambda:FunctionUrlAuthType from the statement being removed, when it
// exists.
func removePermissionChecks(c *checkSet, op opID) bool {
	onTarget(c, op)

	if st, ok := c.h.policyStatements(c.r, c.args.name, c.args.qualifier); ok {
		for i := range st.statements {
			if st.statements[i].StatementID == c.args.item {
				c.set(condPrincipal, st.statements[i].Principal)
				c.set(condFunctionURLAuth, st.statements[i].FunctionURLAuthType)
			}
		}
	}

	return true
}

// tagChecks: the tagging actions act on the function the path ARN names. A
// path that is not a function ARN is evaluated as an unknown resource.
func tagChecks(c *checkSet, op opID) bool {
	resource := ""
	if strings.Contains(c.args.item, ":function:") {
		resource = c.unqualified()
	}

	c.add(action(op), resource, awsauthz.Required)
	c.resourceTags(c.args.name)

	switch op {
	case opTagResource:
		var req struct {
			Tags map[string]string `json:"Tags"`
		}

		if !awsauthz.JSONBody(c.r, &req) {
			return false
		}

		c.requestTags(req.Tags)
	case opUntagResource:
		c.setList(condTagKeys, c.r.URL.Query()["tagKeys"])
	}

	return true
}

// createESMChecks: lambda:CreateEventSourceMapping takes no resource; its
// target function is the lambda:FunctionArn condition. Tagging the mapping
// at create also needs lambda:TagResource. The event source's own
// permissions (sqs:ReceiveMessage and so on) belong to the function's
// execution role, not the caller.
func createESMChecks(c *checkSet, op opID) bool {
	var req struct {
		FunctionName string            `json:"FunctionName"`
		Tags         map[string]string `json:"Tags"`
	}

	if !awsauthz.JSONBody(c.r, &req) {
		return false
	}

	c.add(action(op), "*", awsauthz.Required)
	c.set(condFunctionARN, c.namedFunctionARN(req.FunctionName))

	if len(req.Tags) > 0 {
		c.add(actionTagResource, "", awsauthz.Required)
		c.requestTags(req.Tags)
	}

	return true
}

// esmChecks: the operation's own action on the event source mapping, with
// lambda:FunctionArn naming its function (the new one, for an update that
// changes it).
func esmChecks(c *checkSet, op opID) bool {
	c.add(action(op), c.scope.ARN(serviceName, "event-source-mapping:"+c.args.item), awsauthz.Required)

	if op == opUpdateEventSourceMapping {
		var req struct {
			FunctionName string `json:"FunctionName"`
		}

		if !awsauthz.JSONBody(c.r, &req) {
			return false
		}

		if req.FunctionName != "" {
			c.set(condFunctionARN, c.namedFunctionARN(req.FunctionName))
			return true
		}
	}

	if info, err := c.h.fn.GetEventSourceMapping(c.r.Context(), c.args.item); err == nil {
		c.set(condFunctionARN, info.FunctionArn)
	}

	return true
}

// namedFunctionARN is the function ARN a FunctionName value names, or "".
func (c *checkSet) namedFunctionARN(functionName string) string {
	if functionName == "" {
		return ""
	}

	name, qualifier := splitFunctionNameQualifier(functionName)

	return c.functionARN(name, qualifier)
}

// layerByARNChecks: GetLayerVersionByArn needs lambda:GetLayerVersion on the
// layer version. An Arn that does not parse is answered with a 400.
func layerByARNChecks(c *checkSet, _ opID) bool {
	arn := c.layerVersionARN(c.r.URL.Query().Get("Arn"))
	if arn == "" {
		return false
	}

	c.add(actionGetLayerVersion, arn, awsauthz.Required)

	return true
}

// publishLayerChecks: lambda:PublishLayerVersion on the layer.
func publishLayerChecks(c *checkSet, op opID) bool {
	c.add(action(op), c.scope.ARN(serviceName, "layer:"+c.args.name), awsauthz.Required)
	return true
}

// layerVersionChecks: the layer-version actions act on the layer version.
// GetLayerVersion is the action of GetLayerVersion; the others are their own.
// A version that is not a number is answered with a 400.
func layerVersionChecks(c *checkSet, op opID) bool {
	version, err := strconv.Atoi(c.args.item)
	if err != nil {
		return false
	}

	c.add(action(op), c.scope.ARN(serviceName, "layer:"+c.args.name+":"+strconv.Itoa(version)), awsauthz.Required)

	return true
}

// urlConfigBodyChecks: Create/UpdateFunctionUrlConfig, with
// lambda:FunctionUrlAuthType from the requested AuthType.
func urlConfigBodyChecks(c *checkSet, op opID) bool {
	var req functionURLRequest
	if !awsauthz.JSONBody(c.r, &req) {
		return false
	}

	onTarget(c, op)
	c.set(condFunctionARN, c.target())
	c.set(condFunctionURLAuth, req.AuthType)

	return true
}

// urlConfigStoredChecks: Get/DeleteFunctionUrlConfig, with
// lambda:FunctionUrlAuthType from the stored config.
func urlConfigStoredChecks(c *checkSet, op opID) bool {
	onTarget(c, op)
	c.set(condFunctionARN, c.target())

	if mgr, ok := c.h.fn.(functionURLManager); ok {
		if cfg, err := mgr.GetFunctionURLConfig(c.r.Context(), c.args.name, c.args.qualifier); err == nil {
			c.set(condFunctionURLAuth, cfg.AuthType)
		}
	}

	return true
}

// functionURLInvokeChecks: a call through a function URL needs
// lambda:InvokeFunctionUrl and lambda:InvokeFunction on the URL's function
// (Lambda Developer Guide, "Control access to Lambda function URLs"). Either
// can also be granted by the function's resource-based policy, which the
// handler checks (authorizeURLInvoke). A host with no URL is answered 404.
func functionURLInvokeChecks(c *checkSet, _ opID) bool {
	cfg, ok := c.h.resolveURL(c.r)
	if !ok {
		return false
	}

	arn := c.functionARN(cfg.FunctionName, cfg.Qualifier)

	c.add(actionInvokeFunctionURL, arn, awsauthz.ResourcePolicy)
	c.add(actionInvokeFunction, arn, awsauthz.ResourcePolicy)
	c.set(condFunctionARN, arn)
	c.set(condFunctionURLAuth, cfg.AuthType)
	c.set(condInvokedViaURL, condTrue)
	c.resourceTags(cfg.FunctionName)

	return true
}
