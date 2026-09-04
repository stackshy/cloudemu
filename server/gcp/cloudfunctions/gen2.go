package cloudfunctions

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// gen2Function is the GCP Cloud Functions gen2 (v2 API) resource shape. gen2 is
// the modern default (functions/apiv2, terraform google_cloudfunctions2_function)
// and is an entirely different resource from v1: the code and its build are
// described by buildConfig, the deployed Cloud Run service by serviceConfig, and
// event-driven functions by eventTrigger. CloudEmu does not build or run gen2
// code, so it round-trips the request and synthesizes the output-only fields real
// GCP fills in (state ACTIVE, environment GEN_2, serviceConfig.uri, build, etc.).
type gen2Function struct {
	Name          string             `json:"name,omitempty"`
	Environment   string             `json:"environment,omitempty"`
	Description   string             `json:"description,omitempty"`
	State         string             `json:"state,omitempty"`
	URL           string             `json:"url,omitempty"`
	UpdateTime    string             `json:"updateTime,omitempty"`
	CreateTime    string             `json:"createTime,omitempty"`
	Labels        map[string]string  `json:"labels,omitempty"`
	BuildConfig   *gen2BuildConfig   `json:"buildConfig,omitempty"`
	ServiceConfig *gen2ServiceConfig `json:"serviceConfig,omitempty"`
	EventTrigger  *gen2EventTrigger  `json:"eventTrigger,omitempty"`

	// revisionSeq tracks the deployed-revision generation so a patch cuts a fresh
	// serviceConfig.revision, matching a real gen2 redeploy. Not serialized.
	revisionSeq int `json:"-"`
}

type gen2BuildConfig struct {
	Build                string            `json:"build,omitempty"`
	Runtime              string            `json:"runtime,omitempty"`
	EntryPoint           string            `json:"entryPoint,omitempty"`
	Source               *gen2Source       `json:"source,omitempty"`
	DockerRegistry       string            `json:"dockerRegistry,omitempty"`
	DockerRepository     string            `json:"dockerRepository,omitempty"`
	EnvironmentVariables map[string]string `json:"environmentVariables,omitempty"`
}

type gen2Source struct {
	StorageSource *gen2StorageSource `json:"storageSource,omitempty"`
}

type gen2StorageSource struct {
	Bucket     string `json:"bucket,omitempty"`
	Object     string `json:"object,omitempty"`
	Generation int64  `json:"generation,omitempty"`
}

type gen2ServiceConfig struct {
	Service              string            `json:"service,omitempty"`
	URI                  string            `json:"uri,omitempty"`
	ServiceAccountEmail  string            `json:"serviceAccountEmail,omitempty"`
	AvailableMemory      string            `json:"availableMemory,omitempty"`
	AvailableCPU         string            `json:"availableCpu,omitempty"`
	TimeoutSeconds       int               `json:"timeoutSeconds,omitempty"`
	IngressSettings      string            `json:"ingressSettings,omitempty"`
	EnvironmentVariables map[string]string `json:"environmentVariables,omitempty"`
	Revision             string            `json:"revision,omitempty"`
	MaxInstanceCount     int               `json:"maxInstanceCount,omitempty"`
	MinInstanceCount     int               `json:"minInstanceCount,omitempty"`
}

type gen2EventTrigger struct {
	Trigger             string            `json:"trigger,omitempty"`
	TriggerRegion       string            `json:"triggerRegion,omitempty"`
	EventType           string            `json:"eventType,omitempty"`
	EventFilters        []gen2EventFilter `json:"eventFilters,omitempty"`
	PubsubTopic         string            `json:"pubsubTopic,omitempty"`
	ServiceAccountEmail string            `json:"serviceAccountEmail,omitempty"`
	RetryPolicy         string            `json:"retryPolicy,omitempty"`
}

// gen2EventFilter is one eventTrigger.eventFilters entry: a non-Pub/Sub gen2
// trigger (e.g. Cloud Storage) binds to a specific source resource by
// filtering on a CloudEvent attribute — a storage trigger filters on
// attribute "bucket" — rather than the pubsubTopic field Pub/Sub triggers use.
type gen2EventFilter struct {
	Attribute string `json:"attribute,omitempty"`
	Operator  string `json:"operator,omitempty"`
	Value     string `json:"value,omitempty"`
}

