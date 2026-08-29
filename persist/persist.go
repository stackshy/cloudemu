// Package persist snapshots cloudemu provider state to disk and restores it, so
// emulated resources survive a stop/start of the standalone server.
//
// Persistence is fully generic and full-surface: it iterates the per-provider
// map of services that implement internal/snapshot.Snapshottable (produced by
// each provider factory's SnapshotServices()), captures each service's
// identity-preserving self-snapshot under ProviderState.Services keyed by
// service name, and restores each one through the same interface. Because
// resource ids and id-string cross-references are serialized as-is, a
// snapshot/restore round-trip is transparent to clients. The on-disk file is
// human-readable, diffable JSON rather than an opaque binary blob, and one
// snapshot format spans AWS, Azure, GCP, and OCI.
package persist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"syscall"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

// SchemaVersion is the on-disk snapshot format version. Bumped to 3 for the
// full-surface generic layout: every service captures itself under
// ProviderState.Services and the bespoke per-kind arrays of the v2 layout are
// gone, so a v2 (or older) snapshot can no longer be read. Snapshots are a
// dev-only convenience, so a clean break with a clear error is acceptable.
const SchemaVersion = 3

const dirPerm = 0o755

// errSchema is returned when a snapshot's schema version is not the one this
// build reads. Static so callers (and err113) get a wrappable failure.
var errSchema = errors.New("unsupported snapshot schema version")

// Services maps a stable service name to the mock that snapshots itself. It is
// what a provider factory's SnapshotServices() returns and what Export/Restore
// iterate.
type Services = map[string]snapshot.Snapshottable

// Snapshot is a multi-cloud, point-in-time capture of provider state, written
// as one JSON document.
type Snapshot struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Meta          *Meta                    `json:"meta,omitempty"`
	Providers     map[string]ProviderState `json:"providers,omitempty"`
}

// Meta is optional descriptive header for a named snapshot (the auto
// persist-on-stop file leaves it nil). It lets tooling describe a snapshot
// without restoring it.
type Meta struct {
	Name            string   `json:"name,omitempty"`
	CreatedAt       string   `json:"createdAt,omitempty"`
	CloudemuVersion string   `json:"cloudemuVersion,omitempty"`
	Providers       []string `json:"providers,omitempty"`
}

// ProviderState is a single provider's persisted resources: the per-service
// identity-preserving snapshots keyed by service name.
type ProviderState struct {
	Services map[string]json.RawMessage `json:"services,omitempty"`
}

// Options controls what Export captures.
type Options struct {
	// IncludeAssets includes large object bodies (e.g. S3 object bytes). When
	// false (the default) the snapshot is metadata-only, which keeps the file
	// small. The flag is threaded to each service's Snapshot, so a service that
	// distinguishes metadata from bulk bytes honors it.
	IncludeAssets bool
}

// ExportAll captures the state of every provider in targets into one Snapshot.
// It is the whole-emulator core shared by the persist-on-stop path and the
// snapshot admin endpoint. Each provider's value is its SnapshotServices() map.
func ExportAll(ctx context.Context, targets map[string]Services, opts Options) (Snapshot, error) {
	snap := Snapshot{SchemaVersion: SchemaVersion, Providers: make(map[string]ProviderState, len(targets))}

	for name := range targets {
		ps, err := Export(ctx, targets[name], opts)
		if err != nil {
			return Snapshot{}, fmt.Errorf("export %s: %w", name, err)
		}

		snap.Providers[name] = ps
	}

	return snap, nil
}

// RestoreAll restores each provider present in snap into the matching target.
// Providers in the snapshot with no matching running target are skipped, and
// targets should be freshly rebuilt (empty) before calling.
func RestoreAll(ctx context.Context, snap *Snapshot, targets map[string]Services) error {
	for name := range snap.Providers {
		svcs, ok := targets[name]
		if !ok {
			continue
		}

		ps := snap.Providers[name]
		if err := Restore(ctx, svcs, &ps); err != nil {
			return fmt.Errorf("restore %s: %w", name, err)
		}
	}

	return nil
}

