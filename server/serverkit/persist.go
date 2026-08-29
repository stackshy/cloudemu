package serverkit

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/stackshy/cloudemu/v2/persist"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// restoreState loads the snapshot at path (if any) into the freshly-built
// providers and the shared Kubernetes data plane. A missing file is not an error
// — the server just starts empty, exactly as it does without --persist. Providers
// present in the snapshot but not running now are skipped. k8s may be nil (the
// data plane is disabled), in which case any persisted Kubernetes state is left
// alone.
func restoreState(
	ctx context.Context, path string, targets map[string]persist.Services, k8s *kubernetes.APIServer,
) error {
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

	if err := persist.RestoreAll(ctx, &snap, targets); err != nil {
		return err
	}

	return restoreKubernetes(ctx, &snap, k8s)
}

// restoreKubernetes reinstates the shared Kubernetes data plane from the
// top-level Kubernetes field. It is a no-op when the data plane is disabled or
// the snapshot carries no Kubernetes state, and fails loudly otherwise so a
// broken data-plane snapshot surfaces rather than silently leaving restored
// kubeconfigs pointing at empty clusters.
func restoreKubernetes(ctx context.Context, snap *persist.Snapshot, k8s *kubernetes.APIServer) error {
	if k8s == nil || len(snap.Kubernetes) == 0 {
		return nil
	}

	if err := k8s.Restore(ctx, snap.Kubernetes); err != nil {
		return fmt.Errorf("restore kubernetes data plane: %w", err)
	}

	return nil
}

// exportSnapshot captures every running provider's state and, when a data plane
// is wired, attaches the shared Kubernetes state. It is the single place the
// providers-before-Kubernetes ordering is enforced, shared by the flusher save
// and the admin snapshot endpoint so the two export call sites can never drift.
//
// The ordering is a correctness requirement, not a preference: a CreateCluster
// completing between the two captures inserts a provider-side UID whose
// ClusterState a later Kubernetes capture would still see, but never the reverse
// — so capturing providers first bounds the race to the harmless direction (an
// orphan ClusterState with no provider reference), never a dangling provider UID
// that restores to a 404.
func exportSnapshot(
	ctx context.Context, targets map[string]persist.Services, k8s *kubernetes.APIServer, includeAssets bool,
) (persist.Snapshot, error) {
	snap, err := persist.ExportAll(ctx, targets, persist.Options{IncludeAssets: includeAssets})
	if err != nil {
		return persist.Snapshot{}, err
	}

	if k8s != nil {
		raw, err := k8s.Snapshot(ctx, includeAssets)
		if err != nil {
			return persist.Snapshot{}, fmt.Errorf("snapshot kubernetes data plane: %w", err)
		}

		snap.Kubernetes = raw
	}

	return snap, nil
}

// snapshotState exports every running provider's state plus the shared Kubernetes
// data plane and writes the snapshot file. Called on a background tick / after
// Shutdown, so the providers are quiescent.
func snapshotState(
	ctx context.Context, path string, includeAssets bool,
	targets map[string]persist.Services, k8s *kubernetes.APIServer,
) error {
	snap, err := exportSnapshot(ctx, targets, k8s, includeAssets)
	if err != nil {
		return err
	}

	return snap.WriteFile(path)
}
