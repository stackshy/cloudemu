package sagemaker

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

const maxInvokeBytes = 6 << 20 // 6 MiB, SageMaker's real-time payload limit

// serveRuntime handles POST /endpoints/{name}/invocations and
// /endpoints/{name}/async-invocations (sagemaker-runtime restJson1).
func (h *Handler) serveRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		wire.WriteJSONError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")

		return
	}

	trimmed := strings.TrimPrefix(r.URL.Path, "/endpoints/")

	switch {
	case strings.HasSuffix(trimmed, "/async-invocations"):
		h.invokeAsync(w, r, decodeName(strings.TrimSuffix(trimmed, "/async-invocations")))
	case strings.HasSuffix(trimmed, "/invocations"):
		h.invoke(w, r, decodeName(strings.TrimSuffix(trimmed, "/invocations")))
	default:
		wire.WriteJSONError(w, http.StatusNotFound, "ResourceNotFound", "unsupported runtime path")
	}
}

func decodeName(raw string) string {
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}

	return raw
}

func (h *Handler) invoke(w http.ResponseWriter, r *http.Request, endpointName string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInvokeBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			wire.WriteJSONError(w, http.StatusBadRequest, "ValidationError",
				"Payload exceeds the 6291456 byte limit for real-time inference")

			return
		}

		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationError", "failed to read request body")

		return
	}

	out, err := h.svc.InvokeEndpoint(r.Context(), driver.InvokeEndpointInput{
		EndpointName:           endpointName,
		ContentType:            r.Header.Get("Content-Type"),
		Accept:                 r.Header.Get("Accept"),
		Body:                   body,
		InferenceComponentName: r.Header.Get("X-Amzn-Sagemaker-Inference-Component"),
		TargetModel:            r.Header.Get("X-Amzn-Sagemaker-Target-Model"),
		TargetVariant:          r.Header.Get("X-Amzn-Sagemaker-Target-Variant"),
	})
	if err != nil {
		writeRuntimeError(w, err)

		return
	}

	// The echoed body is caller-controlled, so a markup type is never served as
	// markup and the type is not sniffed.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if out.ContentType != "" {
		w.Header().Set("Content-Type", safeContentType(out.ContentType))
	}

	if out.InvokedVariant != "" {
		w.Header().Set("X-Amzn-Invoked-Production-Variant", out.InvokedVariant)
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Body)
}

// safeContentType maps a content type a browser would render as active
// content to application/octet-stream.
func safeContentType(ct string) string {
	media := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])) //nolint:mnd // protocol constant

	switch media {
	case "text/html", "application/xhtml+xml", "image/svg+xml", "text/xml", "application/xml":
		return "application/octet-stream"
	default:
		return ct
	}
}

func (h *Handler) invokeAsync(w http.ResponseWriter, r *http.Request, endpointName string) {
	out, err := h.svc.InvokeEndpointAsync(r.Context(), driver.InvokeEndpointAsyncInput{
		EndpointName: endpointName,
		InputS3URI:   r.Header.Get("X-Amzn-Sagemaker-Inputlocation"),
		ContentType:  r.Header.Get("Content-Type"),
	})
	if err != nil {
		writeRuntimeError(w, err)

		return
	}

	w.Header().Set("X-Amzn-Sagemaker-Outputlocation", out.OutputS3URI)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"InferenceId": out.InferenceID})
}

// writeRuntimeError maps driver errors for the restJson1 runtime surface.
func writeRuntimeError(w http.ResponseWriter, err error) {
	var modelErr *driver.ModelError
	if errors.As(err, &modelErr) {
		wire.WriteJSONError(w, http.StatusFailedDependency, driver.ExModelError, modelErr.Message)

		return
	}

	switch {
	case cerrors.IsNotFound(err), cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationError", cerrors.Message(err))
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationError", cerrors.Message(err))
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalFailure", cerrors.Message(err))
	}
}
