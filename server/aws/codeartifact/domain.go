package codeartifact

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func (h *Handler) createDomain(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	out, err := h.ca.CreateDomain(r.Context(), &driver.CreateDomainInput{
		Name:          r.URL.Query().Get("domain"),
		EncryptionKey: rawString(raw, "encryptionKey"),
		Tags:          tagsFromBody(raw),
	})
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
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"domain": domainToWire(out)})
}

func (h *Handler) listDomains(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	domains, next, err := h.ca.ListDomains(r.Context(), driver.Page{
		NextToken:  rawString(raw, "nextToken"),
		MaxResults: rawInt32(raw, "maxResults"),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	summaries := make([]map[string]any, 0, len(domains))
	for _, d := range domains {
		summaries = append(summaries, domainSummaryToWire(d))
	}

	body := map[string]any{"domains": summaries}
	if next != "" {
		body["nextToken"] = next
	}

	writeJSON(w, body)
}

// rawString decodes a JSON string field from a raw request body, returning ""
// when the field is absent or not a string.
func rawString(raw map[string]json.RawMessage, key string) string {
	v, ok := raw[key]
	if !ok {
		return ""
	}

	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}

	return s
}

// rawInt32 decodes a JSON number field from a raw request body, returning 0 when
// the field is absent or not a number.
func rawInt32(raw map[string]json.RawMessage, key string) int32 {
	v, ok := raw[key]
	if !ok {
		return 0
	}

	var n int32
	if json.Unmarshal(v, &n) != nil {
		return 0
	}

	return n
}

// maxQueryInt bounds a parsed query integer so the result fits an int32 without
// overflow.
const maxQueryInt = 1 << 20

// atoiDefault parses a base-10 int32 from s, returning 0 when s is empty or
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
