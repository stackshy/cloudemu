package cloudfunctions

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// gen2 functions live in the wire handler's h.gen2 map as a rich v2 resource
// (buildConfig/serviceConfig/eventTrigger), but they are ALSO registered in the
// shared serverless driver keyed by their short name — exactly like gen1 — so
// the invoke data plane resolves them: the v2 :call action and event delivery
// (Pub/Sub / Eventarc) both funnel through h.fn.Invoke and return the same
// invoke result model as gen1 (canned echo by default, or the real
// config.FunctionEngine when one is wired). The v1 API never surfaces or mutates
// a gen2 function (real GCP manages the two generations through disjoint APIs),
// so the v1 routes filter them out and the driver is the authority on name
// uniqueness across both generations.

// gen2DriverConfig builds the portable Serverless FunctionConfig that backs a
// gen2 function in the driver. Runtime and entryPoint come from buildConfig; the
// timeout and the runtime environment (buildConfig env overlaid by the
// serviceConfig runtime env) from serviceConfig. Memory is left unset: it is
// output-only for gen2 and the v2 serviceConfig keeps the real availableMemory
// string, so the driver copy does not need it.
func gen2DriverConfig(name string, fn *gen2Function) sdrv.FunctionConfig {
	cfg := sdrv.FunctionConfig{Name: name}

	var buildEnv, serviceEnv map[string]string

	if fn.BuildConfig != nil {
		cfg.Runtime = fn.BuildConfig.Runtime
		cfg.Handler = fn.BuildConfig.EntryPoint
		buildEnv = fn.BuildConfig.EnvironmentVariables
	}

	if fn.ServiceConfig != nil {
		cfg.Timeout = fn.ServiceConfig.TimeoutSeconds
		serviceEnv = fn.ServiceConfig.EnvironmentVariables
	}

	cfg.Environment = mergeGen2Env(buildEnv, serviceEnv)

	return cfg
}

// mergeGen2Env overlays the serviceConfig runtime env on the buildConfig env so
// the driver-backed function sees the same environment a real gen2 runtime does.
// It returns nil when neither is set so UpdateFunction leaves a stored env alone.
func mergeGen2Env(build, service map[string]string) map[string]string {
	if len(build) == 0 && len(service) == 0 {
		return nil
	}

	out := make(map[string]string, len(build)+len(service))
	for k, v := range build {
		out[k] = v
	}

	for k, v := range service {
		out[k] = v
	}

	return out
}

// gen2SourceCode returns the deployment bytes staged for a gen2 source upload, or
// nil when the function's storageSource does not name one this server staged (a
// gs:// object the client uploaded elsewhere, or no source at all). It lets the
// real FunctionEngine run gen2 code deployed via generateUploadUrl while the
// default (unstaged) path falls back to the echo stub. Best-effort: an
// unresolvable token is not an error.
func (h *Handler) gen2SourceCode(fn *gen2Function) []byte {
	if fn.BuildConfig == nil || fn.BuildConfig.Source == nil || fn.BuildConfig.Source.StorageSource == nil {
		return nil
	}

	token := strings.TrimSuffix(fn.BuildConfig.Source.StorageSource.Object, ".zip")
	if token == "" {
		return nil
	}

	code, ok := h.uploads.take(token)
	if !ok {
		return nil
	}

	return code
}

// registerGen2Driver creates the driver-backed function for a freshly created
// gen2 function. The driver rejects a duplicate name (across gen1 AND gen2, which
// matches real GCP forbidding two generations to share a name), so the caller
// uses its error as the authority before touching the gen2 map.
func (h *Handler) registerGen2Driver(ctx context.Context, name string, fn *gen2Function) error {
	cfg := gen2DriverConfig(name, fn)
	if code := h.gen2SourceCode(fn); len(code) > 0 {
		cfg.Code = code
		cfg.Framework = frameworkHTTP
	}

	_, err := h.fn.CreateFunction(ctx, cfg)

	return err
}

// syncGen2Driver reconciles the driver-backed function to a patched gen2
// function so a later :call / event delivery runs the updated runtime, env and
// (if the patch carried new source) code. It self-heals a missing driver entry
// by creating it, so the driver never diverges from the gen2 map.
func (h *Handler) syncGen2Driver(ctx context.Context, name string, fn *gen2Function) error {
	cfg := gen2DriverConfig(name, fn)
	if code := h.gen2SourceCode(fn); len(code) > 0 {
		cfg.Code = code
		cfg.Framework = frameworkHTTP
	}

	if _, err := h.fn.UpdateFunction(ctx, name, cfg); err != nil {
		if cerrors.IsNotFound(err) {
			_, cerr := h.fn.CreateFunction(ctx, cfg)
			return cerr
		}

		return err
	}

	return nil
}

// isGen2 reports whether the canonical resource name belongs to a gen2 function.
// The v1 routes use it to stay disjoint from gen2, which shares the driver store
// but is managed only through the v2 API.
func (h *Handler) isGen2(fullName string) bool {
	h.mu.RLock()
	_, ok := h.gen2[fullName]
	h.mu.RUnlock()

	return ok
}

// excludeGen2 drops gen2 functions from a v1 ListFunctions result: they share the
// driver store so the invoke path resolves them, but real GCP never lists a gen2
// function through the v1 API.
func (h *Handler) excludeGen2(p functionPath, infos []sdrv.FunctionInfo) []sdrv.FunctionInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]sdrv.FunctionInfo, 0, len(infos))

	for i := range infos {
		scope := p
		scope.name = infos[i].Name

		if _, ok := h.gen2[scope.fullName()]; !ok {
			out = append(out, infos[i])
		}
	}

	return out
}

// serveV2Call answers the gen2 invoke path, POST .../functions/{name}:call. gen2
// has no v1-style :call in the public API (it is invoked over the Cloud Run uri
// or Eventarc), but the emulator exposes one so users can exercise the invoke
// control flow uniformly; it returns the same {executionId, result|error} model
// as the gen1 :call, running the echo stub by default or the real engine when
// one is configured.
func (h *Handler) serveV2Call(w http.ResponseWriter, r *http.Request, p v2Path) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	if !h.isGen2(p.fullName()) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "function "+p.name+" not found")
		return
	}

	var req callRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.fn.Invoke(r.Context(), sdrv.InvokeInput{
		FunctionName: p.name,
		Payload:      []byte(req.Data),
		InvokeType:   "RequestResponse",
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := callResponse{ExecutionID: strconv.FormatInt(time.Now().UnixNano(), 10)}

	if out.Error != "" {
		resp.Error = out.Error
	} else {
		resp.Result = string(out.Payload)
	}

	writeJSON(w, http.StatusOK, resp)
}
