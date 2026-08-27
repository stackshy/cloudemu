package lambda

import (
	"net/http"
	"strings"

	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// concurrencyFunctionName returns the function name for a reserved-concurrency
// request (.../functions/{name}/concurrency) and whether the path is one. Put
// and Delete live under 2017-10-31, Get under 2019-09-30; both end in
// /concurrency with a single-segment function name.
func concurrencyFunctionName(path string) (string, bool) {
	for _, prefix := range []string{concurrencyWritePrefix, concurrencyReadPrefix} {
		if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, concurrencySuffix) {
			continue
		}

		rest := strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")
		name := strings.TrimSuffix(rest, concurrencySuffix)

		if name != "" && !strings.Contains(name, "/") {
			return name, true
		}
	}

	return "", false
}

// serveConcurrency handles the reserved-concurrency sub-resource:
// PUT=PutFunctionConcurrency, GET=GetFunctionConcurrency,
// DELETE=DeleteFunctionConcurrency.
func (h *Handler) serveConcurrency(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPut:
		var req struct {
			ReservedConcurrentExecutions int `json:"ReservedConcurrentExecutions"`
		}

		if !decodeJSON(w, r, &req) {
			return
		}

		err := h.fn.PutFunctionConcurrency(r.Context(), sdrv.ConcurrencyConfig{
			FunctionName:                 name,
			ReservedConcurrentExecutions: req.ReservedConcurrentExecutions,
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"ReservedConcurrentExecutions": req.ReservedConcurrentExecutions})
	case http.MethodGet:
		// GetFunctionConcurrency 404s only when the FUNCTION is missing. A function
		// with no reserved concurrency set is HTTP 200 with an empty body, matching
		// AWS — the provider reports NotFound for both cases, so the function's
		// existence is checked separately here.
		if _, err := h.fn.GetFunction(r.Context(), name); err != nil {
			writeErr(w, err)
			return
		}

		cfg, err := h.fn.GetFunctionConcurrency(r.Context(), name)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"ReservedConcurrentExecutions": cfg.ReservedConcurrentExecutions})
	case http.MethodDelete:
		if err := h.fn.DeleteFunctionConcurrency(r.Context(), name); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}