// listGen2Response is the {functions, nextPageToken} envelope of v2 list.
type listGen2Response struct {
	Functions     []gen2Function `json:"functions"`
	NextPageToken string         `json:"nextPageToken,omitempty"`
}

// gen2UploadURLResponse is the body of v2 functions:generateUploadUrl.
type gen2UploadURLResponse struct {
	UploadURL     string             `json:"uploadUrl"`
	StorageSource *gen2StorageSource `json:"storageSource,omitempty"`
}

// gen2DownloadURLResponse is the body of v2 functions:generateDownloadUrl.
type gen2DownloadURLResponse struct {
	DownloadURL string `json:"downloadUrl"`
}

// v2Path holds the parsed components of a /v2/projects/... Cloud Functions URL.
// For an operation poll, name carries the operation id.
type v2Path struct {
	project     string
	location    string
	name        string
	action      string
	isOperation bool
}

func (p v2Path) fullName() string {
	return "projects/" + p.project + "/locations/" + p.location + "/functions/" + p.name
}

// matchesV2 reports whether path is a v2 Cloud Functions URL this handler owns.
// It claims the functions collection/resource unconditionally, but only claims
// an operation poll whose id carries the gen2OpPrefix — Cloud Run also matches
// /v2/projects/{p}/locations/{l}/operations/... and is registered after this
// handler, so the prefix keeps the two disjoint.
func matchesV2(path string) bool {
	p, ok := parseV2Path(path)
	if !ok {
		return false
	}

	if p.isOperation {
		return strings.HasPrefix(p.name, gen2OpPrefix)
	}

	return true
}

// parseV2Path extracts the components of a v2 URL:
//
//	/v2/projects/{p}/locations/{l}/functions
//	/v2/projects/{p}/locations/{l}/functions/{name}
//	/v2/projects/{p}/locations/{l}/functions/{name}:{action}
//	/v2/projects/{p}/locations/{l}/functions:{action}
//	/v2/projects/{p}/locations/{l}/operations/{op}
func parseV2Path(path string) (v2Path, bool) {
	rest := strings.TrimPrefix(path, v2PathPrefix)
	parts := strings.Split(rest, "/")

	const (
		minParts    = 4
		idxProject  = 0
		idxScope    = 1
		idxLocation = 2
		idxType     = 3
		idxName     = 4
	)

	if len(parts) < minParts || parts[idxScope] != locationsSeg {
		return v2Path{}, false
	}

	out := v2Path{project: parts[idxProject], location: parts[idxLocation]}

	switch parts[idxType] {
	case operationsSeg:
		if len(parts) <= idxName {
			return v2Path{}, false
		}

		out.isOperation = true
		out.name = strings.Join(parts[idxName:], "/")

		return out, true
	case functionsSeg:
		if len(parts) > idxName {
			nameWithAction := strings.Join(parts[idxName:], "/")
			if base, action, ok := splitColon(nameWithAction); ok {
				out.name, out.action = base, action
			} else {
				out.name = nameWithAction
			}
		}

		return out, true
	default:
		if base, action, ok := splitColon(parts[idxType]); ok && base == functionsSeg {
			out.action = action
			return out, true
		}

		return v2Path{}, false
	}
}

// serveV2 routes a v2 Cloud Functions request by URL shape.
func (h *Handler) serveV2(w http.ResponseWriter, r *http.Request) {
	p, ok := parseV2Path(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported path")
		return
	}

	if p.isOperation {
		h.serveV2Operation(w, r, p)
		return
	}

	if p.action != "" {
		h.serveV2Action(w, r, p)
		return
	}

	if p.name != "" {
		h.serveV2Resource(w, r, p)
		return
	}

	h.serveV2Collection(w, r, p)
}

