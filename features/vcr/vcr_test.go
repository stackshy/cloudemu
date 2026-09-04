package vcr_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/vcr"
)

// echoHandler counts how often it is invoked and writes a body derived from the
// request, so a test can tell a live response from a replayed one.
type echoHandler struct{ calls int }

func (h *echoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls++

	body, _ := io.ReadAll(r.Body)

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-Echo", "live")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte("live:" + r.Method + ":" + r.URL.Path + ":" + string(body)))
}

func doRequest(t *testing.T, h http.Handler, method, target, body string) *http.Response {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, target, rdr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Result()
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return string(b)
}

// TestRecordThenReplay is the core round-trip: record a session to a cassette,
// then replay it against a DIFFERENT backend and prove the recorded responses
// come back verbatim without the replay backend ever being called.
func TestRecordThenReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cassette.json")
	clock := config.NewFakeClock(time.Unix(1700000000, 0))

	rec, err := vcr.New(vcr.Options{Mode: vcr.ModeRecord, CassettePath: path, Clock: clock})
	if err != nil {
		t.Fatalf("New record: %v", err)
	}

	recBackend := &echoHandler{}
	recorded := rec.Wrap(recBackend, "aws")

	// A GET and a POST with a body.
	got := readBody(t, doRequest(t, recorded, http.MethodGet, "/things", ""))
	if got != "live:GET:/things:" {
		t.Fatalf("record GET body = %q", got)
	}

	got = readBody(t, doRequest(t, recorded, http.MethodPost, "/things?x=1", "payload"))
	if got != "live:POST:/things:payload" {
		t.Fatalf("record POST body = %q", got)
	}

	if recBackend.calls != 2 {
		t.Fatalf("record backend calls = %d, want 2", recBackend.calls)
	}

	if err := rec.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Fresh replay engine loading the saved cassette, wrapping a backend that must
	// NEVER be called.
	play, err := vcr.New(vcr.Options{Mode: vcr.ModeReplay, CassettePath: path, Strict: true})
	if err != nil {
		t.Fatalf("New replay: %v", err)
	}

	replayBackend := &echoHandler{}
	replayed := play.Wrap(replayBackend, "aws")

	resp := doRequest(t, replayed, http.MethodGet, "/things", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("replay GET status = %d, want 201", resp.StatusCode)
	}

	if body := readBody(t, resp); body != "live:GET:/things:" {
		t.Fatalf("replay GET body = %q, want recorded", body)
	}

	if resp.Header.Get("X-Echo") != "live" {
		t.Fatalf("replay GET must return recorded headers, got %q", resp.Header.Get("X-Echo"))
	}

	if body := readBody(t, doRequest(t, replayed, http.MethodPost, "/things?x=1", "payload")); body != "live:POST:/things:payload" {
		t.Fatalf("replay POST body = %q", body)
	}

	if replayBackend.calls != 0 {
		t.Fatalf("replay backend calls = %d, want 0 (replay must not touch the backend)", replayBackend.calls)
	}
}

// TestReplayStrictNoMatch: an unmatched request 501s in strict mode and never
// reaches the backend.
func TestReplayStrictNoMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.json")
	writeEmptyCassette(t, path)

	play, err := vcr.New(vcr.Options{Mode: vcr.ModeReplay, CassettePath: path, Strict: true})
	if err != nil {
		t.Fatalf("New replay: %v", err)
	}

	backend := &echoHandler{}
	resp := doRequest(t, play.Wrap(backend, "aws"), http.MethodGet, "/missing", "")

	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("strict no-match status = %d, want 501", resp.StatusCode)
	}

	if resp.Header.Get("X-Cloudemu-Vcr") != "no-match" {
		t.Fatalf("missing no-match marker header")
	}

	if backend.calls != 0 {
		t.Fatalf("strict no-match must not call backend, calls = %d", backend.calls)
	}
}

// TestReplayPassthrough: a miss falls through to the backend when strict is off.
func TestReplayPassthrough(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.json")
	writeEmptyCassette(t, path)

	play, err := vcr.New(vcr.Options{Mode: vcr.ModeReplay, CassettePath: path, Strict: false})
	if err != nil {
		t.Fatalf("New replay: %v", err)
	}

	backend := &echoHandler{}
	resp := doRequest(t, play.Wrap(backend, "aws"), http.MethodGet, "/live-path", "")

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("passthrough status = %d, want 201 (backend served)", resp.StatusCode)
	}

	if backend.calls != 1 {
		t.Fatalf("passthrough must call backend once, calls = %d", backend.calls)
	}
}

