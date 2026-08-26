package azure

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// propertyOverlay remembers the request properties an ARM handler did not model
// (and therefore dropped), keyed by the resource's ARM id. It lets the Azure
// server echo unmodeled properties back on the create response and on later
// reads, instead of silently discarding them — real Azure preserves properties
// it accepts, and a caller that sets one expects to read it back.
//
// The store is per-server: it is created in New alongside the handlers, so the
// standalone server's reset flow (which rebuilds the whole server) starts each
// run with an empty overlay.
type propertyOverlay struct {
	mu    sync.RWMutex
	store map[string]map[string]any
}

func newPropertyOverlay() *propertyOverlay {
	return &propertyOverlay{store: make(map[string]map[string]any)}
}

// capture records the unmodeled properties for id, replacing any previous
// entry. An empty set clears the entry so a resource that no longer carries
// unmodeled properties (e.g. re-created without them) does not keep stale ones.
func (o *propertyOverlay) capture(id string, unmodeled map[string]any) {
	id = normalizeOverlayKey(id)

	o.mu.Lock()
	defer o.mu.Unlock()

	if len(unmodeled) == 0 {
		delete(o.store, id)
		return
	}

	o.store[id] = unmodeled
}

// lookup returns the unmodeled properties recorded for id, or nil.
func (o *propertyOverlay) lookup(id string) map[string]any {
	id = normalizeOverlayKey(id)

	o.mu.RLock()
	defer o.mu.RUnlock()

	return o.store[id]
}

// normalizeOverlayKey lowercases the resource-group segment of an ARM resource
// id so overlay entries key case-insensitively on the resource group, matching
// ARM's case-insensitive resource-group semantics. A resource created under
// resourceGroups/rg-1 and read back via resourceGroups/RG-1 must resolve the
// same overlay entry; without this, the differently-cased read would miss and
// silently drop unmodeled properties. Only the resource-group segment is
// touched — the rest of the id (including the resource name) is left byte-for-
// byte, so distinct resources never collide. Applied identically on capture,
// lookup and evict, so the internal map key is consistent regardless of the
// casing a request used; the response body's id is never altered.
func normalizeOverlayKey(id string) string {
	const marker = "/resourceGroups/"

	i := strings.Index(id, marker)
	if i < 0 {
		return id
	}

	start := i + len(marker)

	end := strings.IndexByte(id[start:], '/')
	if end < 0 {
		return id[:start] + strings.ToLower(id[start:])
	}

	end += start

	return id[:start] + strings.ToLower(id[start:end]) + id[end:]
}

// evict drops any entry for id — called when a resource is deleted so the
// store does not grow without bound across create/delete cycles.
func (o *propertyOverlay) evict(id string) {
	if id == "" {
		return
	}

	id = normalizeOverlayKey(id)

	o.mu.Lock()
	defer o.mu.Unlock()

	delete(o.store, id)
}

// echoUnmodeledProperties wraps next so that unmodeled properties on ARM
// resource requests survive into the response. It only engages for ARM resource
// paths (which begin with /subscriptions/) so the storage/table/queue
// data-plane handlers — which return XML or binary — are never buffered or
// rewritten. Non-JSON responses, error responses, and responses without a
// top-level id/properties pair pass through untouched.
func echoUnmodeledProperties(next http.Handler, overlay *propertyOverlay) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/subscriptions/") {
			next.ServeHTTP(w, r)
			return
		}

		reqProps := readRequestProperties(r)

		rec := &captureWriter{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		if r.Method == http.MethodDelete && rec.status >= 200 && rec.status < 300 {
			overlay.evict(resourceIDFromPath(r.URL.Path))
		}

		if !rec.rewrite(w, r, reqProps, overlay) {
			rec.flush(w)
		}
	})
}

