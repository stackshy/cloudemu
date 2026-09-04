// Package vcr provides HTTP record/replay ("VCR") middleware for the cloudemu
// wire server. In RECORD mode it passes each inbound request through to the real
// handlers and captures the response into a cassette (a persisted JSON file). In
// REPLAY mode it matches an inbound request against a loaded cassette and returns
// the recorded response WITHOUT invoking the real handlers, so a session can be
// snapshotted once and replayed deterministically offline.
//
// Matching is deterministic on method + path + canonical query + a hash of the
// request body (see Request). Fuzzy/normalized matching, response-body secret
// redaction beyond dropping volatile/hop-by-hop headers, and sequence/ordering
// replay modes are intentionally out of scope — follow-ups.
package vcr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

// Request is the deterministic identity of an inbound HTTP request. All fields
// are strings so the struct is comparable and can be matched by equality. Query
// is canonicalized (keys and values sorted) so semantically equal query strings
// match regardless of ordering, and BodyHash is a hex SHA-256 of the raw request
// body (empty when there is no body).
type Request struct {
	Provider string `json:"provider"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Query    string `json:"query"`
	BodyHash string `json:"body_hash"`
}

// Response is the captured response the real handlers produced. Body is stored
// verbatim (JSON base64-encodes the bytes) so binary payloads replay exactly.
// Headers carries every response header except the volatile/hop-by-hop ones the
// transport recomputes (see recordableHeaders).
type Response struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers,omitempty"`
	Body    []byte      `json:"body,omitempty"`
}

// Interaction is one recorded request/response pair.
type Interaction struct {
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}

// cassetteFileMode is the permission a saved cassette is written with. Responses
// may carry sensitive bodies, so keep it owner-only.
const cassetteFileMode = 0o600

// Cassette is an ordered, thread-safe list of interactions with JSON save/load.
type Cassette struct {
	mu sync.Mutex

	RecordedAt   time.Time     `json:"recorded_at"`
	Interactions []Interaction `json:"interactions"`
}

// LoadCassette reads and parses a cassette from path.
func LoadCassette(path string) (*Cassette, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var c Cassette
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}

	return &c, nil
}

// Save writes the cassette to path as indented JSON (0o600, since responses may
// contain sensitive bodies).
func (c *Cassette) Save(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, cassetteFileMode)
}

// Add appends an interaction. Safe for concurrent callers.
func (c *Cassette) Add(i *Interaction) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Interactions = append(c.Interactions, *i)
}

// Match returns the first interaction whose request equals req. Deterministic
// (first-match-wins), not sequence-aware.
func (c *Cassette) Match(req *Request) (Interaction, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, i := range c.Interactions {
		if i.Request == *req {
			return i, true
		}
	}

	return Interaction{}, false
}

// requestKey builds the deterministic Request identity for an inbound request.
func requestKey(provider, method string, u *url.URL, body []byte) Request {
	return Request{
		Provider: provider,
		Method:   method,
		Path:     u.Path,
		Query:    u.Query().Encode(), // Encode sorts keys and values
		BodyHash: bodyHash(body),
	}
}

// bodyHash returns the hex SHA-256 of body, or "" when there is no body.
func bodyHash(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:])
}

// volatileHeaders are response headers the transport recomputes or that change
// per-response; recording them would break replay (Content-Length) or add noise
// (Date). This minimal drop-list is the only header filtering VCR does — broader
// redaction is a follow-up.
//
//nolint:gochecknoglobals // fixed lookup table, read-only
var volatileHeaders = map[string]bool{
	"Content-Length":    true,
	"Date":              true,
	"Connection":        true,
	"Transfer-Encoding": true,
	"Keep-Alive":        true,
}

// recordableHeaders copies src, dropping the volatile/hop-by-hop headers. Returns
// nil when nothing survives, so an empty map is omitted from the JSON.
func recordableHeaders(src http.Header) http.Header {
	var out http.Header

	for k, vals := range src {
		if volatileHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}

		if out == nil {
			out = make(http.Header, len(src))
		}

		cp := make([]string, len(vals))
		copy(cp, vals)
		out[k] = cp
	}

	return out
}
