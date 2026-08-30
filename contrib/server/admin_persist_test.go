package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
)

// TestAdminReset boots the batteries server with the admin control plane on,
// seeds a bucket through /_cloudemu/seed, confirms it shows up in the snapshot,
// then POSTs /_cloudemu/reset and confirms the state is wiped — proving the
// --admin flag threads through to serverkit's control plane.
func TestAdminReset(t *testing.T) {
	cfg := testConfig(t, allEnginesOff())
	cfg.Admin = true

	awsURL, stop := startAWS(t, cfg, mustOptions(t, &cfg))
	defer stop()

	seedBucket(t, awsURL, "reset-me")

	if snap := getSnapshot(t, awsURL); !strings.Contains(snap, "reset-me") {
		t.Fatalf("seeded bucket missing from snapshot before reset")
	}

	if code := post(t, awsURL+"/_cloudemu/reset", nil); code != http.StatusOK {
		t.Fatalf("reset status = %d, want 200", code)
	}

	if snap := getSnapshot(t, awsURL); strings.Contains(snap, "reset-me") {
		t.Fatalf("bucket still present after reset — state not cleared")
	}
}

// TestPersistRoundTrip creates a resource, shuts the server down so serverkit
// writes the persistence snapshot, then boots a fresh app pointed at the same
// state file and confirms the resource is restored on boot — proving --persist
// and --state-file thread through.
func TestPersistRoundTrip(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")

	cfg := testConfig(t, allEnginesOff())
	cfg.Admin = true
	cfg.Persist = true
	cfg.StateFile = stateFile

	awsURL, stop := startAWS(t, cfg, mustOptions(t, &cfg))
	seedBucket(t, awsURL, "persist-me")
	stop() // shutdown writes the snapshot to stateFile

	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state file not written on shutdown: %v", err)
	}

	// A fresh app on the same state file (new ports) must restore the bucket.
	cfg2 := testConfig(t, allEnginesOff())
	cfg2.Admin = true
	cfg2.Persist = true
	cfg2.StateFile = stateFile

	awsURL2, stop2 := startAWS(t, cfg2, mustOptions(t, &cfg2))
	defer stop2()

	if snap := getSnapshot(t, awsURL2); !strings.Contains(snap, "persist-me") {
		t.Fatalf("bucket not restored on boot from %s", stateFile)
	}
}

// TestInitDir writes a fixture into an init dir and confirms it is seeded on
// boot — proving --init-dir threads through to serverkit.
func TestInitDir(t *testing.T) {
	dir := t.TempDir()
	fixture := `{"buckets":[{"name":"seeded-bucket"}]}`

	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg := testConfig(t, allEnginesOff())
	cfg.Admin = true
	cfg.InitDir = dir

	awsURL, stop := startAWS(t, cfg, mustOptions(t, &cfg))
	defer stop()

	if snap := getSnapshot(t, awsURL); !strings.Contains(snap, "seeded-bucket") {
		t.Fatalf("init-dir fixture not seeded on boot")
	}
}

// mustOptions builds the engine options for a config or fails the test. The
// identity options are added by ToServerkitConfig inside newAppFromOptions.
func mustOptions(t *testing.T, cfg *appConfig) []config.Option {
	t.Helper()

	opts, _, err := buildOptions(cfg, dockerAvailable)
	if err != nil {
		t.Fatalf("buildOptions: %v", err)
	}

	return opts
}

// seedBucket seeds one empty bucket through the admin /_cloudemu/seed endpoint.
func seedBucket(t *testing.T, baseURL, name string) {
	t.Helper()

	body := strings.NewReader(`{"buckets":[{"name":"` + name + `"}]}`)
	if code := post(t, baseURL+"/_cloudemu/seed", body); code != http.StatusOK {
		t.Fatalf("seed status = %d, want 200", code)
	}
}

// getSnapshot returns the admin /_cloudemu/snapshot body as a string.
func getSnapshot(t *testing.T, baseURL string) string {
	t.Helper()

	resp, err := http.Get(baseURL + "/_cloudemu/snapshot") //nolint:noctx // short-lived in-process test call
	if err != nil {
		t.Fatalf("GET snapshot: %v", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read snapshot body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200", resp.StatusCode)
	}

	return string(b)
}

// post issues a POST to url and returns the status code, failing on transport
// error.
func post(t *testing.T, url string, body io.Reader) int {
	t.Helper()

	resp, err := http.Post(url, "application/json", body) //nolint:noctx // short-lived in-process test call
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode
}