// resourceIDFromPath reconstructs the ARM resource id from a request path so a
// DELETE can evict the matching overlay entry (DELETE responses carry no body
// to read the id from). Handles both a top-level resource
// (.../{type}/{name}) and a named sub-resource one level down
// (.../{type}/{name}/{subResource}/{subResourceName}, e.g. a SQL database
// under its server) — the sub-resource id is built the same way the handlers
// that create these resources build it (see server/azure/sql childID), so it
// matches the "id" field the overlay was captured under. Returns "" for a path
// that isn't a single named resource or named sub-resource, in which case
// nothing is evicted — e.g. a bodiless action like .../failoverGroups/{n}/
// failover carries a SubResourceAction and is left alone.
func resourceIDFromPath(urlPath string) string {
	rp, ok := azurearm.ParsePath(urlPath)
	if !ok || rp.ResourceName == "" {
		return ""
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, rp.Provider, rp.ResourceType, rp.ResourceName)

	if rp.SubResource == "" {
		return id
	}

	if rp.SubResourceName == "" || rp.SubResourceAction != "" {
		return ""
	}

	return id + "/" + rp.SubResource + "/" + rp.SubResourceName
}

// readRequestProperties returns the request body's top-level "properties"
// object as a generic map, or nil when there is no JSON properties object. The
// body is restored so the downstream handler can read it again.
func readRequestProperties(r *http.Request) map[string]any {
	if r.Body == nil {
		return nil
	}

	body, err := readAndRestoreBody(r)
	if err != nil || len(body) == 0 {
		return nil
	}

	var envelope struct {
		Properties map[string]any `json:"properties"`
	}

	if json.Unmarshal(body, &envelope) != nil {
		return nil
	}

	return envelope.Properties
}

// readAndRestoreBody reads the whole request body and resets r.Body so the
// downstream handler still sees it.
func readAndRestoreBody(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer

	if _, err := buf.ReadFrom(r.Body); err != nil {
		return nil, err
	}

	_ = r.Body.Close()
	body := buf.Bytes()
	r.Body = io.NopCloser(bytes.NewReader(body))

	return body, nil
}

// captureWriter buffers a handler's response so the middleware can inspect and,
// when appropriate, rewrite the JSON body before it reaches the client.
type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *captureWriter) WriteHeader(status int) { c.status = status }

func (c *captureWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}

	return c.body.Write(p)
}

// flush writes the captured response through unchanged.
func (c *captureWriter) flush(w http.ResponseWriter) {
	if c.status == 0 {
		c.status = http.StatusOK
	}

	// Every wrapped ARM response is JSON; pin the content type at the sink so a
	// reflected request value in the body can never be sniffed as HTML.
	writeJSONResponse(w, c.status, c.body.Bytes())
}

// writeJSONResponse writes an application/json response body, forbidding MIME
// sniffing. Pinning the content type and nosniff at the sink is what keeps a
// reflected request value (echoed back into the body) from being interpreted as
// HTML by a browser.
func writeJSONResponse(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", azurearm.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// rewrite merges the recorded and request-carried unmodeled properties into the
// response and writes the result. It returns false (and writes nothing) when
// the response is not a rewritable ARM resource object, leaving the caller to
// flush the original bytes. A collection response ({"value": [...]}) is
// dispatched to rewriteList instead of the single-resource path below.
func (c *captureWriter) rewrite(
	w http.ResponseWriter, r *http.Request, reqProps map[string]any, overlay *propertyOverlay,
) bool {
	if c.status < 200 || c.status >= 300 {
		return false
	}

	if !strings.HasPrefix(c.Header().Get("Content-Type"), azurearm.ContentType) {
		return false
	}

	var resource map[string]any
	if json.Unmarshal(c.body.Bytes(), &resource) != nil {
		return false
	}

	if values, ok := resource["value"].([]any); ok {
		return c.rewriteList(w, resource, values, overlay)
	}

	id, ok := resource["id"].(string)
	if !ok || id == "" {
		return false
	}

	respProps, _ := resource["properties"].(map[string]any)

	unmodeled := captureUnmodeled(r, id, reqProps, respProps, overlay)

	if len(unmodeled) == 0 {
		return false
	}

	resource["properties"] = mergeProperties(respProps, unmodeled)

	rewritten, err := json.Marshal(resource)
	if err != nil {
		return false
	}

	writeJSONResponse(w, c.status, rewritten)

	return true
}

// rewriteList merges each list item's previously-recorded overlay entry into
// its own properties, keyed by the item's own "id". A collection list (e.g.
// RecordSets.ListByDnsZone) carries no request body to capture fresh
// unmodeled properties from — list is read-only — so this only replays what
// an earlier create/update on that same item already recorded; without it, a
// record set with unmodeled data (e.g. an MX or SRV DNS record set, whose
// MXRecords/SRVRecords are not natively modeled) loses that data specifically
// on the list response while a single GET on it still returns it correctly.
func (c *captureWriter) rewriteList(
	w http.ResponseWriter, resource map[string]any, values []any, overlay *propertyOverlay,
) bool {
	changed := false

	for _, v := range values {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}

		id, ok := item["id"].(string)
		if !ok || id == "" {
			continue
		}

		unmodeled := overlay.lookup(id)
		if len(unmodeled) == 0 {
			continue
		}

		respProps, _ := item["properties"].(map[string]any)
		item["properties"] = mergeProperties(respProps, unmodeled)
		changed = true
	}

	if !changed {
		return false
	}

	rewritten, err := json.Marshal(resource)
	if err != nil {
		return false
	}

	writeJSONResponse(w, c.status, rewritten)

	return true
}

