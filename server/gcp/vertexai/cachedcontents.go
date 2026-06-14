package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

func cachedContentJSON(c *driver.CachedContent) map[string]any {
	return map[string]any{
		"name": c.Name, "model": c.Model, "displayName": c.DisplayName,
		"createTime": c.CreateTime, "expireTime": c.ExpireTime,
	}
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveCachedContents(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listCachedContents(w, r, p.location)
		case http.MethodPost:
			h.createCachedContent(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getCachedContent(w, r, p.name)
	case http.MethodDelete:
		h.deleteCachedContent(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) createCachedContent(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		Model       string `json:"model"`
		DisplayName string `json:"displayName"`
		TTL         struct {
			Seconds int `json:"seconds"`
		} `json:"ttl"`
	}

	if !decode(w, r, &req) {
		return
	}

	cc, err := h.svc.CreateCachedContent(r.Context(), driver.CachedContentConfig{
		Location: location, Model: req.Model, DisplayName: req.DisplayName, TTLSeconds: req.TTL.Seconds,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, cachedContentJSON(cc))
}

func (h *Handler) getCachedContent(w http.ResponseWriter, r *http.Request, name string) {
	cc, err := h.svc.GetCachedContent(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, cachedContentJSON(cc))
}

func (h *Handler) listCachedContents(w http.ResponseWriter, r *http.Request, location string) {
	ccs, err := h.svc.ListCachedContents(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(ccs))
	for i := range ccs {
		out = append(out, cachedContentJSON(&ccs[i]))
	}

	writeJSON(w, map[string]any{"cachedContents": out})
}

func (h *Handler) deleteCachedContent(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.svc.DeleteCachedContent(r.Context(), name); err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}
