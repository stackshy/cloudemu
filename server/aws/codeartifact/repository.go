package codeartifact

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// createRepositoryRequest is the CreateRepository body.
type createRepositoryRequest struct {
	Description string         `json:"description"`
	Upstreams   []upstreamPair `json:"upstreams"`
	Tags        []tagPair      `json:"tags"`
}

func (h *Handler) createRepository(w http.ResponseWriter, r *http.Request) {
	var req createRepositoryRequest
	if !decodeBody(w, r, &req) {
		return
	}

	q := r.URL.Query()

	out, err := h.ca.CreateRepository(r.Context(), &driver.CreateRepositoryInput{
		Domain: q.Get("domain"), DomainOwner: q.Get("domain-owner"), Repository: q.Get("repository"),
		Description: req.Description, Upstreams: upstreamsFromList(req.Upstreams), Tags: tagsToMap(req.Tags),
	})
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

// updateRepositoryRequest is the UpdateRepository body. A nil field was absent
// from the request and is left unchanged; an empty upstream list clears them.
type updateRepositoryRequest struct {
	Description *string         `json:"description"`
	Upstreams   *[]upstreamPair `json:"upstreams"`
}

func (h *Handler) updateRepository(w http.ResponseWriter, r *http.Request) {
	var req updateRepositoryRequest
	if !decodeBody(w, r, &req) {
		return
	}

	q := r.URL.Query()
	in := &driver.UpdateRepositoryInput{
		Domain: q.Get("domain"), DomainOwner: q.Get("domain-owner"), Repository: q.Get("repository"), Description: req.Description,
	}

	if req.Upstreams != nil {
		ups := upstreamsFromList(*req.Upstreams)
		in.Upstreams = &ups
	}

	out, err := h.ca.UpdateRepository(r.Context(), in)
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

	page, ok := pageFromQuery(w, q)
	if !ok {
		return
	}

	repos, next, err := h.ca.ListRepositories(r.Context(), q.Get("repository-prefix"), page)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeRepositorySummaries(w, repos, next)
}

func (h *Handler) listRepositoriesInDomain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, ok := pageFromQuery(w, q)
	if !ok {
		return
	}

	var (
		repos []*driver.Repository
		next  string
		err   error
	)

	if admin := q.Get("administrator-account"); admin != "" && h.adminLs != nil {
		repos, next, err = h.adminLs.ListRepositoriesInDomainByAdministrator(r.Context(), domainRefFromQuery(q), admin,
			q.Get("repository-prefix"), page)
	} else {
		repos, next, err = h.ca.ListRepositoriesInDomain(r.Context(), q.Get("domain"), q.Get("domain-owner"),
			q.Get("repository-prefix"), page)
	}

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
	putNonEmpty(body, "nextToken", next)

	writeJSON(w, body)
}
