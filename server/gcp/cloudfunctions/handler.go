// Package cloudfunctions implements the GCP Cloud Functions v1 REST API as
// a server.Handler. Real cloud.google.com/go/functions/apiv1 clients
// configured with a custom endpoint hit this handler the same way they hit
// cloudfunctions.googleapis.com.
//
// MVP coverage:
//
//	POST   /v1/projects/{p}/locations/{l}/functions             — Create (LRO)
//	GET    /v1/projects/{p}/locations/{l}/functions/{name}      — Get
//	GET    /v1/projects/{p}/locations/{l}/functions             — List
//	DELETE /v1/projects/{p}/locations/{l}/functions/{name}      — Delete (LRO)
//	POST   /v1/projects/{p}/locations/{l}/functions/{name}:call — Synchronous invoke
//	GET    /v1/operations/{op}                                  — Poll an LRO
//
// All mutating endpoints return Operation envelopes with done=true so SDK
// pollers terminate on the first response.
package cloudfunctions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	pathPrefix      = "/v1/projects/"
	functionsSeg    = "functions"
	locationsSeg    = "locations"
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20
	// maxUploadBytes bounds a source-zip PUT; real gen1 caps uploads at 100 MiB.
	maxUploadBytes = 100 << 20
	// uploadTokenBytes is the entropy of an upload token.
	uploadTokenBytes = 16
	// frameworkHTTP marks an engine deployment as using the functions-framework
	// request/response contract (GCP Cloud Functions gen1).
	frameworkHTTP = "http"
	// actionUploadSource is the collection action a generated upload URL points
	// back at; the source zip is PUT here.
	actionUploadSource = "uploadSource"
	// actionCall is the synchronous-invoke action.
	actionCall = "call"
	// actionGenerateUploadURL mints an upload URL for a source deploy.
	actionGenerateUploadURL = "generateUploadUrl"
	// actionGetIamPolicy / actionSetIamPolicy are the IAM invoker-policy verbs.
	actionGetIamPolicy = "getIamPolicy"
	actionSetIamPolicy = "setIamPolicy"
)

// ObjectStore is the slice of the in-process GCS backend the handler needs to
// fetch a sourceArchiveUrl (gs://bucket/object) deployment package.
type ObjectStore interface {
	GetObject(ctx context.Context, bucket, key string) (*storagedriver.Object, error)
}

// Handler serves GCP Cloud Functions v1 REST requests against a serverless
// driver.
type Handler struct {
	fn sdrv.Serverless
	// uploads stages source zips between generateUploadUrl (which mints a token)
	// and create (which consumes the staged bytes into the function's Code). It
	// is bounded and FIFO-evicting so an abandoned deploy can't grow memory.
	uploads *uploadStaging
	// objects fetches a sourceArchiveUrl (gs://...) deployment package from the
	// in-process GCS backend. Nil when no GCS backend is wired; an archive deploy
	// then fails loudly rather than silently falling back to the echo stub.
	objects ObjectStore
	// mu guards policies.
	mu sync.RWMutex
	// policies stores the IAM policy set via setIamPolicy, keyed by the function's
	// canonical resource name. CloudEmu does not enforce IAM; the policy is stored
	// verbatim so Terraform's setIamPolicy → getIamPolicy round-trips (the standard
	// way to grant roles/cloudfunctions.invoker to allUsers for a public function).
	policies map[string]*iamPolicy
}

// Option configures a Handler.
type Option func(*Handler)

// WithObjectStore lets create() fetch a sourceArchiveUrl (gs://...) deployment
// package from the in-process GCS backend so real code runs.
func WithObjectStore(s ObjectStore) Option {
	return func(h *Handler) { h.objects = s }
}

// New returns a Cloud Functions handler backed by fn.
func New(fn sdrv.Serverless, opts ...Option) *Handler {
	h := &Handler{fn: fn, uploads: newUploadStaging(), policies: make(map[string]*iamPolicy)}
	for _, opt := range opts {
		opt(h)
	}

	return h
}

// Matches accepts paths that look like Cloud Functions v1: either an LRO poll
// (/v1/operations/...) or a /v1/projects/{p}/locations/{l}/functions[/...]
// path. The locations+functions segment guards us from shadowing Firestore's
// /v1/projects/{p}/databases/... URLs.
func (*Handler) Matches(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/v1/operations/") {
		return true
	}

	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	rest := strings.TrimPrefix(r.URL.Path, pathPrefix)

	// rest is "{project}/locations/{location}/functions[/...]"
	parts := strings.Split(rest, "/")

	const (
		idxScope = 1 // locations
		idxType  = 3 // functions
	)

	if len(parts) <= idxType {
		return false
	}

	if parts[idxScope] != locationsSeg {
		return false
	}

	// Strip ":action" suffix from the last segment for the type-equality check.
	typePart := parts[idxType]
	if i := strings.Index(typePart, ":"); i >= 0 {
		typePart = typePart[:i]
	}

	return typePart == functionsSeg
}

