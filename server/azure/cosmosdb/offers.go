package cosmosdb

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// offerState is the provisioned throughput recorded for a container.
type offerState struct {
	manualThroughput int32
	autoscaleMax     int32
	autoscale        bool
}

// containerRID mirrors the "_rid" makeContainerResource assigns a container, so
// the offer store and the SDK (which reads a container's _rid, then queries the
// offer by that resource id) agree on the key.
func containerRID(coll string) string {
	return "rid-" + coll
}

// recordOffer captures the throughput a container was created with. Real Cosmos
// only materializes a dedicated offer when throughput is provisioned on the
// container; a container created without it (shared/serverless) has no offer,
// so ReadThroughput 404s — which the empty-store path reproduces.
func (h *Handler) recordOffer(coll string, r *http.Request) {
	st, ok := parseOfferHeaders(r)
	if !ok {
		return
	}

	h.offerMu.Lock()
	h.offers[containerRID(coll)] = st
	h.offerMu.Unlock()
}

func (h *Handler) deleteOffer(coll string) {
	h.offerMu.Lock()
	delete(h.offers, containerRID(coll))
	h.offerMu.Unlock()
}

// parseOfferHeaders reads the manual (x-ms-offer-throughput) or autoscale
// (x-ms-cosmos-offer-autopilot-settings) throughput headers the SDK sets on a
// container create.
func parseOfferHeaders(r *http.Request) (offerState, bool) {
	if v := r.Header.Get("X-Ms-Offer-Throughput"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 {
			return offerState{manualThroughput: int32(n)}, true
		}
	}

	if v := r.Header.Get("X-Ms-Cosmos-Offer-Autopilot-Settings"); v != "" {
		var settings struct {
			MaxThroughput int32 `json:"maxThroughput"`
		}

		if err := json.Unmarshal([]byte(v), &settings); err == nil && settings.MaxThroughput > 0 {
			return offerState{autoscaleMax: settings.MaxThroughput, autoscale: true}, true
		}
	}

	return offerState{}, false
}

// serveOffers routes the /offers throughput resource: POST /offers is the
// offer query the SDK fires to locate a container's offer; GET/PUT /offers/{id}
// read and replace it. path is the request path with any /{account} prefix
// already peeled off; the offer id itself encodes the account (see
// containerRID / qualify), so offers need no separate account dimension.
func (h *Handler) serveOffers(w http.ResponseWriter, r *http.Request, path string) {
	if path == offersPath {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		h.queryOffers(w, r)

		return
	}

	rid := strings.TrimPrefix(path, offersPathPrefix)

	switch r.Method {
	case http.MethodGet:
		h.readOffer(w, rid)
	case http.MethodPut:
		h.replaceOffer(w, r, rid)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// queryOffers answers the SDK's offer lookup. It returns the single offer whose
// offerResourceId matches the container rid named in the WHERE clause, in the
// {"Offers":[...]} envelope ReadThroughputIfExists expects. No match yields an
// empty list, which the SDK maps to 404 (no dedicated throughput).
func (h *Handler) queryOffers(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeQueryBody(w, r)
	if !ok {
		return
	}

	rid := offerResourceIDFromQuery(body.Query)

	offers := []map[string]any{}

	if rid != "" {
		h.offerMu.RLock()
		st, found := h.offers[rid]
		h.offerMu.RUnlock()

		if found {
			offers = append(offers, offerDocument(rid, st))
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"_rid":   "cloudemu",
		"Offers": offers,
		"_count": len(offers),
	})
}

func (h *Handler) readOffer(w http.ResponseWriter, rid string) {
	h.offerMu.RLock()
	st, found := h.offers[rid]
	h.offerMu.RUnlock()

	if !found {
		writeError(w, http.StatusNotFound, "NotFound", "offer not found")
		return
	}

	writeJSON(w, http.StatusOK, offerDocument(rid, st))
}

func (h *Handler) replaceOffer(w http.ResponseWriter, r *http.Request, rid string) {
	h.offerMu.RLock()
	_, found := h.offers[rid]
	h.offerMu.RUnlock()

	if !found {
		writeError(w, http.StatusNotFound, "NotFound", "offer not found")
		return
	}

	item, ok := decodeAnyJSON(w, r)
	if !ok {
		return
	}

	st := offerFromBody(item)

	h.offerMu.Lock()
	h.offers[rid] = st
	h.offerMu.Unlock()

	writeJSON(w, http.StatusOK, offerDocument(rid, st))
}

// offerFromBody reads the throughput out of a replace request's Throughput
// properties body ({"content":{"offerThroughput":N}} or autopilot settings).
func offerFromBody(item map[string]any) offerState {
	content, _ := item["content"].(map[string]any)
	if content == nil {
		return offerState{}
	}

	if v, ok := numericValue(content["offerThroughput"]); ok && v > 0 {
		return offerState{manualThroughput: int32(v)}
	}

	if settings, ok := content["offerAutopilotSettings"].(map[string]any); ok {
		if v, ok := numericValue(settings["maxThroughput"]); ok && v > 0 {
			return offerState{autoscaleMax: int32(v), autoscale: true}
		}
	}

	return offerState{}
}

// offerDocument renders one offer resource in the V2 wire shape the azcosmos
// ThroughputProperties unmarshaller reads.
func offerDocument(rid string, st offerState) map[string]any {
	content := map[string]any{}
	if st.autoscale {
		content["offerAutopilotSettings"] = map[string]any{"maxThroughput": st.autoscaleMax}
	} else {
		content["offerThroughput"] = st.manualThroughput
	}

	self := "offers/" + rid

	return map[string]any{
		"id":              rid,
		"_rid":            rid,
		"_self":           self,
		"_etag":           `"` + rid + `"`,
		"_ts":             time.Now().Unix(),
		"resource":        "colls/" + rid + "/",
		"offerResourceId": rid,
		"offerType":       "Invalid",
		"offerVersion":    "V2",
		"content":         content,
	}
}

// offerResourceIDFromQuery extracts the value bound to offerResourceId in the
// SDK's offer lookup query, e.g. SELECT * FROM c WHERE c.offerResourceId =
// 'rid-users'. Returns "" when the clause is absent.
func offerResourceIDFromQuery(query string) string {
	const marker = "offerResourceId"

	i := strings.Index(query, marker)
	if i < 0 {
		return ""
	}

	rest := query[i+len(marker):]

	start := strings.IndexByte(rest, '\'')
	if start < 0 {
		return ""
	}

	rest = rest[start+1:]

	end := strings.IndexByte(rest, '\'')
	if end < 0 {
		return ""
	}

	return rest[:end]
}
