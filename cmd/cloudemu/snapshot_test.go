//go:build unix

package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestValidSnapshotName(t *testing.T) {
	ok := []string{"baseline", "v1", "with-users", "a.b_c-1", strings.Repeat("x", 64)}
	for _, n := range ok {
		if !validSnapshotName(n) {
			t.Errorf("validSnapshotName(%q) = false, want true", n)
		}
	}

	bad := []string{"", ".", "..", "a/b", "../etc", "has space", strings.Repeat("x", 65), "a*b"}
	for _, n := range bad {
		if validSnapshotName(n) {
			t.Errorf("validSnapshotName(%q) = true, want false", n)
		}
	}
}

func TestParseSnapshotFlags(t *testing.T) {
	home, force, pos := parseSnapshotFlags([]string{"v1", "--home", "/tmp/h", "--force"})
	if home != "/tmp/h" || !force || len(pos) != 1 || pos[0] != "v1" {
		t.Fatalf("parseSnapshotFlags = %q %v %v", home, force, pos)
	}

	home, force, pos = parseSnapshotFlags([]string{"v2"})
	if home != "" || force || len(pos) != 1 || pos[0] != "v2" {
		t.Fatalf("parseSnapshotFlags(bare) = %q %v %v", home, force, pos)
	}
}

func TestSnapshotSaveListLoadDelete(t *testing.T) {
	var posted string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"schemaVersion":1,"providers":{"aws":{}}}`))

			return
		}

		b, _ := io.ReadAll(r.Body)
		posted = string(b)
		_, _ = w.Write([]byte(`{"status":"restored"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(endpointsPath(dir), []byte(`{"aws":"`+srv.URL+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// save writes the snapshot file
	if err := snapshotSave(dir, "s1", false); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(snapshotFilePath(dir, "s1")); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}

	// a second save without --force is refused
	if err := snapshotSave(dir, "s1", false); !errors.Is(err, errSnapExists) {
		t.Fatalf("save(exists) = %v, want errSnapExists", err)
	}

	// load posts the saved file back to the server
	if err := snapshotLoad(dir, "s1"); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.Contains(posted, `"schemaVersion"`) || !strings.Contains(posted, `"name": "s1"`) {
		t.Fatalf("posted snapshot missing schema/meta: %s", posted)
	}

	// loading a missing snapshot is a clear error
	if err := snapshotLoad(dir, "nope"); !errors.Is(err, errSnapNotFound) {
		t.Fatalf("load(missing) = %v, want errSnapNotFound", err)
	}

	// delete removes it; a second delete reports not found
	if err := snapshotDelete(dir, "s1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := snapshotDelete(dir, "s1"); !errors.Is(err, errSnapNotFound) {
		t.Fatalf("delete(again) = %v, want errSnapNotFound", err)
	}
}

func TestSnapshotSaveDaemonDown(t *testing.T) {
	dir := t.TempDir() // no endpoints file → daemon considered down
	if err := snapshotSave(dir, "x", false); !errors.Is(err, errSnapDaemonDown) {
		t.Fatalf("save(no daemon) = %v, want errSnapDaemonDown", err)
	}
}
