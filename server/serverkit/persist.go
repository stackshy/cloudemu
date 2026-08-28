package serverkit

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/stackshy/cloudemu/v2/persist"
)

// restoreState loads the snapshot at path (if any) into the freshly-built
// providers. A missing file is not an error — the server just starts empty,
// exactly as it does without --persist. Providers present in the snapshot but
// not running now are skipped.
func restoreState(ctx context.Context, path string, targets map[string]persist.Services) error {
	snap, err := persist.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // first run — nothing to restore
		}

		// A corrupt / truncated / unknown-schema snapshot must not wedge startup
		// on the very stop→start path this feature serves: warn and start empty
		// rather than aborting.
		fmt.Fprintf(os.Stderr, "warning: ignoring unreadable state file %s: %v\n", path, err)

		return nil
	}

	return persist.RestoreAll(ctx, &snap, targets)
}

// snapshotState exports every running provider's state and writes the snapshot
// file. Called after Shutdown, so the providers are quiescent.
func snapshotState(ctx context.Context, path string, includeAssets bool, targets map[string]persist.Services) error {
	snap, err := persist.ExportAll(ctx, targets, persist.Options{IncludeAssets: includeAssets})
	if err != nil {
		return err
	}

	return snap.WriteFile(path)
}
