package opensearch

import "net/http"

func (h *Handler) listVersions(w http.ResponseWriter, r *http.Request) {
	versions, next, err := h.os.ListVersions(r.Context(), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"Versions": versions}, next))
}

func (h *Handler) getCompatibleVersions(w http.ResponseWriter, r *http.Request) {
	compat, err := h.os.GetCompatibleVersions(r.Context(), r.URL.Query().Get("domainName"))
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(compat))
	for src, targets := range compat {
		out = append(out, map[string]any{"SourceVersion": src, "TargetVersions": targets})
	}

	writeJSON(w, map[string]any{"CompatibleVersions": out})
}

// describeInstanceTypeLimits handles GET /instanceTypeLimits/{EngineVersion}/{InstanceType}.
func (h *Handler) describeInstanceTypeLimits(w http.ResponseWriter, r *http.Request, rest []string) {
	const wantSegs = 2
	if len(rest) != wantSegs {
		notFoundPath(w, r.URL.Path)

		return
	}

	limits, err := h.os.DescribeInstanceTypeLimits(r.Context(), rest[0], rest[1], r.URL.Query().Get("domainName"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, limits)
}

// listInstanceTypeDetails handles GET /instanceTypeDetails/{EngineVersion}.
func (h *Handler) listInstanceTypeDetails(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 {
		notFoundPath(w, r.URL.Path)

		return
	}

	list, next, err := h.os.ListInstanceTypeDetails(r.Context(), rest[0], pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"InstanceTypeDetails": list}, next))
}
