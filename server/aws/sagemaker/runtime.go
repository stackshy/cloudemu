package sagemaker

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/sagemaker/driver"
	"github.com/stackshy/cloudemu/server/wire"
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
	body, err := io.ReadAll(io.LimitReader(r.Body, maxInvokeBytes))
	if err != nil {
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
	})
	if err != nil {
		writeRuntimeError(w, err)

		return
	}

	if out.ContentType != "" {
		w.Header().Set("Content-Type", out.ContentType)
	}

	if out.InvokedVariant != "" {
		w.Header().Set("X-Amzn-Invoked-Production-Variant", out.InvokedVariant)
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Body)
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
	w.Header().Set("X-Amzn-Sagemaker-Inferenceid", out.InferenceID)
	w.WriteHeader(http.StatusAccepted)
}

// writeRuntimeError maps driver errors for the restJson1 runtime surface.
func writeRuntimeError(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusNotFound, "ValidationError", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationError", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalFailure", err.Error())
	}
}
