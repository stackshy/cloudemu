package lambda

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// codeSigningConfigsPrefix is the Lambda code signing config API prefix
// (Create/Get/List/Update/DeleteCodeSigningConfig and
// ListFunctionsByCodeSigningConfig at .../{arn}/functions). See
// https://docs.aws.amazon.com/lambda/latest/api/API_CreateCodeSigningConfig.html
const (
	codeSigningConfigsPrefix   = "/2020-04-22/code-signing-configs"
	codeSigningFunctionsSuffix = "/functions"
)

// codeSigningConfigNotFound is the error type PutFunctionCodeSigningConfig and
// CreateFunction return for an unknown code signing config ARN (HTTP 404).
const codeSigningConfigNotFound = "CodeSigningConfigNotFoundException"

// codeSigningManager is the AWS-only code signing surface (code signing
// configs and their association with functions). It has no Azure Functions or
// GCP Cloud Functions equivalent, so the handler type-asserts for it rather than
// requiring every cloud's function provider to implement it.
type codeSigningManager interface {
	CreateCodeSigningConfig(ctx context.Context, cfg sdrv.CodeSigningConfig) (*sdrv.CodeSigningConfig, error)
	GetCodeSigningConfig(ctx context.Context, arn string) (*sdrv.CodeSigningConfig, error)
	ListCodeSigningConfigs(ctx context.Context) ([]sdrv.CodeSigningConfig, error)
	UpdateCodeSigningConfig(ctx context.Context, arn string, upd sdrv.CodeSigningConfigUpdate) (*sdrv.CodeSigningConfig, error)
	DeleteCodeSigningConfig(ctx context.Context, arn string) error
	PutFunctionCodeSigningConfig(ctx context.Context, name, arn string) error
	GetFunctionCodeSigningConfig(ctx context.Context, name string) (string, error)
	DeleteFunctionCodeSigningConfig(ctx context.Context, name string) error
	ListFunctionsByCodeSigningConfig(ctx context.Context, arn string) ([]string, error)
}

// codeSigningConfigEnvelope is the wire CodeSigningConfig shape.
type codeSigningConfigEnvelope struct {
	CodeSigningConfigID  string                  `json:"CodeSigningConfigId"`
	CodeSigningConfigArn string                  `json:"CodeSigningConfigArn"`
	Description          string                  `json:"Description"`
	AllowedPublishers    allowedPublishers       `json:"AllowedPublishers"`
	CodeSigningPolicies  codeSigningPoliciesJSON `json:"CodeSigningPolicies"`
	LastModified         string                  `json:"LastModified"`
}

type allowedPublishers struct {
	SigningProfileVersionArns []string `json:"SigningProfileVersionArns"`
}

type codeSigningPoliciesJSON struct {
	UntrustedArtifactOnDeployment string `json:"UntrustedArtifactOnDeployment,omitempty"`
}

// codeSigningConfigRequest is the Create/UpdateCodeSigningConfig body. Fields
// are pointers so an Update can tell an omitted field from an empty one.
type codeSigningConfigRequest struct {
	Description         *string                  `json:"Description"`
	AllowedPublishers   *allowedPublishers       `json:"AllowedPublishers"`
	CodeSigningPolicies *codeSigningPoliciesJSON `json:"CodeSigningPolicies"`
	Tags                map[string]string        `json:"Tags"`
}

func toCodeSigningConfigEnvelope(c *sdrv.CodeSigningConfig) codeSigningConfigEnvelope {
	arns := c.SigningProfileVersionARNs
	if arns == nil {
		arns = []string{}
	}

	return codeSigningConfigEnvelope{
		CodeSigningConfigID:  c.ID,
		CodeSigningConfigArn: c.ARN,
		Description:          c.Description,
		AllowedPublishers:    allowedPublishers{SigningProfileVersionArns: arns},
		CodeSigningPolicies:  codeSigningPoliciesJSON{UntrustedArtifactOnDeployment: c.UntrustedArtifactOnDeployment},
		LastModified:         c.LastModified,
	}
}

// isCodeSigningPath reports whether path is a function code-signing-config
// sub-resource (/2020-06-30/functions/{name}/code-signing-config).
func isCodeSigningPath(path string) bool {
	_, ok := codeSigningFunctionName(path)

	return ok
}

// codeSigningFunctionName extracts the function name from a code-signing-config
// path and whether the path is one. The name is a single segment between the
// version prefix and the /code-signing-config suffix.
func codeSigningFunctionName(path string) (string, bool) {
	if !strings.HasPrefix(path, codeSigningPrefix) || !strings.HasSuffix(path, codeSigningSuffix) {
		return "", false
	}

	rest := strings.TrimPrefix(strings.TrimPrefix(path, codeSigningPrefix), "/")
	name := strings.TrimSuffix(rest, codeSigningSuffix)

	if name == "" || strings.Contains(name, "/") {
		return "", false
	}

	return name, true
}

