package serverkit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stackshy/cloudemu/v2/persist"
)

// TestRestoreStateIgnoresUnreadableFile covers the fail-open half: a corrupt or
// missing snapshot must not wedge startup — restoreState warns and starts empty
// rather than returning an error that aborts New.
func TestRestoreStateIgnoresUnreadableFile(t *testing.T) {
	dir := t.TempDir()

	corrupt := filepath.Join(dir, "snapshot.json")
	if err := os.WriteFile(corrupt, []byte("{ not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := restoreState(context.Background(), corrupt, map[string]persist.Services{}); err != nil {
		t.Fatalf("restoreState(corrupt) = %v, want nil (start empty)", err)
	}

	missing := filepath.Join(dir, "does-not-exist.json")
	if err := restoreState(context.Background(), missing, map[string]persist.Services{}); err != nil {
		t.Fatalf("restoreState(missing) = %v, want nil", err)
	}
}
