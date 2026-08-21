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
	o.mu.RLock()
	defer o.mu.RUnlock()

	return o.store[id]
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

		if !rec.rewrite(w, r, reqProps, overlay) {
			rec.flush(w)
		}
	})
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
// flush the original bytes.
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

	id, ok := resource["id"].(string)
	if !ok || id == "" {
		return false
	}

	respProps, _ := resource["properties"].(map[string]any)

	// On a write that carried a body, the unmodeled set is exactly the request
	// properties the response did not echo back; record it so later reads can
	// replay it. Reads carry no request body, so they fall back to the store.
	unmodeled := overlay.lookup(id)
	if hasBody(r) {
		unmodeled = missingProperties(reqProps, respProps)
		overlay.capture(id, unmodeled)
	}

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

func hasBody(r *http.Request) bool {
	return r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodPost
}

// missingProperties returns the entries of req that resp does not already carry,
// descending into nested objects so an unmodeled leaf under a modeled parent is
// still captured. A key present in both as scalars is considered modeled (the
// handler owns it) and is omitted.
func missingProperties(req, resp map[string]any) map[string]any {
	if len(req) == 0 {
		return nil
	}

	out := map[string]any{}

	for k, reqVal := range req {
		respVal, present := resp[k]
		if !present {
			out[k] = reqVal
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
