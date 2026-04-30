// Package lambda implements the AWS Lambda REST+JSON control-plane protocol
// as a server.Handler. Point a real aws-sdk-go-v2 Lambda client at a Server
// registered with this handler and operations work against an in-memory
// serverless driver.
//
// MVP coverage: CreateFunction, GetFunction, ListFunctions, DeleteFunction,
// Invoke (synchronous). Versions, aliases, layers, concurrency configs, and
// event source mappings are not yet wired through — the driver supports them
// but the wire surface is deferred to a follow-up.
package lambda

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	sdrv "github.com/stackshy/cloudemu/serverless/driver"
)

// pathPrefix is the Lambda API version prefix every control-plane URL starts
// with. We match on this so the handler doesn't accidentally swallow generic
// REST traffic that should fall through to the S3 catch-all.
const pathPrefix = "/2015-03-31/functions"

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 6 << 20 // 6 MiB — Lambda's sync invocation payload limit.
)

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
	return strings.HasPrefix(r.URL.Path, pathPrefix)
}

// ServeHTTP dispatches Lambda operations based on path shape and method.
//
//	/2015-03-31/functions                       GET=list, POST=create
//	/2015-03-31/functions/{name}                GET=get, DELETE=delete
//	/2015-03-31/functions/{name}/invocations    POST=invoke
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, pathPrefix)
	rest = strings.TrimPrefix(rest, "/")

	if rest == "" {
		h.serveCollection(w, r)
		return
	}

	parts := strings.Split(rest, "/")
	name := parts[0]

	const (
		partsResource = 1 // /functions/{name}
		partsInvoke   = 2 // /functions/{name}/invocations
	)

	switch len(parts) {
	case partsResource:
		h.serveResource(w, r, name)
	case partsInvoke:
		if parts[1] == "invocations" {
			h.serveInvoke(w, r, name)
			return
		}

		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
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
