//go:build unix

package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

func TestHTTPHealthURLAndHostPort(t *testing.T) {
	if got := httpHealthURL(map[string]string{"aws": "http://127.0.0.1:4566"}); got != "http://127.0.0.1:4566/_cloudemu/health" {
		t.Fatalf("httpHealthURL(aws) = %q", got)
	}
	if got := httpHealthURL(map[string]string{"azure": "https://127.0.0.1:4568"}); got != "" {
		t.Fatalf("httpHealthURL(azure-only) = %q, want empty", got)
	}

	hp, err := hostPortOf("https://127.0.0.1:4568")
	if err != nil || hp != "127.0.0.1:4568" {
		t.Fatalf("hostPortOf = %q, %v", hp, err)
	}
}

func TestReadEndpointsPrunesEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eps.json")
	if err := os.WriteFile(path, []byte(`{"aws":"http://x:1","azure":"","gcp":"http://y:2"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	eps, err := readEndpoints(path)
	if err != nil {
		t.Fatalf("readEndpoints: %v", err)
	}
	if len(eps) != 2 || eps["aws"] == "" || eps["gcp"] == "" {
		t.Fatalf("readEndpoints = %v, want aws+gcp only", eps)
	}
}

func TestWaitForEndpointsTimeout(t *testing.T) {
	if _, err := waitForEndpoints(filepath.Join(t.TempDir(), "never.json"), 200*time.Millisecond); err == nil {
		t.Fatal("waitForEndpoints(absent) = nil, want timeout")
	}
}

func TestWaitServerReady(t *testing.T) {
	// HTTP branch: a healthy AWS endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	if err := waitServerReady(map[string]string{"aws": srv.URL}, time.Second); err != nil {
		t.Fatalf("waitServerReady(http) = %v", err)
	}

	// TCP branch: a bare listener stands in for the self-signed HTTPS endpoint.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := waitServerReady(map[string]string{"azure": "https://" + ln.Addr().String()}, time.Second); err != nil {
		t.Fatalf("waitServerReady(tcp) = %v", err)
	}

	// Empty set is an error.
	if err := waitServerReady(map[string]string{}, time.Second); err == nil {
		t.Fatal("waitServerReady(empty) = nil, want errNoEndpoints")
	}
}

func TestPollTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := pollTCP(ln.Addr().String(), time.Second); err != nil {
		t.Fatalf("pollTCP(open) = %v", err)
	}
	if err := pollTCP("127.0.0.1:1", 200*time.Millisecond); err == nil {
		t.Fatal("pollTCP(dead) = nil, want timeout")
	}
}

func TestWaitExit(t *testing.T) {
	// A never-alive pid returns immediately.
	if err := waitExit(-1, time.Second); err != nil {
		t.Fatalf("waitExit(dead) = %v", err)
	}
	// The test process itself is alive → times out.
	if err := waitExit(os.Getpid(), 200*time.Millisecond); err == nil {
		t.Fatal("waitExit(self) = nil, want timeout")
	}
}

func TestKillChildReaps(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	if !processAlive(cmd.Process.Pid) {
		t.Fatal("child not alive after start")
	}

	killChild(cmd) // SIGTERM + reap (Wait), no zombie left behind

	if processAlive(cmd.Process.Pid) {
		t.Fatal("child still alive after killChild")
	}
}

func TestRunStopStaleState(t *testing.T) {
	dir := t.TempDir()
	// A state whose pid is not alive → runStop cleans it up, no signal sent.
	if err := writeState(dir, daemonState{PID: -1, Endpoints: map[string]string{"aws": "http://127.0.0.1:1"}}); err != nil {
		t.Fatal(err)
	}
	if err := runStop([]string{"--home", dir}); err != nil {
		t.Fatalf("runStop(stale) = %v", err)
	}
	if _, err := readState(dir); err == nil {
		t.Fatal("stale state.json should have been removed")
	}
}

func TestRunStartAlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()

	// State pointing at ourselves (alive) with a reachable endpoint → runStart
	// must short-circuit without spawning a new server.
	if err := writeState(dir, daemonState{PID: os.Getpid(), Endpoints: map[string]string{"aws": srv.URL}}); err != nil {
		t.Fatal(err)
	}
	if err := runStart([]string{"--home", dir}); err != nil {
		t.Fatalf("runStart(already-running) = %v", err)
	}
	s, err := readState(dir)
	if err != nil || s.PID != os.Getpid() {
		t.Fatalf("runStart overwrote state: %+v (%v)", s, err)
	}
}

func TestRunLifecycleUnknown(t *testing.T) {
	if err := runLifecycle("bogus", nil); err == nil {
		t.Fatal("runLifecycle(bogus) = nil, want error")
	}
}