func (h *Handler) serveV2Action(w http.ResponseWriter, r *http.Request, p v2Path) {
	switch p.action {
	case actionGenerateUploadURL:
		h.generateUploadURLV2(w, r, p)
	case actionGenerateDownloadURL:
		h.generateDownloadURLV2(w, r, p)
	case actionGetIamPolicy, actionSetIamPolicy:
		h.serveV2IamPolicy(w, r, p)
	case actionTestIamPermissions:
		h.serveV2TestIamPermissions(w, r, p)
	case actionCall:
		if p.name == "" {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown method: "+p.action)
			return
		}

		h.serveV2Call(w, r, p)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown method: "+p.action)
	}
}

func (h *Handler) serveV2Resource(w http.ResponseWriter, r *http.Request, p v2Path) {
	switch r.Method {
	case http.MethodGet:
		h.getV2(w, p)
	case http.MethodPatch:
		h.patchV2(w, r, p)
	case http.MethodDelete:
		h.deleteV2(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) serveV2Collection(w http.ResponseWriter, r *http.Request, p v2Path) {
	switch r.Method {
	case http.MethodPost:
		h.createV2(w, r, p)
	case http.MethodGet:
		h.listV2(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) createV2(w http.ResponseWriter, r *http.Request, p v2Path) {
	var body gen2Function
	if !decodeJSON(w, r, &body) {
		return
	}

	name := lastSegment(body.Name)
	if name == "" {
		name = r.URL.Query().Get("functionId")
	}

	if name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "functionId is required")
		return
	}

	p.name = name
	key := p.fullName()

	fn := body
	fn.revisionSeq = 1
	computeGen2Outputs(&fn, p, true)

	// Register the gen2 function in the shared serverless driver so :call and
	// event delivery resolve it like gen1. The driver is the authority on name
	// uniqueness across gen1 AND gen2, so a duplicate create is rejected here
	// before anything lands in the gen2 map.
	if err := h.registerGen2Driver(r.Context(), name, &fn); err != nil {
		writeErr(w, err)
		return
	}

	h.mu.Lock()
	if _, exists := h.gen2[key]; exists {
		h.mu.Unlock()
		// The driver guards uniqueness, so this is unreachable in practice; roll
		// back the driver registration to keep the two stores consistent.
		_ = h.fn.DeleteFunction(r.Context(), name)
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", "function "+name+" already exists")

		return
	}

	h.gen2[key] = &fn
	resp := cloneGen2(&fn)
	h.mu.Unlock()

	h.finishV2LRO(w, p, resp)
}

func (h *Handler) getV2(w http.ResponseWriter, p v2Path) {
	h.mu.RLock()
	fn := cloneGen2(h.gen2[p.fullName()])
	h.mu.RUnlock()

	if fn == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "function "+p.name+" not found")
		return
	}

	writeJSON(w, http.StatusOK, fn)
}

func (h *Handler) listV2(w http.ResponseWriter, r *http.Request, p v2Path) {
	h.mu.RLock()
	fns := make([]gen2Function, 0, len(h.gen2))

	prefix := "projects/" + p.project + "/locations/" + p.location + "/functions/"
	for name, fn := range h.gen2 {
		if strings.HasPrefix(name, prefix) {
			fns = append(fns, *cloneGen2(fn))
		}
	}
	h.mu.RUnlock()

	sort.Slice(fns, func(i, j int) bool { return fns[i].Name < fns[j].Name })

	page, next := paginate(len(fns), r.URL.Query())

	writeJSON(w, http.StatusOK, listGen2Response{
		Functions:     fns[page.start:page.end],
		NextPageToken: next,
	})
}

func (h *Handler) patchV2(w http.ResponseWriter, r *http.Request, p v2Path) {
	var body gen2Function
	if !decodeJSON(w, r, &body) {
		return
	}

	key := p.fullName()

	h.mu.Lock()

	fn := h.gen2[key]
	if fn == nil {
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, "NOT_FOUND", "function "+p.name+" not found")

		return
	}

	fn.revisionSeq++

	mergeGen2(fn, &body, parseUpdateMask(r.URL.Query()))
	computeGen2Outputs(fn, p, false)

	updated := cloneGen2(fn)

	h.mu.Unlock()

	// Keep the driver-backed function in sync so a later :call / event delivery
	// runs the patched runtime, env and (if the patch carried new source) code.
	if err := h.syncGen2Driver(r.Context(), p.name, updated); err != nil {
		writeErr(w, err)
		return
	}

	h.finishV2LRO(w, p, updated)
}

