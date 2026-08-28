package serverkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/seed"
)

// applyInitDir applies every *.json fixture in dir (lexical order) to every
// running provider on boot, bringing the emulator up to a known state. A missing
// dir is a no-op. A parse error fails startup (clear misconfiguration); an apply
// error only warns and continues, so a fixture that collides with
// already-restored state can't wedge the boot.
func applyInitDir(ctx context.Context, dir string, targets map[string]seed.Target) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	names := make([]string, 0, len(entries))

	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}

	sort.Strings(names)

	for _, name := range names {
		if err := applyInitFile(ctx, filepath.Join(dir, name), name, targets); err != nil {
			return err
		}
	}

	return nil
}

// applyInitFile loads one fixture file and applies it to every provider. A load
// (parse) error is returned; per-provider apply errors are warned and skipped.
func applyInitFile(ctx context.Context, path, name string, targets map[string]seed.Target) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	f, err := seed.Load(data)
	if err != nil {
		return fmt.Errorf("init fixture %s: %w", name, err)
	}

	for prov, t := range targets {
		// IgnoreExisting so a resource that already exists (from restored state or
		// an earlier init file) is skipped rather than aborting the rest of the
		// fixture; other errors still warn.
		if err := seed.Apply(ctx, f, t, seed.IgnoreExisting()); err != nil {
			fmt.Fprintf(os.Stderr, "warning: init %s on %s: %v\n", name, prov, err)
		}
	}

	return nil
}
