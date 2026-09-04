// Package lambda implements the AWS Lambda REST+JSON control-plane protocol
// as a server.Handler. Point a real aws-sdk-go-v2 Lambda client at a Server
// registered with this handler and operations work against an in-memory
// serverless driver.
//
// Coverage: CreateFunction, GetFunction, ListFunctions, DeleteFunction,
// Invoke (synchronous), UpdateFunctionConfiguration, PublishVersion /
// ListVersionsByFunction, the alias lifecycle (create/get/list/update/
// delete), and resource policies (AddPermission / GetPolicy /
// RemovePermission), and tagging (TagResource / UntagResource / ListTags at
// the /2017-03-31/tags prefix). Reserved concurrency (Put/Get/Delete
// FunctionConcurrency), layers (Publish/Get/List/Delete layer versions,
// GetLayerVersionByArn, ListLayers, and layer-version resource policies via
// Add/Get/RemoveLayerVersionPermission), and event source mappings are also
// wired through.
package lambda

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// pathPrefix is the Lambda API version prefix every control-plane URL starts
// with. We match on this so the handler doesn't accidentally swallow generic
// REST traffic that should fall through to the S3 catch-all.
const pathPrefix = "/2015-03-31/functions"

// tagsPrefix is the Lambda tagging API prefix (TagResource / UntagResource /
// ListTags). It's a different version prefix than the function control plane,
// so it needs its own Matches clause — otherwise tag requests fall through to
// the S3 catch-all and return a 405 HTML body the SDK can't deserialize.
const tagsPrefix = "/2017-03-31/tags"

// esmPrefix is the Lambda event-source-mapping API prefix (SQS/DynamoDB-stream
// -> Lambda triggers). Its own version prefix, so it needs a Matches clause.
const esmPrefix = "/2015-03-31/event-source-mappings"

// Reserved-concurrency prefixes. AWS versions Put/DeleteFunctionConcurrency
// under 2017-10-31 and GetFunctionConcurrency under 2019-09-30, all on the
// {name}/concurrency sub-resource — each needs its own Matches clause.
const (
	concurrencyWritePrefix = "/2017-10-31/functions" // Put + Delete
	concurrencyReadPrefix  = "/2019-09-30/functions" // Get
	concurrencySuffix      = "/concurrency"
)

// layersPrefix is the Lambda layers API prefix. Layers are a sibling of
// functions (not a function sub-resource), so they route on their own prefix.
const layersPrefix = "/2018-10-31/layers"

// functionURLPrefix is the Lambda Function URL API prefix. The URL config is a
// function sub-resource (.../{name}/url and .../{name}/urls) but versioned under
// 2021-10-31, so it needs its own Matches clause.
const functionURLPrefix = "/2021-10-31/functions"

// functionURLHostMarker identifies an inbound request as an invoke through a
// generated Function URL rather than a control-plane call: real Lambda
// Function URLs are addressed by a unique per-config host
// (<url-id>.lambda-url.<region>.on.aws), not by path, so this routes on the
// Host header instead of the URL path every other Lambda operation uses.
const functionURLHostMarker = ".lambda-url."

// isFunctionURLHost reports whether host (an incoming request's Host header,
// optionally with a port) addresses a generated Function URL.
func isFunctionURLHost(host string) bool {
	return strings.Contains(requestHost(host), functionURLHostMarker)
}

// requestHost strips an optional port from an HTTP Host header and lower-cases
// the remainder.
func requestHost(host string) string {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}

	return strings.ToLower(h)
}

// Function code-signing-config sub-resource (GetFunctionCodeSigningConfig et al).
// Versioned under 2020-06-30 on the {name}/code-signing-config sub-resource, so
// it needs its own Matches clause — otherwise the REST-JSON request falls through
// to the S3 catch-all, which returns XML the Lambda client can't parse. Terraform
// reads it on every function refresh.
const (
	codeSigningPrefix = "/2020-06-30/functions"
	codeSigningSuffix = "/code-signing-config"
)

// subVersions is the "versions" path segment shared by the function-versions
// route (/functions/{name}/versions) and the layer-versions routes.
const subVersions = "versions"

// Function sub-resource path segments that route both a collection/item verb
// (serveSubresource) and a sub-item verb (the partsSubItem switch).
const (
	subAliases = "aliases"
	subPolicy  = "policy"
)

// latestVersion is the symbolic version for a function's mutable current code.
const latestVersion = "$LATEST"

// packageTypeZip is the default deployment-package type. AWS requires Runtime
// and Handler for a Zip package (but not for an Image package).
const packageTypeZip = "Zip"

// invocationTypeEvent is the async (fire-and-forget) invocation type. AWS
// returns HTTP 202 with an empty body for it, versus 200 for RequestResponse.
const invocationTypeEvent = "Event"

// invocationTypeDryRun validates parameters and permissions without running the
// function. AWS returns HTTP 204 No Content (nothing is executed).
const invocationTypeDryRun = "DryRun"

// lastUpdateStatusSuccessful is the terminal LastUpdateStatus real AWS reports
// once a create/update completes. cloudemu settles synchronously, so every
// config response carries it — this is the value the FunctionUpdatedV2 waiter
// (SAM/CDK/Terraform) polls for.
const lastUpdateStatusSuccessful = "Successful"

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 6 << 20 // 6 MiB — Lambda's sync invocation payload limit.
)

// AWS Lambda configuration defaults the handler emits when the client omitted
// the field on create: Architectures defaults to ["x86_64"] and EphemeralStorage
// (the /tmp size) to 512 MB.
const (
	archX8664                 = "x86_64"
	defaultEphemeralStorageMB = 512
)

// EphemeralStorage accepted range: real Lambda rejects a size outside 512–10240
// with InvalidParameterValueException.
const (
	minEphemeralStorageMB = 512
	maxEphemeralStorageMB = 10240
)

// defaultLayerCodeSize is the CodeSize reported for an imported layer whose
// content was not published to this emulator (e.g. an S3-sourced layer we did
// not fetch), so the Layers list still carries a non-zero size.
const defaultLayerCodeSize int64 = 1024