func (h *Handler) deleteV2(w http.ResponseWriter, r *http.Request, p v2Path) {
	key := p.fullName()

	h.mu.Lock()

	_, ok := h.gen2[key]
	if ok {
		delete(h.gen2, key)
		delete(h.policies, key)
	}

	h.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "function "+p.name+" not found")
		return
	}

	// Drop the driver-backed function too so the invoke path no longer resolves
	// it. Best-effort: a missing driver entry is not an error.
	_ = h.fn.DeleteFunction(r.Context(), p.name)

	op := h.mintV2Operation(p, nil)
	writeJSON(w, http.StatusOK, op)
}

// finishV2LRO stores and returns the completed create/patch operation. Real gen2
// deploys are long-running; the emulator reconciles synchronously and returns
// done=true with the function so a client's first poll (or the inline response)
// already sees ACTIVE.
func (h *Handler) finishV2LRO(w http.ResponseWriter, p v2Path, fn *gen2Function) {
	op := h.mintV2Operation(p, resourceAsResponseV2(fn))
	writeJSON(w, http.StatusOK, op)
}

// mintV2Operation builds a done=true operation with a gen2-prefixed name and
// caches it so a later Operations.Get poll returns the same result.
func (h *Handler) mintV2Operation(p v2Path, response map[string]any) operation {
	opName := "projects/" + p.project + "/locations/" + p.location + "/operations/" + gen2OpPrefix + randomToken()

	op := operation{Name: opName, Done: true, Response: response}

	h.mu.Lock()
	h.operations[opName] = op
	h.mu.Unlock()

	return op
}

func (h *Handler) serveV2Operation(w http.ResponseWriter, r *http.Request, p v2Path) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	opName := "projects/" + p.project + "/locations/" + p.location + "/operations/" + p.name

	h.mu.RLock()
	op, ok := h.operations[opName]
	h.mu.RUnlock()

	if !ok {
		// An unknown but well-formed gen2 operation is reported complete rather
		// than 404 so a poll after a process restart still terminates.
		op = operation{Name: opName, Done: true}
	}

	writeJSON(w, http.StatusOK, op)
}

func (h *Handler) generateUploadURLV2(w http.ResponseWriter, r *http.Request, p v2Path) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	token, err := newUploadToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "mint upload token: "+err.Error())
		return
	}

	// Stage a slot so a subsequent PUT to the returned URL (handled by the shared
	// v1 uploadSource endpoint) is accepted; gen2 code is not executed, so the
	// staged bytes are only ever consumed to satisfy the upload handshake.
	h.uploads.stage(token, nil)

	uploadURL := requestScheme(r) + "://" + r.Host + pathPrefix + p.project + "/" + locationsSeg + "/" + p.location +
		"/" + functionsSeg + ":" + actionUploadSource + "?token=" + token

	writeJSON(w, http.StatusOK, gen2UploadURLResponse{
		UploadURL: uploadURL,
		StorageSource: &gen2StorageSource{
			Bucket: "gcf-v2-uploads-" + p.project,
			Object: token + ".zip",
		},
	})
}

// generateDownloadURLV2 serves v2 functions:generateDownloadUrl on a named
// function: it returns a URL from which the function's deployed source can be
// downloaded. The function must exist (404 otherwise). gen2 source is only
// staged (not executed) in the emulator, so the URL is synthetic — enough for
// the SDK/gcloud call to succeed and read back a downloadUrl.
func (h *Handler) generateDownloadURLV2(w http.ResponseWriter, r *http.Request, p v2Path) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	h.mu.RLock()
	_, ok := h.gen2[p.fullName()]
	h.mu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "function "+p.name+" not found")
		return
	}

	downloadURL := requestScheme(r) + "://" + r.Host + pathPrefix + p.project + "/" + locationsSeg + "/" + p.location +
		"/" + functionsSeg + "/" + p.name + "/source.zip"

	writeJSON(w, http.StatusOK, gen2DownloadURLResponse{DownloadURL: downloadURL})
}

