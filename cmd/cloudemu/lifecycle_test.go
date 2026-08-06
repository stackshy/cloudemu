package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunDir(t *testing.T) {
	// Explicit --home wins.
	got, err := runDir("/tmp/custom-home")
	if err != nil || got != "/tmp/custom-home" {
		t.Fatalf("runDir(explicit) = %q, %v", got, err)
	}

	// Empty falls back to ~/.cloudemu.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}

	got, err = runDir("")
	if err != nil || got != filepath.Join(home, ".cloudemu") {
		t.Fatalf("runDir(default) = %q, %v; want %q", got, err, filepath.Join(home, ".cloudemu"))
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()

	want := daemonState{
		PID:       4321,
		Endpoints: map[string]string{"aws": "http://127.0.0.1:4566"},
		StartedAt: "2026-08-06T00:00:00Z",
		Args:      []string{"--region", "eu-west-1"},
	}
	if err := writeState(dir, want); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	got, err := readState(dir)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if got.PID != want.PID || got.Endpoints["aws"] != want.Endpoints["aws"] ||
		got.StartedAt != want.StartedAt || len(got.Args) != 2 {
		t.Fatalf("readState = %+v, want %+v", got, want)
	}

	if err := removeState(dir); err != nil {
		t.Fatalf("removeState: %v", err)
	}
	if _, err := readState(dir); err == nil {
		t.Fatal("readState after remove: expected error, got nil")
	}
}

func TestReadStateMissing(t *testing.T) {
	if _, err := readState(t.TempDir()); err == nil {
		t.Fatal("expected error reading state from empty dir")
	}
}

func TestProcessAlive(t *testing.T) {
	// The test process itself is alive.
	if !processAlive(os.Getpid()) {
		t.Fatal("processAlive(self) = false, want true")
	}

	// PID 0 / a very unlikely PID is not a live cloudemu process.
	if processAlive(-1) {
		t.Fatal("processAlive(-1) = true, want false")
	}
}

func TestPollHealthReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := pollHealth(srv.URL, time.Second); err != nil {
		t.Fatalf("pollHealth(ready) = %v", err)
	}
}

func TestPollHealthTimeout(t *testing.T) {
	// Nothing listening on this address → should time out, not hang.
	if err := pollHealth("http://127.0.0.1:1", 200*time.Millisecond); err == nil {
		t.Fatal("pollHealth(dead) = nil, want timeout error")
	}
}

func TestSplitHomeFlag(t *testing.T) {
	// --home is extracted; the rest passes through to serve untouched.
	home, rest := splitHomeFlag([]string{"--home", "/tmp/h", "--region", "eu-west-1", "--aws-port", "4600"})
	if home != "/tmp/h" {
		t.Fatalf("home = %q, want /tmp/h", home)
	}
	if len(rest) != 4 || rest[0] != "--region" || rest[2] != "--aws-port" {
		t.Fatalf("rest = %v, want [--region eu-west-1 --aws-port 4600]", rest)
	}

	// --home=value form.
	home, rest = splitHomeFlag([]string{"--home=/tmp/h2", "--quiet"})
	if home != "/tmp/h2" || len(rest) != 1 || rest[0] != "--quiet" {
		t.Fatalf("splitHomeFlag(=form) home=%q rest=%v", home, rest)
	}

	// Absent → empty home, all args pass through.
	home, rest = splitHomeFlag([]string{"--region", "us-east-1"})
	if home != "" || len(rest) != 2 {
		t.Fatalf("splitHomeFlag(absent) home=%q rest=%v", home, rest)
	}
}