// policyManager is the AWS-specific resource-policy surface (AddPermission /
// GetPolicy / RemovePermission). It's not part of the portable Serverless
// driver — resource policies are a Lambda concept — so the handler type-asserts
// for it rather than requiring every cloud's function provider to implement it.
type policyManager interface {
	AddPermission(ctx context.Context, functionName, qualifier string, stmt sdrv.PermissionStatement) error
	RemovePermission(ctx context.Context, functionName, qualifier, statementID string) error
	GetPolicy(ctx context.Context, functionName, qualifier string) (string, error)
}

// layerPolicyManager is the AWS-specific layer-version resource-policy surface
// (AddLayerVersionPermission / GetLayerVersionPolicy /
// RemoveLayerVersionPermission). Like policyManager, it's not part of the
// portable Serverless driver — layer version permissions are a Lambda concept —
// so the handler type-asserts for it rather than requiring every cloud's
// function provider to implement it.
type layerPolicyManager interface {
	AddLayerVersionPermission(
		ctx context.Context, name string, version int, stmt sdrv.LayerPermissionStatement, revisionID string,
	) (statementJSON, newRevisionID string, err error)
	RemoveLayerVersionPermission(ctx context.Context, name string, version int, statementID, revisionID string) error
	GetLayerVersionPolicy(ctx context.Context, name string, version int) (policy, revisionID string, err error)
}

// functionTagger is the AWS-specific Lambda tagging surface (not part of the
// portable Serverless driver), asserted the same way as policyManager.
type functionTagger interface {
	TagFunction(ctx context.Context, name string, tags map[string]string) error
	UntagFunction(ctx context.Context, name string, keys []string) error
	ListFunctionTags(ctx context.Context, name string) (map[string]string, error)
}

// awsConfigManager is the AWS-specific surface for the Lambda VpcConfig,
// DeadLetterConfig and TracingConfig settings. These have no Azure Functions or
// GCP Cloud Functions equivalent, so they are kept off the portable Serverless
// interface and asserted the same way as policyManager / functionURLManager.
type awsConfigManager interface {
	SetFunctionAWSConfig(ctx context.Context, name string, cfg sdrv.AWSFunctionConfig, create bool) error
	GetFunctionAWSConfig(ctx context.Context, name string) (sdrv.AWSFunctionConfig, error)
}

// ObjectStore is the slice of the in-process S3 backend the handler needs to
// fetch an S3-sourced deployment package (Code.S3Bucket/S3Key).
type ObjectStore interface {
	GetObject(ctx context.Context, bucket, key string) (*storagedriver.Object, error)
}

// Handler serves AWS Lambda REST requests against a serverless.Serverless
// driver.
type Handler struct {
	fn sdrv.Serverless
	// objects fetches an S3-sourced deployment package from the in-process S3
	// backend. Nil when no S3 backend is wired; an S3-sourced deploy then fails
	// loudly rather than silently falling back to the echo stub.
	objects ObjectStore
	// mu guards layerContent.
	mu sync.Mutex
	// layerContent stages each published layer version's zip bytes, keyed by
	// "name:version", so a function importing the layer can have its files
	// overlaid into the deployment package.
	layerContent map[string][]byte
}

// Option configures a Handler.
type Option func(*Handler)

// WithObjectStore lets CreateFunction fetch an S3-sourced deployment package
// (Code.S3Bucket/S3Key) from the in-process S3 backend so real code runs.
func WithObjectStore(s ObjectStore) Option {
	return func(h *Handler) { h.objects = s }
}

// New returns a Lambda handler backed by fn.
func New(fn sdrv.Serverless, opts ...Option) *Handler {
	h := &Handler{fn: fn, layerContent: make(map[string][]byte)}
	for _, opt := range opts {
		opt(h)
	}

	return h
}

// Matches returns true for any URL under /2015-03-31/functions — that's the
// Lambda control-plane prefix the SDK uses for every operation in our MVP —
// plus a request addressed to a generated Function URL host.
func (*Handler) Matches(r *http.Request) bool {
	return isFunctionURLHost(r.Host) ||
		strings.HasPrefix(r.URL.Path, pathPrefix) ||
		strings.HasPrefix(r.URL.Path, tagsPrefix) ||
		strings.HasPrefix(r.URL.Path, esmPrefix) ||
		strings.HasPrefix(r.URL.Path, concurrencyWritePrefix) ||
		strings.HasPrefix(r.URL.Path, concurrencyReadPrefix) ||
		strings.HasPrefix(r.URL.Path, layersPrefix) ||
		strings.HasPrefix(r.URL.Path, functionURLPrefix) ||
		strings.HasPrefix(r.URL.Path, eventInvokeConfigPrefix) ||
		isCodeSigningPath(r.URL.Path)
}

// ServeHTTP dispatches Lambda operations based on path shape and method.
//
//	/2015-03-31/functions                       GET=list, POST=create
//	/2015-03-31/functions/{name}                GET=get, DELETE=delete
//	/2015-03-31/functions/{name}/invocations    POST=invoke
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isFunctionURLHost(r.Host) {
		h.serveFunctionURLInvoke(w, r)
		return
	}

	if h.routePrefixed(w, r) {
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, pathPrefix)
	rest = strings.TrimPrefix(rest, "/")

	if rest == "" {
		h.serveCollection(w, r)
		return
	}

	parts := strings.Split(rest, "/")
	name := parts[0]

	const (
		partsResource    = 1 // /functions/{name}
		partsSubresource = 2 // /functions/{name}/{sub}
		partsSubItem     = 3 // /functions/{name}/{sub}/{id}
	)

	switch len(parts) {
	case partsResource:
		h.serveResource(w, r, name)
	case partsSubresource:
		h.serveSubresource(w, r, name, parts[1])
	case partsSubItem:
		switch parts[1] {
		case subAliases:
			h.serveAlias(w, r, name, parts[2])
		case subPolicy:
			h.serveRemovePermission(w, r, name, parts[2])
		default:
			writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
		}
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
	}
}