// mergeGen2 applies a patch body onto the stored function under the update mask.
// A partial buildConfig/serviceConfig patch merges sub-fields rather than
// replacing the whole object, so a mask that touches one sub-field (or omits a
// mask entirely) never wipes the runtime, entryPoint or scaling the client left
// unset.
func mergeGen2(dst, src *gen2Function, mask updateMask) {
	applyMaskedStr(mask, "description", &dst.Description, src.Description)

	if mask.covers("labels") && (mask.explicit() || src.Labels != nil) {
		dst.Labels = src.Labels
	}

	if src.BuildConfig != nil {
		mergeBuildConfig(dst, src.BuildConfig, mask)
	}

	if src.ServiceConfig != nil {
		mergeServiceConfig(dst, src.ServiceConfig, mask)
	}

	if src.EventTrigger != nil && mask.covers("eventTrigger") {
		dst.EventTrigger = src.EventTrigger
	}
}

// mergeBuildConfig merges the patch buildConfig sub-fields under the mask. The
// build id is output-only (computeGen2Outputs recuts it) and is not merged here.
func mergeBuildConfig(dst *gen2Function, bc *gen2BuildConfig, mask updateMask) {
	if dst.BuildConfig == nil {
		dst.BuildConfig = &gen2BuildConfig{}
	}

	d := dst.BuildConfig
	applyMaskedStr(mask, "buildConfig.runtime", &d.Runtime, bc.Runtime)
	applyMaskedStr(mask, "buildConfig.entryPoint", &d.EntryPoint, bc.EntryPoint)
	applyMaskedStr(mask, "buildConfig.dockerRegistry", &d.DockerRegistry, bc.DockerRegistry)
	applyMaskedStr(mask, "buildConfig.dockerRepository", &d.DockerRepository, bc.DockerRepository)

	if mask.covers("buildConfig.environmentVariables") && (mask.explicit() || bc.EnvironmentVariables != nil) {
		d.EnvironmentVariables = bc.EnvironmentVariables
	}

	if mask.covers("buildConfig.source") && bc.Source != nil {
		d.Source = bc.Source
	}
}

func mergeServiceConfig(dst *gen2Function, sc *gen2ServiceConfig, mask updateMask) {
	if dst.ServiceConfig == nil {
		dst.ServiceConfig = &gen2ServiceConfig{}
	}

	d := dst.ServiceConfig
	applyMaskedStr(mask, "serviceConfig.serviceAccountEmail", &d.ServiceAccountEmail, sc.ServiceAccountEmail)
	applyMaskedStr(mask, "serviceConfig.availableMemory", &d.AvailableMemory, sc.AvailableMemory)
	applyMaskedStr(mask, "serviceConfig.availableCpu", &d.AvailableCPU, sc.AvailableCPU)
	applyMaskedInt(mask, "serviceConfig.timeoutSeconds", &d.TimeoutSeconds, sc.TimeoutSeconds)
	applyMaskedStr(mask, "serviceConfig.ingressSettings", &d.IngressSettings, sc.IngressSettings)
	applyMaskedInt(mask, "serviceConfig.maxInstanceCount", &d.MaxInstanceCount, sc.MaxInstanceCount)
	applyMaskedInt(mask, "serviceConfig.minInstanceCount", &d.MinInstanceCount, sc.MinInstanceCount)

	if mask.covers("serviceConfig.environmentVariables") && (mask.explicit() || sc.EnvironmentVariables != nil) {
		d.EnvironmentVariables = sc.EnvironmentVariables
	}
}

