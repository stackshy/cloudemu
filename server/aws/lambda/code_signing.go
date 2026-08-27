package lambda

import (
	"net/http"
	"strings"
)

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

// serveFunctionCodeSigningConfig handles the code-signing-config sub-resource.
// cloudemu does not associate code-signing configs with functions, so a GET on
// an existing function returns 200 with an empty CodeSigningConfigArn (matching
// a function that has none). The function's existence is still validated so a
// missing function 404s. Terraform's function read calls this on every refresh;
// before this route existed the request fell through to the S3 catch-all and
// returned XML the REST-JSON client could not parse.
func (h *Handler) serveFunctionCodeSigningConfig(w http.ResponseWriter, r *http.Request) {
	name, ok := codeSigningFunctionName(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
		return
	}

	if _, err := h.fn.GetFunction(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"FunctionName":         name,
			"CodeSigningConfigArn": "",
		})
	case http.MethodDelete:
		// No config is stored, so removal is a no-op that AWS answers 204.
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "unsupported method for code-signing-config")
	}
}