// routePrefixed serves the Lambda sub-APIs that live on their own version
// prefix (tags, event-source-mappings, layers, reserved concurrency). It
// returns true when it has handled the request, false to fall through to the
// /2015-03-31/functions control plane.
func (h *Handler) routePrefixed(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case strings.HasPrefix(r.URL.Path, tagsPrefix):
		arn := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, tagsPrefix), "/")
		h.serveTags(w, r, arn)
	case strings.HasPrefix(r.URL.Path, esmPrefix):
		uuid := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, esmPrefix), "/")
		h.serveEventSourceMappings(w, r, uuid)
	case strings.HasPrefix(r.URL.Path, layersPrefix):
		h.serveLayers(w, r)
	case strings.HasPrefix(r.URL.Path, functionURLPrefix):
		h.serveFunctionURL(w, r)
	case strings.HasPrefix(r.URL.Path, eventInvokeConfigPrefix):
		h.serveEventInvokeConfig(w, r)
	case isProvisionedConcurrencyPath(r.URL.Path):
		h.serveProvisionedConcurrency(w, r)
	case isCodeSigningPath(r.URL.Path):
		h.serveFunctionCodeSigningConfig(w, r)
	default:
		name, ok := concurrencyFunctionName(r.URL.Path)
		if !ok {
			return false
		}

		h.serveConcurrency(w, r, name)
	}

	return true
}

// serveSubresource dispatches /functions/{name}/{sub} paths.
func (h *Handler) serveSubresource(w http.ResponseWriter, r *http.Request, name, sub string) {
	switch sub {
	case "invocations":
		h.serveInvoke(w, r, name)
	case "configuration":
		h.serveConfiguration(w, r, name)
	case "code":
		h.serveCode(w, r, name)
	case subVersions:
		h.serveVersions(w, r, name)
	case subAliases:
		h.serveAliases(w, r, name)
	case subPolicy:
		h.servePolicy(w, r, name)
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
	}
}

// serveTags handles the Lambda tagging API at /2017-03-31/tags/{arn}:
// POST=TagResource, DELETE=UntagResource (?tagKeys=...), GET=ListTags.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, arn string) {
	tagger, ok := h.fn.(functionTagger)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "tagging not supported")
		return
	}

	name := functionNameFromARN(arn)

	switch r.Method {
	case http.MethodPost:
		var req struct {
			Tags map[string]string `json:"Tags"`
		}

		if !decodeJSON(w, r, &req) {
			return
		}

		if err := tagger.TagFunction(r.Context(), name, req.Tags); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := tagger.UntagFunction(r.Context(), name, r.URL.Query()["tagKeys"]); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		tags, err := tagger.ListFunctionTags(r.Context(), name)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"Tags": tags})
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// functionNameFromARN extracts the function name from a Lambda ARN
// (arn:aws:lambda:<region>:<account>:function:<name>). A value that isn't an
// ARN is returned unchanged.
func functionNameFromARN(arn string) string {
	const marker = ":function:"

	if i := strings.LastIndex(arn, marker); i >= 0 {
		return arn[i+len(marker):]
	}

	return arn
}

// splitFunctionNameQualifier extracts the bare function name and an optional
// version/alias qualifier from an Invoke FunctionName path segment. Per the
// Lambda Invoke API's FunctionName pattern, a version or alias can be
// appended with ":<qualifier>" to any accepted form: a bare name
// ("my-fn:PROD"), a full ARN ("arn:aws:lambda:region:account:function:my-fn:PROD"),
// or a partial ARN ("account:function:my-fn:PROD"). Everything through the
// "function:" marker (if present) is the function name; a colon after that is
// the qualifier boundary.
func splitFunctionNameQualifier(raw string) (name, qualifier string) {
	const marker = ":function:"

	rest := raw
	if i := strings.LastIndex(raw, marker); i >= 0 {
		rest = raw[i+len(marker):]
	}

	if j := strings.IndexByte(rest, ':'); j >= 0 {
		return rest[:j], rest[j+1:]
	}

	return rest, ""
}

// servePolicy handles POST (AddPermission) and GET (GetPolicy) on
// .../{name}/policy.
func (h *Handler) servePolicy(w http.ResponseWriter, r *http.Request, name string) {
	pm, ok := h.fn.(policyManager)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "resource policies not supported")
		return
	}

	qualifier := r.URL.Query().Get("Qualifier")

	switch r.Method {
	case http.MethodPost:
		var req addPermissionRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		err := pm.AddPermission(r.Context(), name, qualifier, sdrv.PermissionStatement{
			StatementID: req.StatementID, Action: req.Action,
			Principal: req.Principal, SourceARN: req.SourceArn,
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		// Echo the statement exactly as GetPolicy renders it (correct per-type
		// Principal shape, qualified Resource) rather than re-deriving it here, so
		// the AddPermission response and a subsequent GetPolicy agree.
		stmt, err := addedStatement(r.Context(), pm, name, qualifier, req.StatementID)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{"Statement": stmt})
	case http.MethodGet:
		policy, err := pm.GetPolicy(r.Context(), name, qualifier)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"Policy": policy, "RevisionId": "1"})
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// addedStatement returns the JSON of the single statement just added, pulled
// back out of the qualifier-scoped policy so the AddPermission echo matches the
// GetPolicy rendering (Principal shape, Resource ARN) instead of duplicating it.
func addedStatement(ctx context.Context, pm policyManager, name, qualifier, statementID string) (string, error) {
	policy, err := pm.GetPolicy(ctx, name, qualifier)
	if err != nil {
		return "", err
	}

	var doc struct {
		Statement []json.RawMessage `json:"Statement"`
	}

	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		return "", err
	}

	for _, raw := range doc.Statement {
		var peek struct {
			Sid string `json:"Sid"`
		}

		if json.Unmarshal(raw, &peek) == nil && peek.Sid == statementID {
			return string(raw), nil
		}
	}

	return "", cerrors.Newf(cerrors.NotFound, "statement %s not found", statementID)
}

// serveRemovePermission handles DELETE .../{name}/policy/{statementId}.
func (h *Handler) serveRemovePermission(w http.ResponseWriter, r *http.Request, name, statementID string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
		return
	}

	pm, ok := h.fn.(policyManager)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "resource policies not supported")
		return
	}

	if err := pm.RemovePermission(r.Context(), name, r.URL.Query().Get("Qualifier"), statementID); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// serveConfiguration handles .../{name}/configuration: GET is
