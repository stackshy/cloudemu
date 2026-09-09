package mwaa

import "net/http"

// serveToken handles POST /clitoken/{Name} and POST /webtoken/{Name}. rest is
// the path below the token root, so it carries exactly the environment name.
func (h *Handler) serveToken(w http.ResponseWriter, r *http.Request, rest []string, kind string) {
	if len(rest) != 1 {
		notFoundPath(w, r.URL.Path)

		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	name := rest[0]

	var (
		token, hostname string
		err             error
	)

	if kind == tokenKindCli {
		token, hostname, err = h.mw.CreateCliToken(r.Context(), name)
	} else {
		token, hostname, err = h.mw.CreateWebLoginToken(r.Context(), name)
	}

	if err != nil {
		writeErr(w, err)

		return
	}

	if kind == tokenKindCli {
		writeJSON(w, map[string]any{"CliToken": token, "WebServerHostname": hostname})

		return
	}

	writeJSON(w, map[string]any{"WebToken": token, "WebServerHostname": hostname})
}