// captureUnmodeled resolves the unmodeled properties to merge into this
// response and updates the overlay store. The overlay is only rewritten when
// the request actually carried a properties object — a lifecycle action
// (POST start/stop/restart) or any request without properties leaves the
// stored set intact, so it is never wiped by a subsequent bodiless call. A PUT
// replaces the set (full-replace semantics); a PATCH unions the freshly
// unmodeled keys over the stored ones, so a partial update keeps earlier
// preserved keys it did not resend.
func captureUnmodeled(
	r *http.Request, id string, reqProps, respProps map[string]any, overlay *propertyOverlay,
) map[string]any {
	stored := overlay.lookup(id)
	if len(reqProps) == 0 {
		return stored
	}

	fresh := missingProperties(reqProps, respProps)
	if r.Method == http.MethodPatch {
		fresh = mergeProperties(fresh, stored)
	}

	overlay.capture(id, fresh)

	return fresh
}

// writeOnlyProperty reports whether an ARM request-body property key is a
// write-only secret that real Azure accepts on write but never returns on a
// read. Such a key must never be captured by the overlay or echoed back:
// because the owning handler deliberately omits it from its response (mirroring
// Azure), the generic overlay would otherwise treat it as an unmodeled property
// and reflect the caller's secret on the create response and on every later GET
// — a credential leak. Matched case-insensitively at any nesting depth, since
// missingProperties descends into nested objects.
//
// A key is treated as write-only in two ways:
//
// 1. Suffix rule (the robust guard against wholly-unmodeled subtrees captured
// verbatim): any key whose lowercased name ENDS WITH "password" or "secret".
// This covers every write-only credential input real Azure accepts but never
// echoes, regardless of the prefix a handler's model happens not to know:
//   - administratorLoginPassword — Microsoft.Sql/servers (server/azure/sql),
//     DBforMySQL/DBforPostgreSQL flexibleServers (mysqlflex/postgresflex) and
//     Cosmos DB for PostgreSQL clusters (cosmospostgresql); each toARM* omits it.
//   - adminPassword — VM osProfile (virtualmachines); the osProfile model has no
//     password field, so it is dropped.
//   - initialCassandraAdminPassword — managed Cassandra (managedcassandra);
//     toARMCluster omits it.
//   - password — Cosmos-PG role (toARMRole drops it) and Container Instances
//     imageRegistryCredentials[].password (the array is unmodeled, captured
//     verbatim — hence the []any recursion in sanitizeUnmodeled).
//   - secret — AKS servicePrincipalProfile.secret (armManagedClusterProperties
//     omits the whole block).
//   - serverAppSecret / clientSecret — AKS aadProfile.serverAppSecret and any
//     clientSecret-style field in a verbatim-captured subtree (aadProfile is
//     unmodeled). The sibling serverAppID (public) does not end in the suffix,
//     so it still round-trips, matching real Azure's aadProfile read.
//
// The suffix is a true endsWith, so it never touches a field that merely
// contains the word: secretName, secretUri, secretRef, clientSecretSetting,
// passwordPolicy and the plural list outputs secrets/accessKeys are all
// preserved. Verified by sweeping every server/azure response/toARM* struct: no
// field a handler RETURNS has a json name ending in "password" or "secret". The
// sole password-ending returned field is the Container Instances exec action's
// `password`, a bare {webSocketUri, password} object with no id/properties — the
// overlay only rewrites ARM resources and only ever ADDS unmodeled keys onto a
// properties map (it never removes a handler's own fields), so that response is
// untouched. Output credential keys a client never sends on write
// (primaryKey/secondaryKey/accessKeys/connectionString) never enter the
// request-capture path and are correctly left alone.
//
// 2. Exact-match object keys: the Notification Hubs PNS credential blocks, which
// carry secrets but do not end in the suffixes. toHubJSON (notificationhubs)
// models only name/registrationTtl and drops these; real Azure serves them only
// via GetPnsCredentials, never the generic hub GET. Each is an object, so
// denylisting the key skips the whole credential subtree.
//
// The rule is matched case-insensitively at any nesting depth (missingProperties
// and sanitizeUnmodeled recurse into nested objects and arrays), and never
// suppresses a field a handler legitimately returns.
func writeOnlyProperty(key string) bool {
	lower := strings.ToLower(key)

	if strings.HasSuffix(lower, "password") || strings.HasSuffix(lower, "secret") {
		return true
	}

	switch lower {
	case "gcmcredential", "apnscredential", "wnscredential",
		"admcredential", "baiducredential", "mpnscredential":
		return true
	default:
		return false
	}
}