// serveFunctionCodeSigningConfig handles the function code-signing-config
// sub-resource: GET (GetFunctionCodeSigningConfig), PUT
// (PutFunctionCodeSigningConfig) and DELETE (DeleteFunctionCodeSigningConfig).
// A function with no config attached answers GET with 200 and an empty
// CodeSigningConfigArn; Terraform's function read calls it on every refresh.
func (h *Handler) serveFunctionCodeSigningConfig(w http.ResponseWriter, r *http.Request, op opID, name string) {
	if name == "" {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
		return
	}

	if _, err := h.fn.GetFunction(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	csm, ok := h.fn.(codeSigningManager)

	switch op {
	case opGetFunctionCodeSigningConfig:
		arn := ""

		if ok {
			got, err := csm.GetFunctionCodeSigningConfig(r.Context(), name)
			if err != nil {
				writeErr(w, err)
				return
			}

			arn = got
		}

		writeJSON(w, http.StatusOK, map[string]any{"FunctionName": name, "CodeSigningConfigArn": arn})
	case opPutFunctionCodeSigningConfig:
		h.putFunctionCodeSigningConfig(w, r, csm, ok, name)
	case opDeleteFunctionCodeSigningConfig:
		if ok {
			if err := csm.DeleteFunctionCodeSigningConfig(r.Context(), name); err != nil {
				writeErr(w, err)
				return
			}
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "unsupported method for code-signing-config")
	}
}

// putFunctionCodeSigningConfig serves PutFunctionCodeSigningConfig.
func (h *Handler) putFunctionCodeSigningConfig(
	w http.ResponseWriter, r *http.Request, csm codeSigningManager, ok bool, name string,
) {
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "code signing not supported")
		return
	}

	var req struct {
		CodeSigningConfigArn string `json:"CodeSigningConfigArn"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if !h.checkCodeSigningConfigExists(w, r, req.CodeSigningConfigArn) {
		return
	}

	if err := csm.PutFunctionCodeSigningConfig(r.Context(), name, req.CodeSigningConfigArn); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"FunctionName": name, "CodeSigningConfigArn": req.CodeSigningConfigArn,
	})
}

// checkCodeSigningConfigExists writes CodeSigningConfigNotFoundException (404)
// and returns false when arn is set but names no code signing config, the error
// PutFunctionCodeSigningConfig and CreateFunction return for an unknown config
// (distinct from the function's ResourceNotFoundException). An empty arn passes.
func (h *Handler) checkCodeSigningConfigExists(w http.ResponseWriter, r *http.Request, arn string) bool {
	if arn == "" {
		return true
	}

	csm, ok := h.fn.(codeSigningManager)
	if !ok {
		return true
	}

	if _, err := csm.GetCodeSigningConfig(r.Context(), arn); err != nil {
		if cerrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, codeSigningConfigNotFound, cerrors.Message(err))
		} else {
			writeErr(w, err)
		}

		return false
	}

	return true
}

// serveCodeSigningConfigs handles /2020-04-22/code-signing-configs[/{arn}[/functions]].
func (h *Handler) serveCodeSigningConfigs(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) {
	csm, ok := h.fn.(codeSigningManager)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "code signing not supported")
		return
	}

	switch op {
	case opCreateCodeSigningConfig:
		h.createCodeSigningConfig(w, r, csm)
	case opListCodeSigningConfigs:
		listCodeSigningConfigs(w, r, csm)
	case opGetCodeSigningConfig:
		c, err := csm.GetCodeSigningConfig(r.Context(), a.item)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"CodeSigningConfig": toCodeSigningConfigEnvelope(c)})
	case opUpdateCodeSigningConfig:
		h.updateCodeSigningConfig(w, r, csm, a.item)
	case opDeleteCodeSigningConfig:
		if err := csm.DeleteCodeSigningConfig(r.Context(), a.item); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	case opListFunctionsByCodeSigningConfig:
		listFunctionsByCodeSigningConfig(w, r, csm, a.item)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// listCodeSigningConfigs serves ListCodeSigningConfigs, paged by Marker/MaxItems.
func listCodeSigningConfigs(w http.ResponseWriter, r *http.Request, csm codeSigningManager) {
	cfgs, err := csm.ListCodeSigningConfigs(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	start, end, nextMarker, _ := pageWindow(len(cfgs), r.URL.Query())

	out := make([]codeSigningConfigEnvelope, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, toCodeSigningConfigEnvelope(&cfgs[i]))
	}

	resp := map[string]any{"CodeSigningConfigs": out}
	if nextMarker != "" {
		resp["NextMarker"] = nextMarker
	}

	writeJSON(w, http.StatusOK, resp)
}

// listFunctionsByCodeSigningConfig serves ListFunctionsByCodeSigningConfig,
// paged by Marker/MaxItems.
func listFunctionsByCodeSigningConfig(w http.ResponseWriter, r *http.Request, csm codeSigningManager, arn string) {
	arns, err := csm.ListFunctionsByCodeSigningConfig(r.Context(), arn)
	if err != nil {
		writeErr(w, err)
		return
	}

	start, end, nextMarker, _ := pageWindow(len(arns), r.URL.Query())

	resp := map[string]any{"FunctionArns": append([]string{}, arns[start:end]...)}
	if nextMarker != "" {
		resp["NextMarker"] = nextMarker
	}

	writeJSON(w, http.StatusOK, resp)
}

// createCodeSigningConfig serves CreateCodeSigningConfig (HTTP 201).
func (*Handler) createCodeSigningConfig(w http.ResponseWriter, r *http.Request, csm codeSigningManager) {
	var req codeSigningConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	cfg := sdrv.CodeSigningConfig{Tags: req.Tags}
	if req.Description != nil {
		cfg.Description = *req.Description
	}

	if req.AllowedPublishers != nil {
		cfg.SigningProfileVersionARNs = req.AllowedPublishers.SigningProfileVersionArns
	}

	if req.CodeSigningPolicies != nil {
		cfg.UntrustedArtifactOnDeployment = req.CodeSigningPolicies.UntrustedArtifactOnDeployment
	}

	c, err := csm.CreateCodeSigningConfig(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"CodeSigningConfig": toCodeSigningConfigEnvelope(c)})
}

// updateCodeSigningConfig serves UpdateCodeSigningConfig.
func (*Handler) updateCodeSigningConfig(w http.ResponseWriter, r *http.Request, csm codeSigningManager, arn string) {
	var req codeSigningConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	upd := sdrv.CodeSigningConfigUpdate{Description: req.Description}
	if req.AllowedPublishers != nil {
		upd.SigningProfileVersionARNs = req.AllowedPublishers.SigningProfileVersionArns
		if upd.SigningProfileVersionARNs == nil {
			upd.SigningProfileVersionARNs = []string{}
		}
	}

	if req.CodeSigningPolicies != nil && req.CodeSigningPolicies.UntrustedArtifactOnDeployment != "" {
		policy := req.CodeSigningPolicies.UntrustedArtifactOnDeployment
		upd.UntrustedArtifactOnDeployment = &policy
	}

	c, err := csm.UpdateCodeSigningConfig(r.Context(), arn, upd)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"CodeSigningConfig": toCodeSigningConfigEnvelope(c)})
}

// codeSigningConfigARNMarker identifies a code signing config ARN
// (arn:aws:lambda:<region>:<account>:code-signing-config:csc-...) on the
// tagging API, which Lambda shares between functions and code signing configs.
const codeSigningConfigARNMarker = ":code-signing-config:"

// isCodeSigningConfigARN reports whether a tagging resource ARN names a code
// signing config rather than a function.
func isCodeSigningConfigARN(arn string) bool {
	return strings.Contains(arn, codeSigningConfigARNMarker)
}

// codeSigningTagger is the AWS-only tagging surface for code signing configs.
type codeSigningTagger interface {
	TagCodeSigningConfig(ctx context.Context, arn string, tags map[string]string) error
	UntagCodeSigningConfig(ctx context.Context, arn string, keys []string) error
	ListCodeSigningConfigTags(ctx context.Context, arn string) (map[string]string, error)
}

// serveCodeSigningConfigTags handles TagResource / UntagResource / ListTags on
// a code signing config ARN (/2017-03-31/tags/{arn}).
func (h *Handler) serveCodeSigningConfigTags(w http.ResponseWriter, r *http.Request, op opID, arn string) {
	tagger, ok := h.fn.(codeSigningTagger)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "tagging not supported")
		return
	}

	switch op {
	case opTagResource:
		var req struct {
			Tags map[string]string `json:"Tags"`
		}

		if !decodeJSON(w, r, &req) {
			return
		}

		if err := tagger.TagCodeSigningConfig(r.Context(), arn, req.Tags); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	case opUntagResource:
		if err := tagger.UntagCodeSigningConfig(r.Context(), arn, r.URL.Query()["tagKeys"]); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	case opListTags:
		tags, err := tagger.ListCodeSigningConfigTags(r.Context(), arn)
		if err != nil {
			writeErr(w, err)
			return
		}

		if tags == nil {
			tags = map[string]string{}
		}

		writeJSON(w, http.StatusOK, map[string]any{"Tags": tags})
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}
