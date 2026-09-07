package codeartifact

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func (h *Handler) createRepository(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()

	in := &driver.CreateRepositoryInput{
		Domain:      q.Get("domain"),
		DomainOwner: q.Get("domain-owner"),
		Repository:  q.Get("repository"),
		Description: rawString(raw, "description"),
		Tags:        tagsFromBody(raw),
	}
	if ups := upstreamsFromBody(raw); ups != nil {
		in.Upstreams = *ups
	}

	out, err := h.ca.CreateRepository(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"repository": repositoryToWire(out)})
}

func (h *Handler) describeRepository(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	out, err := h.ca.DescribeRepository(r.Context(), q.Get("domain"), q.Get("domain-owner"), q.Get("repository"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"repository": repositoryToWire(out)})
}

func (h *Handler) updateRepository(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()

	out, err := h.ca.UpdateRepository(r.Context(), &driver.UpdateRepositoryInput{
		Domain:      q.Get("domain"),
		DomainOwner: q.Get("domain-owner"),
		Repository:  q.Get("repository"),
		Description: stringPtrFromBody(raw, "description"),
		Upstreams:   upstreamsFromBody(raw),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"repository": repositoryToWire(out)})
}

func (h *Handler) deleteRepository(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	out, err := h.ca.DeleteRepository(r.Context(), q.Get("domain"), q.Get("domain-owner"), q.Get("repository"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"repository": repositoryToWire(out)})
}

func (h *Handler) listRepositories(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	repos, next, err := h.ca.ListRepositories(r.Context(), q.Get("repository-prefix"), driver.Page{
		NextToken:  q.Get("next-token"),
		MaxResults: atoiDefault(q.Get("max-results")),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeRepositorySummaries(w, repos, next)
}

func (h *Handler) listRepositoriesInDomain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	q := r.URL.Query()

	repos, next, err := h.ca.ListRepositoriesInDomain(r.Context(), q.Get("domain"), q.Get("domain-owner"),
		q.Get("repository-prefix"), driver.Page{
			NextToken:  q.Get("next-token"),
			MaxResults: atoiDefault(q.Get("max-results")),
		})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeRepositorySummaries(w, repos, next)
}

func (h *Handler) associateExternalConnection(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	out, err := h.ca.AssociateExternalConnection(r.Context(), q.Get("domain"), q.Get("domain-owner"),
		q.Get("repository"), q.Get("external-connection"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"repository": repositoryToWire(out)})
}

func (h *Handler) disassociateExternalConnection(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	out, err := h.ca.DisassociateExternalConnection(r.Context(), q.Get("domain"), q.Get("domain-owner"),
		q.Get("repository"), q.Get("external-connection"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"repository": repositoryToWire(out)})
}

// writeRepositorySummaries writes a ListRepositories/ListRepositoriesInDomain
// response body from a page of repositories.
func writeRepositorySummaries(w http.ResponseWriter, repos []*driver.Repository, next string) {
	summaries := make([]map[string]any, 0, len(repos))
	for _, r := range repos {
		summaries = append(summaries, repositorySummaryToWire(r))
	}

	body := map[string]any{"repositories": summaries}
	if next != "" {
		body["nextToken"] = next
	}

	writeJSON(w, body)
}