// sanitizeUnmodeled deep-copies a captured request value with every write-only
// secret key (writeOnlyProperty) removed at every depth. It guards the wholesale
// capture path in missingProperties: when the handler drops an entire object or
// array, it is preserved as-is, so a secret nested inside it would otherwise
// slip past the per-key denylist. Both objects (map[string]any) and arrays
// ([]any, e.g. imageRegistryCredentials[].password) are recursed and deep-copied
// so no request-owned map or slice is aliased into the overlay store; any other
// value passes through unchanged.
func sanitizeUnmodeled(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))

		for k, val := range t {
			if writeOnlyProperty(k) {
				continue
			}

			out[k] = sanitizeUnmodeled(val)
		}

		return out
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			out[i] = sanitizeUnmodeled(el)
		}

		return out
	default:
		return v
	}
}

// missingProperties returns the entries of req that resp does not already carry,
// descending into nested objects so an unmodeled leaf under a modeled parent is
// still captured. A key present in both as scalars is considered modeled (the
// handler owns it) and is omitted. Write-only secret keys (writeOnlyProperty)
// are skipped at every level so they are never captured, persisted, or echoed.
func missingProperties(req, resp map[string]any) map[string]any {
	if len(req) == 0 {
		return nil
	}

	out := map[string]any{}

	for k, reqVal := range req {
		if writeOnlyProperty(k) {
			continue
		}

		respVal, present := resp[k]
		if !present {
			// A wholly-unmodeled object is captured verbatim, so strip any
			// write-only secret nested inside it — the per-key skip above only
			// covers keys the loop visits directly, not those buried in a
			// subtree the handler dropped entirely (e.g. a VM osProfile the
			// response omits, carrying adminPassword).
			out[k] = sanitizeUnmodeled(reqVal)
			continue
		}

		reqChild, reqIsMap := reqVal.(map[string]any)
		respChild, respIsMap := respVal.(map[string]any)

		if reqIsMap && respIsMap {
			if nested := missingProperties(reqChild, respChild); len(nested) > 0 {
				out[k] = nested
			}
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// mergeProperties overlays the unmodeled properties onto resp without
// overwriting any key resp already carries (the handler/driver is authoritative
// for the properties it models). Nested objects are merged recursively.
func mergeProperties(resp, unmodeled map[string]any) map[string]any {
	// Size the map to resp and let it grow for the unmodeled keys — summing the
	// two lengths as a capacity hint is a pointless overflow risk for no gain.
	out := make(map[string]any, len(resp))
	for k, v := range resp {
		out[k] = v
	}

	for k, addVal := range unmodeled {
		existing, present := out[k]
		if !present {
			out[k] = addVal
			continue
		}

		existingChild, existingIsMap := existing.(map[string]any)
		addChild, addIsMap := addVal.(map[string]any)

		if existingIsMap && addIsMap {
			out[k] = mergeProperties(existingChild, addChild)
		}
	}

	return out
}
