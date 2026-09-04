package vcr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/stackshy/cloudemu/v2/config"
)

// Mode selects record or replay behavior.
type Mode string

const (
	// ModeRecord captures every inbound request/response into the cassette.
	ModeRecord Mode = "record"
	// ModeReplay serves recorded responses and never calls the real handlers.
	ModeReplay Mode = "replay"
)

// errUnknownMode is returned when Options.Mode is not record or replay.
var errUnknownMode = errors.New("vcr: mode must be \"record\" or \"replay\"")

// errCassettePathRequired is returned when Options.CassettePath is empty.
var errCassettePathRequired = errors.New("vcr: cassette path is required")

// ParseMode validates a mode string (e.g. from a CLI flag). Empty is rejected;
// callers that treat empty as "VCR off" should check that before calling.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModeRecord:
		return ModeRecord, nil
	case ModeReplay:
		return ModeReplay, nil
	default:
		return "", fmt.Errorf("%w (got %q)", errUnknownMode, s)
	}
}

// Options configures a VCR.
type Options struct {
	Mode         Mode         // record or replay
	CassettePath string       // file the cassette is loaded from (replay) / saved to (record)
	Strict       bool         // replay only: on no match, 501 instead of falling through to the real handler
	Clock        config.Clock // timestamp source for a new cassette (default RealClock)
}

// VCR is the record/replay engine shared across every wrapped handler. One VCR
// owns one cassette; Wrap tags each handler with its provider so a shared
// cassette disambiguates identical paths across providers.
type VCR struct {
	mode     Mode
	strict   bool
	path     string
	cassette *Cassette
}

// New builds a VCR. In replay mode the cassette is loaded from CassettePath now
// (a missing/invalid file is a hard error — replay against nothing is a bug). In
// record mode a fresh cassette is created and stamped with Clock.Now(); it is
// persisted by Flush.
func New(opts Options) (*VCR, error) {
	mode, err := ParseMode(string(opts.Mode))
	if err != nil {
		return nil, err
	}

	if opts.CassettePath == "" {
		return nil, errCassettePathRequired
	}

	v := &VCR{mode: mode, strict: opts.Strict, path: opts.CassettePath}

	switch mode {
	case ModeReplay:
		c, err := LoadCassette(opts.CassettePath)
		if err != nil {
			return nil, fmt.Errorf("vcr: load cassette: %w", err)
		}

		v.cassette = c
	case ModeRecord:
		clock := opts.Clock
		if clock == nil {
			clock = config.RealClock{}
		}

		v.cassette = &Cassette{RecordedAt: clock.Now()}
	}

	return v, nil
}

// Wrap decorates next with VCR behavior for the given provider. In record mode
// requests flow through next and the response is captured; in replay mode a
// matching request is served from the cassette and next is never called.
func (v *VCR) Wrap(next http.Handler, provider string) http.Handler {
	if v.mode == ModeReplay {
		return v.replay(next, provider)
	}

	return v.record(next, provider)
}

// Flush persists the cassette in record mode; a no-op in replay mode.
func (v *VCR) Flush() error {
	if v.mode != ModeRecord {
		return nil
	}

	return v.cassette.Save(v.path)
}

// record passes the request through, capturing the response into the cassette.
func (v *VCR) record(next http.Handler, provider string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := drainBody(r)

		cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(cw, r)

		v.cassette.Add(&Interaction{
			Request: requestKey(provider, r.Method, r.URL, body),
			Response: Response{
				Status:  cw.status,
				Headers: recordableHeaders(cw.Header()),
				Body:    cw.body.Bytes(),
			},
		})
	})
}

// replay serves a matching recorded response and never calls next. On no match
// it 501s in strict mode, or falls through to next in passthrough mode.
func (v *VCR) replay(next http.Handler, provider string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := drainBody(r)
		key := requestKey(provider, r.Method, r.URL, body)

		inter, ok := v.cassette.Match(&key)
		if !ok {
			if v.strict {
				writeNoMatch(w, &key)

				return
			}

			next.ServeHTTP(w, r)

			return
		}

		writeRecorded(w, inter.Response)
	})
}

// drainBody reads the request body fully and restores it so the wrapped handler
// (record mode) can read it again. Returns nil for a bodyless request.
func drainBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}

	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()

	if err != nil {
		body = nil
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	return body
}

// writeRecorded writes a recorded response verbatim. Content-Length is left to
// the transport (it was dropped at record time), so it always matches the body.
func writeRecorded(w http.ResponseWriter, resp Response) {
	for k, vals := range resp.Headers {
		for _, val := range vals {
			w.Header().Add(k, val)
		}
	}

	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}

	w.WriteHeader(status)
	_, _ = w.Write(resp.Body)
}

// writeNoMatch reports a strict-mode replay miss as 501 with a diagnostic body,
// so a client sees a clear failure rather than a silent fallthrough.
func writeNoMatch(w http.ResponseWriter, key *Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Cloudemu-Vcr", "no-match")
	w.WriteHeader(http.StatusNotImplemented)
	// The reflected request line is a local-dev diagnostic served as text/plain
	// (never HTML), so echoing the path/query cannot execute in a browser.
	//nolint:gosec // G705: text/plain diagnostic for a local emulator, not HTML
	fmt.Fprintf(w, "cloudemu vcr replay: no cassette match for %s %s?%s (provider %s)\n",
		key.Method, key.Path, key.Query, key.Provider)
}

// captureWriter records the status and body while forwarding to the real writer.
type captureWriter struct {
	http.ResponseWriter

	status      int
	body        bytes.Buffer
	wroteHeader bool
}

func (c *captureWriter) WriteHeader(code int) {
	if !c.wroteHeader {
		c.status = code
		c.wroteHeader = true
	}

	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	c.body.Write(b)

	return c.ResponseWriter.Write(b)
}