// ServeHTTP routes requests by URL shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v1/operations/") {
		h.serveOperation(w, r)
		return
	}

	parts, ok := parseFunctionsPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported path")
		return
	}

	if h.serveAction(w, r, parts) {
		return
	}

	if parts.name != "" {
		h.serveResource(w, r, parts)
		return
	}

	h.serveCollection(w, r, parts)
}

// serveAction dispatches the ":action" verbs (call, generateUploadUrl,
// uploadSource, get/setIamPolicy). It returns true when it handled the request;
// false lets ServeHTTP fall through to the plain resource/collection routes.
func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, p functionPath) bool {
	switch p.action {
	case actionCall:
		if p.name == "" {
			return false
		}

		h.serveCall(w, r, p)
	case actionGetIamPolicy, actionSetIamPolicy:
		if p.name == "" {
			return false
		}

		h.serveIamPolicy(w, r, p)
	case actionGenerateUploadURL:
		h.generateUploadURL(w, r, p)
	case actionUploadSource:
		h.uploadSource(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) serveResource(w http.ResponseWriter, r *http.Request, p functionPath) {
	switch r.Method {
	case http.MethodGet:
		h.get(w, r, p)
	case http.MethodPatch:
		h.update(w, r, p)
	case http.MethodDelete:
		h.delete(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, p functionPath) {
	switch r.Method {
	case http.MethodPost:
		h.create(w, r, p)
	case http.MethodGet:
		h.list(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

// generateUploadURL answers functions:generateUploadUrl — the first step of a
// source-upload deploy. Real Cloud Functions returns a signed GCS URL the
// client PUTs the source zip to; the emulator mints a token, stages a pending
// slot, and returns a URL that points BACK at this same server's
// functions:uploadSource action so the PUT lands here.
func (h *Handler) generateUploadURL(w http.ResponseWriter, r *http.Request, p functionPath) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	token, err := newUploadToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "mint upload token: "+err.Error())
		return
	}

	// Stage a pending (empty) slot so a token is only ever consumed once and an
	// upload against an unknown token is rejected.
	h.uploads.stage(token, nil)

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	uploadURL := scheme + "://" + r.Host + pathPrefix + p.project + "/" + locationsSeg + "/" + p.location +
		"/" + functionsSeg + ":" + actionUploadSource + "?token=" + token

	writeJSON(w, http.StatusOK, map[string]string{"uploadUrl": uploadURL})
}

// uploadSource accepts the PUT of a source zip to a generated upload URL and
// stores the raw bytes under the URL's token for create to consume. It mirrors
// the signed-URL PUT clients make to GCS: no Authorization, content-type
// application/zip.
func (h *Handler) uploadSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" || !h.uploads.has(token) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown or expired upload token")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUploadBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "read upload body: "+err.Error())
		return
	}

	h.uploads.stage(token, body)

	// GCS answers a signed-URL PUT with 200 and an empty body.
	w.WriteHeader(http.StatusOK)
}

// consumeUpload pulls the staged source zip named by uploadURL's token into
// cfg.Code and marks the deployment as using the http framework, then removes
// the one-time staging entry. It returns an error when the URL carries no token
// this server minted, or the token resolves to no staged bytes (unknown, already
// consumed, or PUT skipped) — so create can reject it rather than silently
// producing a function that never runs the intended code.
func (h *Handler) consumeUpload(uploadURL string, cfg *sdrv.FunctionConfig) error {
	token := uploadToken(uploadURL)
	if token == "" {
		return errUnresolvedUpload
	}

	code, ok := h.uploads.take(token)
	if !ok || len(code) == 0 {
		return errUnresolvedUpload
	}

	cfg.Code = code
	cfg.Framework = frameworkHTTP

	return nil
}

// loadSource pulls a source deploy's code into cfg from whichever source the
// request names: the staged upload (generateUploadUrl → PUT) or a gs:// archive
// fetched from the in-process GCS backend. It writes the error response and
// returns false on failure so create can bail rather than register a function
// that never runs the intended code.
func (h *Handler) loadSource(w http.ResponseWriter, r *http.Request, body *cloudFunction, cfg *sdrv.FunctionConfig) bool {
	if body.SourceUploadURL != "" {
		if err := h.consumeUpload(body.SourceUploadURL, cfg); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unknown or already-consumed sourceUploadUrl")
			return false
		}

		return true
	}

	if err := h.consumeArchive(r.Context(), body.SourceArchiveURL, cfg); err != nil {
		writeErr(w, err)
		return false
	}

	return true
}

