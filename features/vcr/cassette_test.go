package vcr_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/features/vcr"
)

// TestCassetteSaveLoadRoundTrip proves a saved cassette reloads identically and
// matches by request identity, including a binary (non-UTF8) response body.
func TestCassetteSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")

	binary := []byte{0x00, 0x01, 0xff, 0xfe, 'x'}
	c := &vcr.Cassette{RecordedAt: time.Unix(1700000000, 0)}
	c.Add(&vcr.Interaction{
		Request:  vcr.Request{Provider: "aws", Method: http.MethodGet, Path: "/o", Query: "a=1", BodyHash: ""},
		Response: vcr.Response{Status: 200, Headers: http.Header{"Content-Type": {"application/octet-stream"}}, Body: binary},
	})

	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := vcr.LoadCassette(path)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	if len(got.Interactions) != 1 {
		t.Fatalf("interactions = %d, want 1", len(got.Interactions))
	}

	inter, ok := got.Match(&vcr.Request{Provider: "aws", Method: http.MethodGet, Path: "/o", Query: "a=1"})
	if !ok {
		t.Fatal("Match failed after round-trip")
	}

	if string(inter.Response.Body) != string(binary) {
		t.Fatalf("body corrupted across round-trip: %v", inter.Response.Body)
	}

	if _, ok := got.Match(&vcr.Request{Provider: "aws", Method: http.MethodGet, Path: "/nope"}); ok {
		t.Fatal("Match should fail for an absent request")
	}
}

// TestLoadCassetteErrors covers the missing-file and malformed-JSON paths.
func TestLoadCassetteErrors(t *testing.T) {
	if _, err := vcr.LoadCassette(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("loading a missing cassette should error")
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := vcr.LoadCassette(bad); err == nil {
		t.Fatal("loading malformed JSON should error")
	}
}