// GetFunctionConfiguration (the op FunctionActiveV2 / FunctionUpdatedV2 waiters
// poll — a 405 here hangs every Terraform/SAM/CDK deploy), PUT is
// UpdateFunctionConfiguration.
func (h *Handler) serveConfiguration(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method == http.MethodGet {
		cfg, err := h.resolvedConfiguration(r.Context(), name, r.URL.Query().Get("Qualifier"))
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, cfg)

		return
	}

	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
		return
	}

	var req updateFunctionConfigurationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.validateLayers(r.Context(), req.Layers); err != nil {
		writeErr(w, err)
		return
	}

	cfg := sdrv.FunctionConfig{
		Name:        name,
		Runtime:     req.Runtime,
		Handler:     req.Handler,
		Role:        req.Role,
		Description: req.Description,
		Memory:      req.MemorySize,
		Timeout:     req.Timeout,
	}
	if req.Environment != nil {
		cfg.Environment = req.Environment.Variables
	}

	info, err := h.fn.UpdateFunction(r.Context(), name, cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsCfg := h.applyAWSConfig(r.Context(), name, sdrv.AWSFunctionConfig{
		VPCConfig:        toDriverVPCConfig(req.VpcConfig),
		DeadLetterConfig: toDriverDeadLetter(req.DeadLetterConfig),
		TracingConfig:    toDriverTracing(req.TracingConfig),
		Layers:           h.resolveLayers(req.Layers),
	}, false)

	writeJSON(w, http.StatusOK, toConfiguration(info, awsCfg))
}

// writePublished publishes a new version of name and writes that version's
// configuration with the given status, or the underlying error. It is the shared
// tail of the Publish=true UpdateFunctionCode/UpdateFunctionConfiguration paths.
func (h *Handler) writePublished(
	ctx context.Context, w http.ResponseWriter, status int,
	name string, info *sdrv.FunctionInfo, awsCfg *sdrv.AWSFunctionConfig,
) {
	cfg, err := h.publishConfiguration(ctx, name, "", info, awsCfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, status, cfg)
}