// computeGen2Outputs fills the output-only fields real gen2 GCP reconciles a
// function to: identity, ACTIVE state, the Cloud Run-backed serviceConfig (uri,
// service, revision) and buildConfig.build, plus the defaults for unset config.
func computeGen2Outputs(fn *gen2Function, p v2Path, create bool) {
	now := time.Now().UTC().Format(time.RFC3339)

	fn.Name = p.fullName()
	fn.Environment = "GEN_2"
	fn.State = "ACTIVE"
	fn.UpdateTime = now

	if create {
		fn.CreateTime = now
	}

	if fn.BuildConfig == nil {
		fn.BuildConfig = &gen2BuildConfig{}
	}

	if fn.BuildConfig.DockerRegistry == "" {
		fn.BuildConfig.DockerRegistry = defaultDockerRegistry
	}

	fn.BuildConfig.Build = "projects/" + p.project + "/locations/" + p.location + "/builds/" + randomToken()

	if fn.ServiceConfig == nil {
		fn.ServiceConfig = &gen2ServiceConfig{}
	}

	applyServiceConfigDefaults(fn.ServiceConfig, p)

	fn.ServiceConfig.Service = "projects/" + p.project + "/locations/" + p.location + "/services/" + p.name
	fn.ServiceConfig.Revision = p.name + "-" + revisionSuffix(fn.revisionSeq)
	fn.ServiceConfig.URI = gen2URI(p)
	fn.URL = fn.ServiceConfig.URI
}

func applyServiceConfigDefaults(sc *gen2ServiceConfig, p v2Path) {
	if sc.ServiceAccountEmail == "" {
		sc.ServiceAccountEmail = gen2ServiceAccount(p.project)
	}

	if sc.AvailableMemory == "" {
		sc.AvailableMemory = gen2DefaultMemory
	}

	if sc.AvailableCPU == "" {
		sc.AvailableCPU = gen2DefaultCPU
	}

	if sc.TimeoutSeconds == 0 {
		sc.TimeoutSeconds = gen2DefaultTimeout
	}

	if sc.IngressSettings == "" {
		sc.IngressSettings = defaultIngress
	}
}

func gen2URI(p v2Path) string {
	return "https://" + p.name + "-" + randomToken() + "." + p.location + ".run.app"
}

func revisionSuffix(seq int) string {
	return "000" + strconv.Itoa(seq) + "-" + randomToken()[:3]
}

// resourceAsResponseV2 renders a gen2 function as an LRO response payload with
// the v2 Function type URL.
func resourceAsResponseV2(fn *gen2Function) map[string]any {
	b, err := json.Marshal(fn)
	if err != nil {
		return nil
	}

	out := map[string]any{
		"@type": "type.googleapis.com/google.cloud.functions.v2.Function",
	}

	var fields map[string]any
	_ = json.Unmarshal(b, &fields)

	for k, v := range fields {
		out[k] = v
	}

	return out
}

// cloneGen2 returns a deep copy of fn: the nested BuildConfig/ServiceConfig/
// EventTrigger structs and every map are cloned, so the copy shares no mutable
// state with the stored function. Readers clone under the lock and then marshal
// the copy after releasing it, so a concurrent patchV2 (which mutates the stored
// serviceConfig in place and reassigns nested pointers) can never be observed
// mid-write. A shallow struct copy is not enough — it still aliases the nested
// pointers and maps.
func cloneGen2(fn *gen2Function) *gen2Function {
	if fn == nil {
		return nil
	}

	out := *fn
	out.Labels = cloneStringMap(fn.Labels)

	if fn.BuildConfig != nil {
		bc := *fn.BuildConfig
		bc.EnvironmentVariables = cloneStringMap(fn.BuildConfig.EnvironmentVariables)

		if fn.BuildConfig.Source != nil {
			src := *fn.BuildConfig.Source

			if fn.BuildConfig.Source.StorageSource != nil {
				ss := *fn.BuildConfig.Source.StorageSource
				src.StorageSource = &ss
			}

			bc.Source = &src
		}

		out.BuildConfig = &bc
	}

	if fn.ServiceConfig != nil {
		sc := *fn.ServiceConfig
		sc.EnvironmentVariables = cloneStringMap(fn.ServiceConfig.EnvironmentVariables)
		out.ServiceConfig = &sc
	}

	if fn.EventTrigger != nil {
		et := *fn.EventTrigger
		if fn.EventTrigger.EventFilters != nil {
			et.EventFilters = append([]gen2EventFilter(nil), fn.EventTrigger.EventFilters...)
		}

		out.EventTrigger = &et
	}

	return &out
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}

	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}

	return out
}

// randomToken returns a short random hex string for synthesized ids.
func randomToken() string {
	b := make([]byte, buildIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "0"
	}

	return hex.EncodeToString(b)
}