// consumeArchive fetches the gs://bucket/object source zip named by archiveURL
// from the in-process GCS backend into cfg.Code and marks the deployment as
// using the http framework — the same contract a staged upload uses. A malformed
// URL, an unwired GCS backend, or a missing/empty object is a hard error rather
// than a silently stubbed function.
func (h *Handler) consumeArchive(ctx context.Context, archiveURL string, cfg *sdrv.FunctionConfig) error {
	bucket, object, ok := parseGCSURL(archiveURL)
	if !ok {
		return cerrors.Newf(cerrors.InvalidArgument, "sourceArchiveUrl must be gs://bucket/object, got %q", archiveURL)
	}

	if h.objects == nil {
		return cerrors.New(cerrors.InvalidArgument,
			"sourceArchiveUrl set but no GCS backend is wired; deploy via generateUploadUrl instead")
	}

	obj, err := h.objects.GetObject(ctx, bucket, object)
	if err != nil {
		return err
	}

	if len(obj.Data) == 0 {
		return cerrors.Newf(cerrors.InvalidArgument, "sourceArchiveUrl %q resolves to an empty object", archiveURL)
	}

	cfg.Code = obj.Data
	cfg.Framework = frameworkHTTP

	return nil
}

// parseGCSURL splits a gs://bucket/object URL into its bucket and object name.
func parseGCSURL(raw string) (bucket, object string, ok bool) {
	const scheme = "gs://"

	if !strings.HasPrefix(raw, scheme) {
		return "", "", false
	}

	rest := strings.TrimPrefix(raw, scheme)

	i := strings.Index(rest, "/")
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}

	return rest[:i], rest[i+1:], true
}

// uploadToken extracts the ?token= value from a generated upload URL. It returns
// "" when the URL is not one this server minted.
func uploadToken(uploadURL string) string {
	u, err := url.Parse(uploadURL)
	if err != nil {
		return ""
	}

	return u.Query().Get("token")
}