// serveCode handles PUT .../{name}/code (UpdateFunctionCode). It resolves the
// new deployment package the same way create does — an inline ZipFile or an
// S3-sourced artifact fetched from the in-process S3 backend, with any layer
// content overlaid — then redeploys it to the engine via the provider so
// update-function-code runs the new real code instead of leaving the stale
// deployment in place. A request with no usable source is a hard error.
func (h *Handler) serveCode(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
		return
	}

	var req updateFunctionCodeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	code, err := h.resolveCode(r.Context(), &functionCode{
		ZipFile: req.ZipFile, S3Bucket: req.S3Bucket, S3Key: req.S3Key,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	code, err = h.overlayLayers(code, req.Layers)
	if err != nil {
		writeErr(w, err)
		return
	}

	if len(code) == 0 {
		writeError(w, http.StatusBadRequest, "InvalidParameterValueException",
			"UpdateFunctionCode requires a deployment package (ZipFile or S3Bucket/S3Key)")
		return
	}

	info, err := h.fn.UpdateFunction(r.Context(), name, sdrv.FunctionConfig{Name: name, Code: code})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsCfg := h.awsFnConfig(r.Context(), name)

	if req.Publish {
		h.writePublished(r.Context(), w, http.StatusOK, name, info, awsCfg)
		return
	}

	writeJSON(w, http.StatusOK, toConfiguration(info, awsCfg))
}

// serveVersions handles POST (PublishVersion) and GET (ListVersionsByFunction)
// on .../{name}/versions.
func (h *Handler) serveVersions(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPost:
		var req publishVersionRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		ver, err := h.fn.PublishVersion(r.Context(), name, req.Description)
		if err != nil {
			writeErr(w, err)
			return
		}

		info, err := h.fn.GetFunction(r.Context(), name)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toVersionConfiguration(info, ver, h.awsFnConfig(r.Context(), name)))
	case http.MethodGet:
		vers, err := h.fn.ListVersions(r.Context(), name)
		if err != nil {
			writeErr(w, err)
			return
		}

		info, err := h.fn.GetFunction(r.Context(), name)
		if err != nil {
			writeErr(w, err)
			return
		}

		awsCfg := h.awsFnConfig(r.Context(), name)

		// Sort by version so Marker offsets stay stable across paginated calls
		// (ListVersions returns versions in publish order, but page deterministically).
		sort.Slice(vers, func(i, j int) bool { return vers[i].Version < vers[j].Version })

		start, end, nextMarker, _ := pageWindow(len(vers), r.URL.Query())

		out := listVersionsResponse{Versions: make([]functionConfiguration, 0, end-start)}
		for i := start; i < end; i++ {
			out.Versions = append(out.Versions, toVersionConfiguration(info, &vers[i], awsCfg))
		}

		out.NextMarker = nextMarker

		writeJSON(w, http.StatusOK, out)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// serveAliases handles POST (CreateAlias) and GET (ListAliases) on
// .../{name}/aliases.
func (h *Handler) serveAliases(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPost:
		var req aliasRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		a, err := h.fn.CreateAlias(r.Context(), sdrv.AliasConfig{
			FunctionName: name, Name: req.Name,
			FunctionVersion: req.FunctionVersion, Description: req.Description,
			RoutingConfig: toRoutingConfig(req.RoutingConfig),
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toAliasResponse(a))
	case http.MethodGet:
		aliases, err := h.fn.ListAliases(r.Context(), name)
		if err != nil {
			writeErr(w, err)
			return
		}

		// Sort by name so Marker offsets stay stable across paginated calls.
		sort.Slice(aliases, func(i, j int) bool { return aliases[i].Name < aliases[j].Name })

		start, end, nextMarker, _ := pageWindow(len(aliases), r.URL.Query())

		out := listAliasesResponse{Aliases: make([]aliasResponse, 0, end-start)}
		for i := start; i < end; i++ {
			out.Aliases = append(out.Aliases, toAliasResponse(&aliases[i]))
		}

		out.NextMarker = nextMarker

		writeJSON(w, http.StatusOK, out)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// serveAlias handles GET/PUT/DELETE on .../{name}/aliases/{aliasName}.
func (h *Handler) serveAlias(w http.ResponseWriter, r *http.Request, name, aliasName string) {
	switch r.Method {
	case http.MethodGet:
		a, err := h.fn.GetAlias(r.Context(), name, aliasName)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toAliasResponse(a))
	case http.MethodPut:
		var req aliasRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		a, err := h.fn.UpdateAlias(r.Context(), sdrv.AliasConfig{
			FunctionName: name, Name: aliasName,
			FunctionVersion: req.FunctionVersion, Description: req.Description,
			RoutingConfig: toRoutingConfig(req.RoutingConfig),
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toAliasResponse(a))
	case http.MethodDelete:
		if err := h.fn.DeleteAlias(r.Context(), name, aliasName); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// toRoutingConfig maps the wire AliasRoutingConfiguration (a map of additional
// version -> weight) to the driver shape, preserving the full weights map so
// every additional version reaches the backend's weighted-routing selection.
func toRoutingConfig(rc *aliasRoutingConfig) *sdrv.AliasRoutingConfig {
	if rc == nil || len(rc.AdditionalVersionWeights) == 0 {
		return nil
	}

	weights := make(map[string]float64, len(rc.AdditionalVersionWeights))
	for version, weight := range rc.AdditionalVersionWeights {
		weights[version] = weight
	}

	return &sdrv.AliasRoutingConfig{AdditionalVersionWeights: weights}
}

func toAliasResponse(a *sdrv.Alias) aliasResponse {
	resp := aliasResponse{
		AliasArn:        a.AliasARN,
		Name:            a.Name,
		FunctionVersion: a.FunctionVersion,
		Description:     a.Description,
		RevisionID:      a.RevisionID,
	}

	if a.RoutingConfig != nil && len(a.RoutingConfig.AdditionalVersionWeights) > 0 {
		weights := make(map[string]float64, len(a.RoutingConfig.AdditionalVersionWeights))
		for version, weight := range a.RoutingConfig.AdditionalVersionWeights {
			weights[version] = weight
		}

		resp.RoutingConfig = &aliasRoutingConfig{AdditionalVersionWeights: weights}
	}

	return resp
}

func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.create(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

func (h *Handler) serveResource(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.get(w, r, name)
	case http.MethodDelete:
		h.delete(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

func (h *Handler) serveInvoke(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
		return
	}

	h.invoke(w, r, name)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createFunctionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := validateCreateRequest(&req); err != nil {
		writeErr(w, err)
		return
	}

	if err := h.validateLayers(r.Context(), req.Layers); err != nil {
		writeErr(w, err)
		return
	}

	cfg := sdrv.FunctionConfig{
		Name:        req.FunctionName,
		Runtime:     req.Runtime,
		Handler:     req.Handler,
		Role:        req.Role,
		Description: req.Description,
		Memory:      req.MemorySize,
		Timeout:     req.Timeout,
		Tags:        req.Tags,
	}
	if req.Environment != nil {
		cfg.Environment = req.Environment.Variables
	}

	code, err := h.resolveCode(r.Context(), req.Code)
	if err != nil {
		writeErr(w, err)
		return
	}

	code, err = h.overlayLayers(code, req.Layers)
	if err != nil {
		writeErr(w, err)
		return
	}

	cfg.Code = code

	info, err := h.fn.CreateFunction(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsCfg := h.applyAWSConfig(r.Context(), req.FunctionName, h.createAWSConfig(&req), true)

	// Publish=true cuts version 1 from the just-created function and returns that
	// published version's configuration (Version "1", :1-qualified ARN), matching
	// AWS and Terraform's aws_lambda_function{publish=true}.
	if req.Publish {
		cfg, perr := h.publishConfiguration(r.Context(), req.FunctionName, req.Description, info, awsCfg)
		if perr != nil {
			writeErr(w, perr)
			return
		}

		writeJSON(w, http.StatusCreated, cfg)

		return
	}

	writeJSON(w, http.StatusCreated, toConfiguration(info, awsCfg))
}

// validateCreateRequest enforces the CreateFunction input rules the emulator
// checks up front: a .zip package requires Runtime and Handler, and an
// EphemeralStorage size must be within the accepted range. Each violation is an
// InvalidArgument error the wire layer maps to InvalidParameterValueException.
func validateCreateRequest(req *createFunctionRequest) error {
	if req.PackageType == "" || req.PackageType == packageTypeZip {
		if req.Runtime == "" {
			return cerrors.New(cerrors.InvalidArgument,
				"Runtime is required if the deployment package is a .zip file archive.")
		}

		if req.Handler == "" {
			return cerrors.New(cerrors.InvalidArgument,
				"Handler is required if the deployment package is a .zip file archive.")
		}
	}

	return validateEphemeralStorage(req.EphemeralStorage)
}

// validateEphemeralStorage rejects a /tmp size outside the AWS 512–10240 MB range.
func validateEphemeralStorage(e *ephemeralStorageEnvelope) error {
	if e == nil {
		return nil
	}

	if e.Size < minEphemeralStorageMB || e.Size > maxEphemeralStorageMB {
		return cerrors.Newf(cerrors.InvalidArgument,
			"'ephemeralStorage.size' value %d must be >= %d and <= %d",
			e.Size, minEphemeralStorageMB, maxEphemeralStorageMB)
	}

	return nil
}

// createAWSConfig assembles the AWS-only settings supplied on a CreateFunction
// request (VpcConfig/DeadLetterConfig/TracingConfig plus Architectures,
// EphemeralStorage and the imported Layers) for applyAWSConfig to store.
func (h *Handler) createAWSConfig(req *createFunctionRequest) sdrv.AWSFunctionConfig {
	return sdrv.AWSFunctionConfig{
		VPCConfig:        toDriverVPCConfig(req.VpcConfig),
		DeadLetterConfig: toDriverDeadLetter(req.DeadLetterConfig),
		TracingConfig:    toDriverTracing(req.TracingConfig),
		Architectures:    req.Architectures,
		EphemeralStorage: toDriverEphemeral(req.EphemeralStorage),
		Layers:           h.resolveLayers(req.Layers),
	}
}

// publishConfiguration publishes a new version of name and renders that version's
// configuration (its own version number and a qualified ARN), the response body
// AWS returns for a Publish=true Create/UpdateFunctionCode/Configuration.
func (h *Handler) publishConfiguration(
	ctx context.Context, name, description string,
	info *sdrv.FunctionInfo, awsCfg *sdrv.AWSFunctionConfig,
) (functionConfiguration, error) {
	ver, err := h.fn.PublishVersion(ctx, name, description)
	if err != nil {
		return functionConfiguration{}, err
	}

	return toVersionConfiguration(info, ver, awsCfg), nil
}

// validateLayers checks that every layer version ARN a CreateFunction /
// UpdateFunctionConfiguration request references actually exists, matching
// real Lambda, which rejects an unknown or malformed layer version ARN with
// InvalidParameterValueException instead of silently accepting it.
func (h *Handler) validateLayers(ctx context.Context, arns []string) error {
	for _, arn := range arns {
		name, version, ok := parseLayerARN(arn)
		if !ok {
			return cerrors.Newf(cerrors.InvalidArgument, "Layer version %s is not a valid layer version ARN", arn)
		}

		if _, err := h.fn.GetLayerVersion(ctx, name, version); err != nil {
			return cerrors.Newf(cerrors.InvalidArgument, "Layer version %s does not exist", arn)
		}
	}

	return nil
}

// resolveLayers maps the requested layer ARNs to the echoed Layers list,
// resolving each layer version's CodeSize from its staged content when the layer
// was published to this emulator, or a sane default otherwise.
func (h *Handler) resolveLayers(arns []string) []sdrv.FunctionLayer {
	if len(arns) == 0 {
		return nil
	}

	out := make([]sdrv.FunctionLayer, 0, len(arns))

	for _, arn := range arns {
		size := int64(len(h.layerContentFor(arn)))
		if size == 0 {
			size = defaultLayerCodeSize
		}

		out = append(out, sdrv.FunctionLayer{ARN: arn, CodeSize: size})
	}

	return out
}

// resolveCode returns the deployment-package bytes for a CreateFunction request.
// An inline Code.ZipFile wins; otherwise an S3Bucket/S3Key pair is fetched from
// the in-process S3 backend so a function deployed the way Terraform/SAM/CDK
// deploy it (an uploaded artifact, not an inline zip) runs real code instead of
// silently falling back to the echo stub. A code that references S3 with no S3
// backend wired is a hard error rather than a silent stub.
func (h *Handler) resolveCode(ctx context.Context, code *functionCode) ([]byte, error) {
	if code == nil {
		return nil, nil
	}

	if len(code.ZipFile) > 0 {
		return code.ZipFile, nil
	}

	if code.S3Bucket == "" && code.S3Key == "" {
		return nil, nil
	}

	if code.S3Bucket == "" || code.S3Key == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "Code.S3Bucket and Code.S3Key must both be set")
	}

	if h.objects == nil {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"function code references S3 but no S3 backend is wired; deploy with an inline Code.ZipFile")
	}

	obj, err := h.objects.GetObject(ctx, code.S3Bucket, code.S3Key)
	if err != nil {
		return nil, err
	}

	return obj.Data, nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, name string) {
	info, err := h.fn.GetFunction(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	cfg, err := h.resolvedConfiguration(r.Context(), name, r.URL.Query().Get("Qualifier"))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, functionResource{
		Configuration: cfg,
		Code: codeLocation{
			RepositoryType: "S3",
			Location:       "https://cloudemu-mock/" + name,
		},
		Concurrency: h.reservedConcurrency(r.Context(), name),
		Tags:        info.Tags,
	})
}

// resolvedConfiguration renders a function's configuration for an optional
// Qualifier. An empty or "$LATEST" qualifier returns the mutable $LATEST config;
// a version number returns that published version's snapshot (its own
// CodeSha256/Runtime/Timeout and a version-qualified ARN); an alias resolves to
// its target version's config but keeps the alias-qualified ARN. A qualifier
// that names neither an alias nor a published version is a
// ResourceNotFoundException, matching real Lambda.
func (h *Handler) resolvedConfiguration(ctx context.Context, name, qualifier string) (functionConfiguration, error) {
	info, err := h.fn.GetFunction(ctx, name)
	if err != nil {
		return functionConfiguration{}, err
	}

	awsCfg := h.awsFnConfig(ctx, name)

	if qualifier == "" || qualifier == latestVersion {
		return toConfiguration(info, awsCfg), nil
	}

	// An alias resolves one hop to its target version's config; the ARN keeps the
	// alias qualifier rather than the target version number.
	if a, aerr := h.fn.GetAlias(ctx, name, qualifier); aerr == nil {
		ver, verr := h.findVersion(ctx, name, a.FunctionVersion)
		if verr != nil {
			return functionConfiguration{}, verr
		}

		cfg := toVersionConfiguration(info, ver, awsCfg)
		cfg.FunctionArn = info.ARN + ":" + qualifier

		return cfg, nil
	}

	ver, verr := h.findVersion(ctx, name, qualifier)
	if verr != nil {
		return functionConfiguration{}, cerrors.Newf(cerrors.NotFound,
			"function version or alias %q not found for %s", qualifier, name)
	}

	return toVersionConfiguration(info, ver, awsCfg), nil
}

// findVersion returns the published version (or $LATEST) snapshot matching
// version, or a NotFound error.
func (h *Handler) findVersion(ctx context.Context, name, version string) (*sdrv.FunctionVersion, error) {
	vers, err := h.fn.ListVersions(ctx, name)
	if err != nil {
		return nil, err
	}

	for i := range vers {
		if vers[i].Version == version {
			return &vers[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "version %s not found for %s", version, name)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	infos, err := h.fn.ListFunctions(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	// Sort by name so Marker offsets stay stable across paginated calls (the
	// driver returns functions in map-iteration order, which is unstable).
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })

	start, end, nextMarker, _ := pageWindow(len(infos), r.URL.Query())

	out := listFunctionsResponse{Functions: make([]functionConfiguration, 0, end-start)}
	for i := start; i < end; i++ {
		out.Functions = append(out.Functions, toConfiguration(&infos[i], h.awsFnConfig(r.Context(), infos[i].Name)))
	}

	out.NextMarker = nextMarker

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.fn.DeleteFunction(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) invoke(w http.ResponseWriter, r *http.Request, name string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "RequestEntityTooLargeException", err.Error())
		return
	}

	// FunctionName may carry its own ":<qualifier>" suffix (bare name, full ARN,
	// or partial ARN all accept one — see the Invoke API's FunctionName pattern);
	// the Qualifier query parameter is the other place AWS accepts one and, when
	// both are present, wins.
	functionName, qualifier := splitFunctionNameQualifier(name)
	if q := r.URL.Query().Get("Qualifier"); q != "" {
		qualifier = q
	}

	invokeType := r.Header.Get("X-Amz-Invocation-Type")

	// A DryRun invocation validates the request without executing the function:
	// confirm the function AND the qualifier (alias/version) exist, then return
	// 204 No Content (real Lambda runs nothing and returns no payload). Routing
	// through Invoke reuses the driver's qualifier resolution, so an unknown
	// alias/version fails with ResourceNotFoundException rather than a spurious
	// 204.
	if invokeType == invocationTypeDryRun {
		if _, err = h.fn.Invoke(r.Context(), sdrv.InvokeInput{
			FunctionName: functionName,
			InvokeType:   invocationTypeDryRun,
			Qualifier:    qualifier,
		}); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)

		return
	}

	out, err := h.fn.Invoke(r.Context(), sdrv.InvokeInput{
		FunctionName: functionName,
		Payload:      payload,
		InvokeType:   invokeType,
		Qualifier:    qualifier,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	// An asynchronous (Event) invocation is fire-and-forget: AWS queues it and
	// returns HTTP 202 with an empty body — no payload, no function-error header.
	if invokeType == invocationTypeEvent {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", contentTypeJSON)

	// A synchronous (RequestResponse) invocation always reports the version that
	// ran via X-Amz-Executed-Version, which the SDK reads into
	// InvokeOutput.ExecutedVersion: the alias's target version when Qualifier
	// named an alias, the qualifier itself when it named a version, or $LATEST
	// for an unqualified invoke.
	executedVersion := out.ExecutedVersion
	if executedVersion == "" {
		executedVersion = latestVersion
	}

	w.Header().Set("X-Amz-Executed-Version", executedVersion)

	if out.Error != "" {
		// Lambda surfaces handler errors via the X-Amz-Function-Error header
		// while still returning HTTP 200 with the error payload as the body.
		w.Header().Set("X-Amz-Function-Error", "Unhandled")

		body, jerr := json.Marshal(map[string]string{
			"errorType":    "HandlerError",
			"errorMessage": out.Error,
		})
		if jerr != nil {
			body = []byte(`{"errorMessage":"unknown error"}`)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)

		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Payload)
}

// toVersionConfiguration renders a published version's configuration. It starts
// from the live function (for name/ARN/state/environment) then overlays the
// immutable per-version fields snapshotted at publish time, so each version
// reports its own CodeSha256/RevisionId/runtime rather than reusing $LATEST.
func toVersionConfiguration(info *sdrv.FunctionInfo, ver *sdrv.FunctionVersion, awsCfg *sdrv.AWSFunctionConfig) functionConfiguration {
	cfg := toConfiguration(info, awsCfg)
	cfg.Version = ver.Version
	cfg.Description = ver.Description
	cfg.CodeSha256 = ver.CodeSHA256
	cfg.RevisionID = ver.RevisionID
	cfg.Runtime = ver.Runtime
	cfg.Handler = ver.Handler
	cfg.Role = ver.Role
	cfg.MemorySize = ver.Memory
	cfg.Timeout = ver.Timeout

	// A published version's ARN is qualified with the version number; $LATEST
	// keeps the unqualified ARN.
	if ver.Version != "" && ver.Version != latestVersion {
		cfg.FunctionArn = info.ARN + ":" + ver.Version
	}

	return cfg
}

func toConfiguration(info *sdrv.FunctionInfo, awsCfg *sdrv.AWSFunctionConfig) functionConfiguration {
	version := info.Version
	if version == "" {
		version = latestVersion
	}

	cfg := functionConfiguration{
		FunctionName:     info.Name,
		FunctionArn:      info.ARN,
		Runtime:          info.Runtime,
		Role:             info.Role,
		Handler:          info.Handler,
		Description:      info.Description,
		MemorySize:       info.Memory,
		Timeout:          info.Timeout,
		LastModified:     info.LastModified,
		State:            info.State,
		LastUpdateStatus: lastUpdateStatusSuccessful,
		PackageType:      packageTypeZip,
		CodeSha256:       info.CodeSHA256,
		CodeSize:         info.CodeSize,
		Version:          version,
		RevisionID:       info.RevisionID,
	}

	if len(info.Environment) > 0 {
		cfg.Environment = &envEnvelope{Variables: info.Environment}
	}

	// AWS always reports Architectures and EphemeralStorage, defaulting to
	// ["x86_64"] and 512 MB when the function was created without them.
	cfg.Architectures = []string{archX8664}
	cfg.EphemeralStorage = &ephemeralStorageEnvelope{Size: defaultEphemeralStorageMB}

	applyAWSConfigToResponse(&cfg, awsCfg)

	return cfg
}

// applyAWSConfigToResponse overlays the stored AWS-only settings onto a function
// configuration response: the imported layers, plus VpcConfig/DeadLetterConfig/
// TracingConfig, and the non-default Architectures/EphemeralStorage.
func applyAWSConfigToResponse(cfg *functionConfiguration, awsCfg *sdrv.AWSFunctionConfig) {
	if awsCfg == nil {
		return
	}

	if len(awsCfg.Architectures) > 0 {
		cfg.Architectures = awsCfg.Architectures
	}

	if awsCfg.EphemeralStorage != nil {
		cfg.EphemeralStorage = &ephemeralStorageEnvelope{Size: awsCfg.EphemeralStorage.Size}
	}

	cfg.Layers = toLayerReferences(awsCfg.Layers)

	if awsCfg.VPCConfig != nil {
		cfg.VpcConfig = &vpcConfigEnvelope{
			SubnetIDs:        awsCfg.VPCConfig.SubnetIDs,
			SecurityGroupIDs: awsCfg.VPCConfig.SecurityGroupIDs,
			VpcID:            awsCfg.VPCConfig.VpcID,
		}
	}

	if awsCfg.DeadLetterConfig != nil {
		cfg.DeadLetterConfig = &deadLetterConfigEnvelope{TargetArn: awsCfg.DeadLetterConfig.TargetArn}
	}

	if awsCfg.TracingConfig != nil {
		cfg.TracingConfig = &tracingConfigEnvelope{Mode: awsCfg.TracingConfig.Mode}
	}
}

// reservedConcurrency returns the function's reserved-concurrency envelope for
// the GetFunction Concurrency field, or nil when no reserved concurrency has
// been set (GetFunctionConcurrency reports NotFound) — matching AWS, which omits
// the object until PutFunctionConcurrency has run.
func (h *Handler) reservedConcurrency(ctx context.Context, name string) *concurrencyEnvelope {
	cfg, err := h.fn.GetFunctionConcurrency(ctx, name)
	if err != nil || cfg == nil {
		return nil
	}

	return &concurrencyEnvelope{ReservedConcurrentExecutions: cfg.ReservedConcurrentExecutions}
}

// awsFnConfig returns the AWS-only Lambda settings (VpcConfig/DeadLetterConfig/
// TracingConfig) for a function, or nil when the backend does not model them (a
// non-AWS serverless provider). It is fetched through the AWS-only optional
// interface so these AWS-specific settings stay off the provider-agnostic
// Serverless surface.
func (h *Handler) awsFnConfig(ctx context.Context, name string) *sdrv.AWSFunctionConfig {
	mgr, ok := h.fn.(awsConfigManager)
	if !ok {
		return nil
	}

	cfg, err := mgr.GetFunctionAWSConfig(ctx, name)
	if err != nil {
		return nil
	}

	return &cfg
}

// applyAWSConfig stores the AWS-only settings supplied on a Create/Update
// request through the AWS-only optional interface, then returns the resulting
// stored config for the response. It is a no-op returning nil when the backend
// does not model these settings.
//
//nolint:gocritic // hugeParam: cfg mirrors the SetFunctionAWSConfig value receiver.
func (h *Handler) applyAWSConfig(
	ctx context.Context, name string, cfg sdrv.AWSFunctionConfig, create bool,
) *sdrv.AWSFunctionConfig {
	mgr, ok := h.fn.(awsConfigManager)
	if !ok {
		return nil
	}

	if err := mgr.SetFunctionAWSConfig(ctx, name, cfg, create); err != nil {
		return nil
	}

	stored, err := mgr.GetFunctionAWSConfig(ctx, name)
	if err != nil {
		return nil
	}

	return &stored
}

// toDriverVPCConfig maps a wire VpcConfig envelope to the driver type.
func toDriverVPCConfig(e *vpcConfigEnvelope) *sdrv.VPCConfig {
	if e == nil {
		return nil
	}

	return &sdrv.VPCConfig{SubnetIDs: e.SubnetIDs, SecurityGroupIDs: e.SecurityGroupIDs}
}

// toDriverDeadLetter maps a wire DeadLetterConfig envelope to the driver type.
func toDriverDeadLetter(e *deadLetterConfigEnvelope) *sdrv.DeadLetterConfig {
	if e == nil {
		return nil
	}

	return &sdrv.DeadLetterConfig{TargetArn: e.TargetArn}
}

// toDriverTracing maps a wire TracingConfig envelope to the driver type.
func toDriverTracing(e *tracingConfigEnvelope) *sdrv.TracingConfig {
	if e == nil {
		return nil
	}

	return &sdrv.TracingConfig{Mode: e.Mode}
}

// toDriverEphemeral maps a wire EphemeralStorage envelope to the driver type.
func toDriverEphemeral(e *ephemeralStorageEnvelope) *sdrv.EphemeralStorage {
	if e == nil {
		return nil
	}

	return &sdrv.EphemeralStorage{Size: e.Size}
}

// toLayerReferences maps the stored imported layers to the function
// configuration's Layers list.
func toLayerReferences(layers []sdrv.FunctionLayer) []layerReference {
	if len(layers) == 0 {
		return nil
	}

	out := make([]layerReference, 0, len(layers))
	for i := range layers {
		out = append(out, layerReference{Arn: layers[i].ARN, CodeSize: layers[i].CodeSize})
	}

	return out
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContentException", err.Error())
		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"Type":    errType,
		"Message": msg,
		"message": msg,
	})
}

func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", msg)
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ResourceConflictException", msg)
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidParameterValueException", msg)
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusPreconditionFailed, "PreconditionFailedException", msg)
	case cerrors.IsThrottled(err):
		writeThrottle(w, msg)
	default:
		writeError(w, http.StatusInternalServerError, "ServiceException", msg)
	}
}

// reservedConcurrencyReason is the Reason a reserved-concurrency throttle
// carries in the TooManyRequestsException body — the value real Lambda returns
// when a function's ReservedConcurrentExecutions limit is exhausted.
const reservedConcurrencyReason = "ReservedFunctionConcurrentInvocationLimitExceeded"

// writeThrottle emits the 429 TooManyRequestsException a throttled Invoke
// returns. The body carries the Reason field the SDK deserializes into
// TooManyRequestsException.Reason so callers can distinguish a reserved-
// concurrency throttle from other limits.
func writeThrottle(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", "TooManyRequestsException")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"Type":    "TooManyRequestsException",
		"Message": msg,
		"message": msg,
		"Reason":  reservedConcurrencyReason,
	})
}