// Export captures a provider's current state by calling each service's
// identity-preserving self-snapshot, keyed by service name under
// ProviderState.Services. Keys are visited in sorted order for stable output.
func Export(ctx context.Context, services Services, opts Options) (ProviderState, error) {
	ps := ProviderState{Services: map[string]json.RawMessage{}}

	for _, name := range sortedKeys(services) {
		s := services[name]
		if s == nil {
			continue
		}

		raw, err := s.Snapshot(ctx, opts.IncludeAssets)
		if err != nil {
			return ProviderState{}, fmt.Errorf("snapshot %s: %w", name, err)
		}

		ps.Services[name] = raw
	}

	if len(ps.Services) == 0 {
		ps.Services = nil
	}

	return ps, nil
}

// Restore writes a provider state back into what should be a freshly-built
// (empty) provider, restoring each captured service through its mock's
// Snapshottable.Restore. A service captured in the snapshot but not present in
// services (e.g. a snapshot from a build with a wider surface) is skipped.
// Sorted iteration keeps restore order deterministic.
func Restore(ctx context.Context, services Services, ps *ProviderState) error {
	names := make([]string, 0, len(ps.Services))
	for name := range ps.Services {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		s, ok := services[name]
		if !ok {
			// The snapshot carries a service this build no longer exposes (a
			// wider-surface or newer snapshot restored into a narrower build).
			// Skipping is intentional, but a silent skip hides state loss, so
			// warn — this is what #817 asked for.
			log.Printf("persist: restore: snapshot service %q has no matching target service; skipping", name)

			continue
		}

		if s == nil {
			continue
		}

		if err := s.Restore(ctx, ps.Services[name]); err != nil {
			return fmt.Errorf("restore %s: %w", name, err)
		}
	}

	return nil
}

func sortedKeys(services Services) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

// WriteFile writes the snapshot as indented JSON, creating parent directories.
func (s Snapshot) WriteFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	// Write to a temp file in the same dir, fsync it, then rename onto the
	// target. The full ordering — temp-write → fsync file → rename → best-effort
	// fsync parent dir — is what makes the write crash-safe: fsync forces the
	// data blocks to disk before the rename publishes the name, so power loss can
	// leave the previous snapshot (or none) but never a truncated/empty file, and
	// rename is atomic on the same filesystem. The parent-dir fsync then makes the
	// rename entry itself durable. Darwin caveat: Go's File.Sync issues fsync(2),
	// which on macOS flushes to the drive but does NOT flush the drive's own write
	// cache (a true device flush needs fcntl(F_FULLFSYNC), which is not issued
	// here), so on darwin this is best-effort, not a hard power-loss guarantee.
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)

		return err
	}

	// fsync the data to disk before the rename. This is the critical fix: without
	// it the rename can be committed to the directory before the file's blocks
	// reach disk, leaving an empty/truncated state file after a crash.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)

		return err
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)

		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)

		return err
	}

	// Make the rename entry itself durable. Best-effort by design (see fsyncDir).
	if err := fsyncDir(dir); err != nil {
		return err
	}

	return nil
}

// fsyncDir fsyncs a directory so a rename into it is durable across a crash. It
// is best-effort: opening a directory handle for fsync fails on Windows, and
// some filesystems reject a directory fsync with EINVAL/ENOTSUP. Those are
// swallowed (there is nothing the caller can do and the file data is already
// synced); genuinely unexpected errors are returned.
func fsyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		// Windows has no fsync-the-directory concept; the rename is durable once
		// the file was synced above.
		return nil
	}

	d, err := os.Open(dir)
	if err != nil {
		// Cannot open the directory as a handle on this platform/FS; treat as
		// unsupported rather than failing the whole write.
		return nil
	}

	syncErr := d.Sync()

	if closeErr := d.Close(); closeErr != nil && syncErr == nil {
		syncErr = closeErr
	}

	if syncErr == nil || errors.Is(syncErr, syscall.EINVAL) || errors.Is(syncErr, syscall.ENOTSUP) {
		// EINVAL/ENOTSUP: this filesystem does not support fsync on a directory.
		return nil
	}

	return syncErr
}

// ReadFile loads a snapshot from disk, rejecting an incompatible schema version
// with a clear error rather than silently mis-restoring a stale layout.
func ReadFile(path string) (Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}

	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return Snapshot{}, fmt.Errorf("parse snapshot %q: %w", path, err)
	}

	if s.SchemaVersion != SchemaVersion {
		return Snapshot{}, fmt.Errorf(
			"%w: snapshot schema v%d is not compatible with this build (expected v%d); re-create the snapshot",
			errSchema, s.SchemaVersion, SchemaVersion)
	}

	return s, nil
}