// newUploadToken returns a random, single-use upload token.
func newUploadToken() (string, error) {
	b := make([]byte, uploadTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return hex.EncodeToString(b), nil
}

// serveOperation answers GET /v1/operations/{name}. We always return done=true
// because mutations are synchronous in the mock; a poll is just an echo.
func (*Handler) serveOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	opName := strings.TrimPrefix(r.URL.Path, "/v1/")
	writeJSON(w, http.StatusOK, operation{Name: opName, Done: true})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, p functionPath) {
	// Real Cloud Functions accepts the function name in either the body or as a
	// "?functionId=" query parameter. SDKs use the body.
	var body cloudFunction
	if !decodeJSON(w, r, &body) {
		return
	}

	name := lastSegment(body.Name)
	if name == "" {
		name = r.URL.Query().Get("functionId")
	}

	if name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "function name required")
		return
	}

	cfg := sdrv.FunctionConfig{
		Name:        name,
		Runtime:     body.Runtime,
		Handler:     body.EntryPoint,
		Memory:      body.AvailableMemory,
		Tags:        body.Labels,
		Environment: body.EnvVariables,
		Timeout:     parseTimeoutSeconds(body.Timeout),
	}

	// A source deploy carries the code out-of-band. gen1 functions run under the
	// functions-framework request/response contract with a bare entrypoint, which
	// real Cloud Functions requires — reject a code deploy that omits it.
	if body.SourceUploadURL != "" || body.SourceArchiveURL != "" {
		if body.EntryPoint == "" {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "entryPoint is required")
			return
		}

		if !h.loadSource(w, r, &body, &cfg) {
			return
		}
	}

	info, err := h.fn.CreateFunction(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	resource := toCloudFunction(info, p)

	writeJSON(w, http.StatusOK, operation{
		Name:     "operations/create-" + name + "-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Done:     true,
		Response: resourceAsResponse(resource, "CloudFunction"),
	})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, p functionPath) {
	info, err := h.fn.GetFunction(r.Context(), p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toCloudFunction(info, p))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, p functionPath) {
	infos, err := h.fn.ListFunctions(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listFunctionsResponse{Functions: make([]cloudFunction, 0, len(infos))}
	for i := range infos {
		out.Functions = append(out.Functions, toCloudFunction(&infos[i], p))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request, p functionPath) {
	var body cloudFunction
	if !decodeJSON(w, r, &body) {
		return
	}

	cfg := sdrv.FunctionConfig{
		Name:        p.name,
		Runtime:     body.Runtime,
		Handler:     body.EntryPoint,
		Memory:      body.AvailableMemory,
		Tags:        body.Labels,
		Environment: body.EnvVariables,
		Timeout:     parseTimeoutSeconds(body.Timeout),
	}

	// A PATCH that carries a new source must redeploy the real code to the engine;
	// resolve it exactly as create does so the new bytes land in cfg.Code and the
	// staged upload token is consumed. A metadata-only PATCH carries no source, so
	// cfg.Code stays empty and the provider leaves the deployed code untouched.
	if body.SourceUploadURL != "" || body.SourceArchiveURL != "" {
		if !h.loadSource(w, r, &body, &cfg) {
			return
		}
	}

	info, err := h.fn.UpdateFunction(r.Context(), p.name, cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	resource := toCloudFunction(info, p)
	writeJSON(w, http.StatusOK, operation{
		Name:     "operations/update-" + p.name,
		Done:     true,
		Response: resourceAsResponse(resource, "CloudFunction"),
	})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, p functionPath) {
	if err := h.fn.DeleteFunction(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, operation{
		Name: "operations/delete-" + p.name,
		Done: true,
	})
}

func (h *Handler) serveCall(w http.ResponseWriter, r *http.Request, p functionPath) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
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

	resp := callResponse{
		ExecutionID: strconv.FormatInt(time.Now().UnixNano(), 10),
	}

	if out.Error != "" {
		resp.Error = out.Error
	} else {
		resp.Result = string(out.Payload)
	}

	writeJSON(w, http.StatusOK, resp)
}

// functionPath holds the components of a Cloud Functions URL.
type functionPath struct {
	project  string
	location string
	name     string
	action   string // "call", etc.
}

// fullName returns the canonical resource name "projects/{p}/locations/{l}/functions/{n}".
func (p functionPath) fullName() string {
	return "projects/" + p.project + "/locations/" + p.location + "/functions/" + p.name
}

// parseFunctionsPath extracts components from a Cloud Functions v1 URL.
//
//	/v1/projects/{p}/locations/{l}/functions
//	/v1/projects/{p}/locations/{l}/functions/{name}
//	/v1/projects/{p}/locations/{l}/functions/{name}:{action}
func parseFunctionsPath(path string) (functionPath, bool) {
	rest := strings.TrimPrefix(path, pathPrefix)

	parts := strings.Split(rest, "/")

	const (
		minParts    = 4 // {project}/locations/{location}/functions
		idxProject  = 0
		idxScope    = 1
		idxLocation = 2
		idxType     = 3
		idxName     = 4
	)

	if len(parts) < minParts || parts[idxScope] != locationsSeg {
		return functionPath{}, false
	}

	typePart := parts[idxType]
	if typePart != functionsSeg {
		// Could be "functions:action" with no name on the collection.
		base, action, hasAction := splitColon(typePart)
		if !hasAction || base != functionsSeg {
			return functionPath{}, false
		}

		return functionPath{
			project: parts[idxProject], location: parts[idxLocation], action: action,
		}, true
	}

	out := functionPath{
		project:  parts[idxProject],
		location: parts[idxLocation],
	}

	if len(parts) > idxName {
		nameWithAction := strings.Join(parts[idxName:], "/")
		if base, action, ok := splitColon(nameWithAction); ok {
			out.name = base
			out.action = action
		} else {
			out.name = nameWithAction
		}
	}

	return out, true
}

func splitColon(s string) (base, action string, ok bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, "", false
	}

	return s[:i], s[i+1:], true
}

func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

func parseTimeoutSeconds(t string) int {
	t = strings.TrimSuffix(t, "s")

	n, err := strconv.Atoi(t)
	if err != nil {
		return 0
	}

	return n
}

func toCloudFunction(info *sdrv.FunctionInfo, p functionPath) cloudFunction {
	scope := p
	scope.name = info.Name

	cf := cloudFunction{
		Name:            scope.fullName(),
		Status:          "ACTIVE",
		Runtime:         info.Runtime,
		EntryPoint:      info.Handler,
		AvailableMemory: info.Memory,
		Labels:          info.Tags,
		EnvVariables:    info.Environment,
		UpdateTime:      info.LastModified,
		VersionID:       "1",
		// Real Cloud Functions always advertises the HTTPS trigger URL; clients
		// read it to invoke the function.
		HTTPSTrigger: &httpsTrigger{
			URL: "https://" + scope.location + "-" + scope.project + ".cloudfunctions.net/" + scope.name,
		},
	}

	if info.Timeout > 0 {
		cf.Timeout = strconv.Itoa(info.Timeout) + "s"
	}

	return cf
}

//nolint:gocritic // cf is the response body shape; one copy per LRO response is fine.
func resourceAsResponse(cf cloudFunction, kind string) map[string]any {
	b, err := json.Marshal(cf)
	if err != nil {
		return nil
	}

	out := map[string]any{
		"@type": "type.googleapis.com/google.cloud.functions.v1." + kind,
	}

	var fields map[string]any
	_ = json.Unmarshal(b, &fields)

	for k, v := range fields {
		out[k] = v
	}

	return out
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, reason, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": msg,
			"status":  reason,
		},
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
