package codeartifact

import (
	"net/http"
	"strconv"
)

func (h *Handler) getAuthorizationToken(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var seconds int64

	if s := q.Get("duration"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			writeValidation(w, "duration must be an integer number of seconds")

			return
		}

		seconds = n
	}

	tok, err := h.access.GetAuthorizationToken(r.Context(), domainRefFromQuery(q), seconds)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"authorizationToken": tok.Token, "expiration": epochSeconds(tok.Expiration)})
}

func (h *Handler) getRepositoryEndpoint(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	endpoint, err := h.access.GetRepositoryEndpoint(r.Context(), repoRefFromQuery(q), q.Get("format"), q.Get("endpointType"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"repositoryEndpoint": endpoint})
}
