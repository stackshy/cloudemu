package codeartifact

import (
	"net/http"
)

// createDomainRequest is the CreateDomain body.
type createDomainRequest struct {
	EncryptionKey string    `json:"encryptionKey"`
	Tags          []tagPair `json:"tags"`
}

func (h *Handler) createDomain(w http.ResponseWriter, r *http.Request) {
	var req createDomainRequest
	if !decodeBody(w, r, &req) {
		return
	}

	out, err := h.ca.CreateDomain(r.Context(), newCreateDomainInput(r.URL.Query().Get("domain"), &req))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"domain": domainToWire(out)})
}

func (h *Handler) describeDomain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	out, err := h.ca.DescribeDomain(r.Context(), q.Get("domain"), q.Get("domain-owner"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"domain": domainToWire(out)})
}

func (h *Handler) deleteDomain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	out, err := h.ca.DeleteDomain(r.Context(), q.Get("domain"), q.Get("domain-owner"))
	if err != nil {
		writeErrNoNotFound(w, err)

		return
	}

	writeJSON(w, map[string]any{"domain": domainToWire(out)})
}

// listPageRequest is the body of the operations that carry paging in the body.
type listPageRequest struct {
	MaxResults *int32 `json:"maxResults"`
	NextToken  string `json:"nextToken"`
}

func (h *Handler) listDomains(w http.ResponseWriter, r *http.Request) {
	var req listPageRequest
	if !decodeBody(w, r, &req) {
		return
	}

	page, ok := pageFromBody(w, &req)
	if !ok {
		return
	}

	domains, next, err := h.ca.ListDomains(r.Context(), page)
	if err != nil {
		writeErr(w, err)

		return
	}

	summaries := make([]map[string]any, 0, len(domains))
	for _, d := range domains {
		summaries = append(summaries, domainSummaryToWire(d))
	}

	body := map[string]any{"domains": summaries}
	putNonEmpty(body, "nextToken", next)

	writeJSON(w, body)
}
