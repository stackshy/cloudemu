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
// the /2017-03-31/tags prefix). Layers, concurrency configs, and event source
// mappings remain deferred — the driver supports some of them but the wire
// surface is not yet wired through.
package lambda

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
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

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 6 << 20 // 6 MiB — Lambda's sync invocation payload limit.
)

// policyManager is the AWS-specific resource-policy surface (AddPermission /
// GetPolicy / RemovePermission). It's not part of the portable Serverless
// driver — resource policies are a Lambda concept — so the handler type-asserts
// for it rather than requiring every cloud's function provider to implement it.
type policyManager interface {
	AddPermission(ctx context.Context, functionName string, stmt sdrv.PermissionStatement) error
	RemovePermission(ctx context.Context, functionName, statementID string) error
	GetPolicy(ctx context.Context, functionName string) (string, error)
}

// functionTagger is the AWS-specific Lambda tagging surface (not part of the
// portable Serverless driver), asserted the same way as policyManager.
type functionTagger interface {
	TagFunction(ctx context.Context, name string, tags map[string]string) error
	UntagFunction(ctx context.Context, name string, keys []string) error
	ListFunctionTags(ctx context.Context, name string) (map[string]string, error)
}

// Handler serves AWS Lambda REST requests against a serverless.Serverless
// driver.
type Handler struct {
	fn sdrv.Serverless
}

// New returns a Lambda handler backed by fn.
func New(fn sdrv.Serverless) *Handler {
	return &Handler{fn: fn}
}

// Matches returns true for any URL under /2015-03-31/functions — that's the
// Lambda control-plane prefix the SDK uses for every operation in our MVP.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, pathPrefix) ||
		strings.HasPrefix(r.URL.Path, tagsPrefix) ||
		strings.HasPrefix(r.URL.Path, esmPrefix)
}

// ServeHTTP dispatches Lambda operations based on path shape and method.
//
//	/2015-03-31/functions                       GET=list, POST=create
//	/2015-03-31/functions/{name}                GET=get, DELETE=delete
//	/2015-03-31/functions/{name}/invocations    POST=invoke
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, tagsPrefix) {
		arn := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, tagsPrefix), "/")
		h.serveTags(w, r, arn)

		return
	}

	if strings.HasPrefix(r.URL.Path, esmPrefix) {
		uuid := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, esmPrefix), "/")
		h.serveEventSourceMappings(w, r, uuid)

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
		case "aliases":
			h.serveAlias(w, r, name, parts[2])
		case "policy":
			h.serveRemovePermission(w, r, name, parts[2])
		default:
			writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
		}
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
	}
}

// serveSubresource dispatches /functions/{name}/{sub} paths.
func (h *Handler) serveSubresource(w http.ResponseWriter, r *http.Request, name, sub string) {
	switch sub {
	case "invocations":
		h.serveInvoke(w, r, name)
	case "configuration":
		h.serveConfiguration(w, r, name)
	case "versions":
		h.serveVersions(w, r, name)
	case "aliases":
		h.serveAliases(w, r, name)
	case "policy":
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

// servePolicy handles POST (AddPermission) and GET (GetPolicy) on
// .../{name}/policy.
func (h *Handler) servePolicy(w http.ResponseWriter, r *http.Request, name string) {
	pm, ok := h.fn.(policyManager)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "resource policies not supported")
		return
	}

	switch r.Method {
	case http.MethodPost:
		var req addPermissionRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		err := pm.AddPermission(r.Context(), name, sdrv.PermissionStatement{
			StatementID: req.StatementID, Action: req.Action,
			Principal: req.Principal, SourceARN: req.SourceArn,
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		stmt, jerr := json.Marshal(map[string]any{
			"Sid":       req.StatementID,
			"Effect":    "Allow",
			"Principal": map[string]string{"Service": req.Principal},
			"Action":    req.Action,
		})
		if jerr != nil {
			writeErr(w, jerr)
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{"Statement": string(stmt)})
	case http.MethodGet:
		policy, err := pm.GetPolicy(r.Context(), name)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"Policy": policy, "RevisionId": "1"})
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
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

	if err := pm.RemovePermission(r.Context(), name, statementID); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// serveConfiguration handles PUT .../{name}/configuration
// (UpdateFunctionConfiguration).
func (h *Handler) serveConfiguration(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
		return
	}

	var req updateFunctionConfigurationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	cfg := sdrv.FunctionConfig{
		Name:    name,
		Runtime: req.Runtime,
		Handler: req.Handler,
		Memory:  req.MemorySize,
		Timeout: req.Timeout,
	}
	if req.Environment != nil {
		cfg.Environment = req.Environment.Variables
	}

	info, err := h.fn.UpdateFunction(r.Context(), name, cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toConfiguration(info))
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

		cfg := toConfiguration(info)
		cfg.Version = ver.Version
		cfg.Description = ver.Description
		writeJSON(w, http.StatusCreated, cfg)
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

		out := listVersionsResponse{Versions: make([]functionConfiguration, 0, len(vers))}
		for i := range vers {
			cfg := toConfiguration(info)
			cfg.Version = vers[i].Version
			cfg.Description = vers[i].Description
			out.Versions = append(out.Versions, cfg)
		}

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

		out := listAliasesResponse{Aliases: make([]aliasResponse, 0, len(aliases))}
		for i := range aliases {
			out.Aliases = append(out.Aliases, toAliasResponse(&aliases[i]))
		}

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

func toAliasResponse(a *sdrv.Alias) aliasResponse {
	return aliasResponse{
		AliasArn:        a.AliasARN,
		Name:            a.Name,
		FunctionVersion: a.FunctionVersion,
		Description:     a.Description,
	}
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

	cfg := sdrv.FunctionConfig{
		Name:    req.FunctionName,
		Runtime: req.Runtime,
		Handler: req.Handler,
		Memory:  req.MemorySize,
		Timeout: req.Timeout,
		Tags:    req.Tags,
	}
	if req.Environment != nil {
		cfg.Environment = req.Environment.Variables
	}

	info, err := h.fn.CreateFunction(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toConfiguration(info))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, name string) {
	info, err := h.fn.GetFunction(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, functionResource{
		Configuration: toConfiguration(info),
		Code: codeLocation{
			RepositoryType: "S3",
			Location:       "https://cloudemu-mock/" + name,
		},
		Tags: info.Tags,
	})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	infos, err := h.fn.ListFunctions(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listFunctionsResponse{Functions: make([]functionConfiguration, 0, len(infos))}
	for i := range infos {
		out.Functions = append(out.Functions, toConfiguration(&infos[i]))
	}

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

	out, err := h.fn.Invoke(r.Context(), sdrv.InvokeInput{
		FunctionName: name,
		Payload:      payload,
		InvokeType:   r.Header.Get("X-Amz-Invocation-Type"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("Content-Type", contentTypeJSON)

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

func toConfiguration(info *sdrv.FunctionInfo) functionConfiguration {
	cfg := functionConfiguration{
		FunctionName: info.Name,
		FunctionArn:  info.ARN,
		Runtime:      info.Runtime,
		Handler:      info.Handler,
		MemorySize:   info.Memory,
		Timeout:      info.Timeout,
		LastModified: info.LastModified,
		State:        info.State,
		PackageType:  "Zip",
	}

	if len(info.Environment) > 0 {
		cfg.Environment = &envEnvelope{Variables: info.Environment}
	}

	return cfg
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
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ResourceConflictException", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidParameterValueException", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "ServiceException", err.Error())
	}
}
