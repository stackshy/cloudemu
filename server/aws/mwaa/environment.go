package mwaa

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/mwaa/driver"
)

func (h *Handler) createEnvironment(w http.ResponseWriter, r *http.Request, name string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	out, err := h.mw.CreateEnvironment(r.Context(), &driver.CreateEnvironmentInput{
		Name:   name,
		Tags:   tagsFromBody(raw),
		Config: raw,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"Arn": out.Arn})
}

func (h *Handler) getEnvironment(w http.ResponseWriter, r *http.Request, name string) {
	out, err := h.mw.GetEnvironment(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"Environment": environmentToWire(out)})
}

func (h *Handler) updateEnvironment(w http.ResponseWriter, r *http.Request, name string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	out, err := h.mw.UpdateEnvironment(r.Context(), &driver.UpdateEnvironmentInput{
		Name:   name,
		Config: raw,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"Arn": out.Arn})
}

func (h *Handler) deleteEnvironment(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.mw.DeleteEnvironment(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listEnvironments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	names, next, err := h.mw.ListEnvironments(r.Context(), driver.Page{
		NextToken:  q.Get("NextToken"),
		MaxResults: atoiDefault(q.Get("MaxResults")),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{"Environments": names}
	if next != "" {
		body["NextToken"] = next
	}

	writeJSON(w, body)
}

// tagsFromBody extracts the modeled Tags map from a raw request body.
func tagsFromBody(raw map[string]json.RawMessage) map[string]string {
	v, ok := raw["Tags"]
	if !ok {
		return nil
	}

	var tags map[string]string
	if json.Unmarshal(v, &tags) != nil {
		return nil
	}

	return tags
}

// maxQueryInt bounds a parsed query integer so the result fits an int32 without
// overflow; MWAA's MaxResults tops out well below this.
const maxQueryInt = 1 << 20

// atoiDefault parses a non-negative query int32, returning 0 when empty or
// invalid. It is bounded by maxQueryInt so the conversion cannot overflow.
func atoiDefault(s string) int32 {
	n := 0

	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}

		n = n*10 + int(c-'0')
		if n > maxQueryInt {
			return maxQueryInt
		}
	}

	return int32(n)
}
