package opensearch

import "net/http"

// serveServiceSoftware handles /opensearch/serviceSoftwareUpdate/{start,cancel,rollback}.
func (h *Handler) serveServiceSoftware(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 || r.Method != http.MethodPost {
		notFoundPath(w, r.URL.Path)

		return
	}

	var req struct {
		DomainName string `json:"DomainName"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	handler := map[string]func(string) (map[string]jsonRaw, error){
		"start":    func(n string) (map[string]jsonRaw, error) { return h.os.StartServiceSoftwareUpdate(r.Context(), n) },
		"cancel":   func(n string) (map[string]jsonRaw, error) { return h.os.CancelServiceSoftwareUpdate(r.Context(), n) },
		"rollback": func(n string) (map[string]jsonRaw, error) { return h.os.RollbackServiceSoftwareUpdate(r.Context(), n) },
	}

	fn, ok := handler[rest[0]]
	if !ok {
		notFoundPath(w, r.URL.Path)

		return
	}

	out, err := fn(req.DomainName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

// serveUpgradeDomain handles POST /opensearch/upgradeDomain and the
// GET /opensearch/upgradeDomain/{name}/{history,status} reads.
func (h *Handler) serveUpgradeDomain(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method == http.MethodPost {
			h.upgradeDomain(w, r)

			return
		}

		methodNotAllowed(w)

		return
	}

	const wantSegs = 2
	if len(rest) != wantSegs || r.Method != http.MethodGet {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch rest[1] {
	case segHistory:
		h.getUpgradeHistory(w, r, rest[0])
	case "status":
		h.getUpgradeStatus(w, r, rest[0])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

func (h *Handler) upgradeDomain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainName       string            `json:"DomainName"`
		TargetVersion    string            `json:"TargetVersion"`
		PerformCheckOnly bool              `json:"PerformCheckOnly"`
		AdvancedOptions  map[string]string `json:"AdvancedOptions"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	id, err := h.os.UpgradeDomain(r.Context(), req.DomainName, req.TargetVersion, !req.PerformCheckOnly, req.AdvancedOptions)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"UpgradeId":        id,
		"DomainName":       req.DomainName,
		"TargetVersion":    req.TargetVersion,
		"PerformCheckOnly": req.PerformCheckOnly,
		"AdvancedOptions":  req.AdvancedOptions,
	})
}

func (h *Handler) getUpgradeStatus(w http.ResponseWriter, r *http.Request, name string) {
	step, err := h.os.GetUpgradeStatus(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"UpgradeStep": step.UpgradeStep,
		"StepStatus":  step.StepStatus,
		"UpgradeName": "",
	})
}

func (h *Handler) getUpgradeHistory(w http.ResponseWriter, r *http.Request, name string) {
	list, next, err := h.os.GetUpgradeHistory(r.Context(), name, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	histories := make([]map[string]any, 0, len(list))
	for _, hst := range list {
		histories = append(histories, upgradeHistoryToWire(hst))
	}

	writeJSON(w, withNext(map[string]any{"UpgradeHistories": histories}, next))
}