// TestBodyHashDistinguishes: same method+path, different bodies must not match.
func TestBodyHashDistinguishes(t *testing.T) {
	path := recordOne(t, http.MethodPost, "/put", "first")

	play, err := vcr.New(vcr.Options{Mode: vcr.ModeReplay, CassettePath: path, Strict: true})
	if err != nil {
		t.Fatalf("New replay: %v", err)
	}

	backend := &echoHandler{}
	wrapped := play.Wrap(backend, "aws")

	// Same body → match.
	if resp := doRequest(t, wrapped, http.MethodPost, "/put", "first"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("same body should match, status = %d", resp.StatusCode)
	}

	// Different body → no match (501).
	if resp := doRequest(t, wrapped, http.MethodPost, "/put", "second"); resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("different body must not match, status = %d, want 501", resp.StatusCode)
	}
}

// TestQueryCanonicalization: query params in a different order still match.
func TestQueryCanonicalization(t *testing.T) {
	path := recordOne(t, http.MethodGet, "/q?b=2&a=1", "")

	play, err := vcr.New(vcr.Options{Mode: vcr.ModeReplay, CassettePath: path, Strict: true})
	if err != nil {
		t.Fatalf("New replay: %v", err)
	}

	resp := doRequest(t, play.Wrap(&echoHandler{}, "aws"), http.MethodGet, "/q?a=1&b=2", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("reordered query should match, status = %d", resp.StatusCode)
	}
}

// TestProviderDisambiguation: the same path under a different provider is a
// distinct interaction, so a shared cassette across providers is unambiguous.
func TestProviderDisambiguation(t *testing.T) {
	path := recordOne(t, http.MethodGet, "/shared", "")

	play, err := vcr.New(vcr.Options{Mode: vcr.ModeReplay, CassettePath: path, Strict: true})
	if err != nil {
		t.Fatalf("New replay: %v", err)
	}

	// Recorded under "aws" → matches for aws, misses for gcp.
	if resp := doRequest(t, play.Wrap(&echoHandler{}, "aws"), http.MethodGet, "/shared", ""); resp.StatusCode != http.StatusCreated {
		t.Fatalf("aws should match, status = %d", resp.StatusCode)
	}

	if resp := doRequest(t, play.Wrap(&echoHandler{}, "gcp"), http.MethodGet, "/shared", ""); resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("gcp must not match an aws recording, status = %d, want 501", resp.StatusCode)
	}
}

// TestReplayMissingCassetteFails: replay against a non-existent cassette is a
// hard error, not a silent empty replay.
func TestReplayMissingCassetteFails(t *testing.T) {
	_, err := vcr.New(vcr.Options{Mode: vcr.ModeReplay, CassettePath: filepath.Join(t.TempDir(), "nope.json"), Strict: true})
	if err == nil {
		t.Fatal("replay against a missing cassette should error")
	}
}

// TestNewRejectsBadInput covers mode/path validation.
func TestNewRejectsBadInput(t *testing.T) {
	if _, err := vcr.New(vcr.Options{Mode: "bogus", CassettePath: "x.json"}); err == nil {
		t.Fatal("bad mode should error")
	}

	if _, err := vcr.New(vcr.Options{Mode: vcr.ModeRecord, CassettePath: ""}); err == nil {
		t.Fatal("empty cassette path should error")
	}
}

// recordOne records a single request against an echo backend and returns the
// saved cassette path.
func recordOne(t *testing.T, method, target, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cassette.json")

	rec, err := vcr.New(vcr.Options{Mode: vcr.ModeRecord, CassettePath: path, Clock: config.NewFakeClock(time.Unix(1700000000, 0))})
	if err != nil {
		t.Fatalf("New record: %v", err)
	}

	doRequest(t, rec.Wrap(&echoHandler{}, "aws"), method, target, body)

	if err := rec.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	return path
}

// writeEmptyCassette records nothing and flushes, yielding a valid empty cassette.
func writeEmptyCassette(t *testing.T, path string) {
	t.Helper()

	rec, err := vcr.New(vcr.Options{Mode: vcr.ModeRecord, CassettePath: path, Clock: config.NewFakeClock(time.Unix(1700000000, 0))})
	if err != nil {
		t.Fatalf("New record: %v", err)
	}

	if err := rec.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}
