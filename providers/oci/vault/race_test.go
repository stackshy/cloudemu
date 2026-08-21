package vault

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// The stores hand back pointers, so writing a version mutates the very record
// a concurrent read is projecting. These tests fail under -race if the Mock's
// mutex is dropped from either path, and the create test fails outright: the
// duplicate-name check and the write are only atomic together.

const raceGoroutines = 16

func TestConcurrentVersionWritesAndReads(t *testing.T) {
	t.Parallel()

	m := newTestMock()
	ctx := context.Background()
	s := newSecret(t, m, testCompartment, "hot", "initial")

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(5)

		go func() {
			defer wg.Done()

			if _, err := m.PutSecretValue(ctx, "hot", []byte(fmt.Sprintf("v%d", i))); err != nil {
				t.Errorf("PutSecretValue: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.GetSecretBundle(s.ID, BundleSelector{}); err != nil {
				t.Errorf("GetSecretBundle: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.ListOCISecretVersions(s.ID); err != nil {
				t.Errorf("ListOCISecretVersions: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.ListOCISecrets(testCompartment, "", ""); err != nil {
				t.Errorf("ListOCISecrets: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.CreateKeyVersion(s.KeyID); err != nil {
				t.Errorf("CreateKeyVersion: %v", err)
			}
		}()
	}

	wg.Wait()

	versions, err := m.ListOCISecretVersions(s.ID)
	require.NoError(t, err)
	assert.Len(t, versions, raceGoroutines+1)
}

// Exactly one concurrent create of the same name may win, and the losers must
// all report AlreadyExists rather than overwriting each other.
func TestConcurrentCreateOfTheSameName(t *testing.T) {
	t.Parallel()

	m := newTestMock()
	ctx := context.Background()

	var (
		wg       sync.WaitGroup
		created  atomic.Int64
		conflict atomic.Int64
	)

	for range raceGoroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()

			switch _, err := m.CreateSecret(ctx, driver.SecretConfig{Name: "contended"}, []byte("v")); {
			case err == nil:
				created.Add(1)
			case cerrors.GetCode(err) == cerrors.AlreadyExists:
				conflict.Add(1)
			default:
				t.Errorf("CreateSecret: %v", err)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int64(1), created.Load())
	assert.Equal(t, int64(raceGoroutines-1), conflict.Load())

	// The default vault was minted exactly once despite the contention.
	vaults, err := m.ListVaults(testCompartment)
	require.NoError(t, err)
	assert.Len(t, vaults, 1)
}

// Scheduling and cancelling a deletion concurrently must leave the secret in
// one of the two states, never a torn one.
func TestConcurrentScheduleAndCancelDeletion(t *testing.T) {
	t.Parallel()

	m := newTestMock()
	s := newSecret(t, m, testCompartment, "contended-deletion", "v")

	var wg sync.WaitGroup

	for range raceGoroutines {
		wg.Add(3)

		go func() {
			defer wg.Done()

			//nolint:errcheck // either order is legal; the test is for the race detector.
			m.ScheduleOCISecretDeletion(s.ID, "")
		}()

		go func() {
			defer wg.Done()

			//nolint:errcheck // either order is legal; the test is for the race detector.
			m.CancelOCISecretDeletion(s.ID)
		}()

		go func() {
			defer wg.Done()

			if _, err := m.GetOCISecret(s.ID); err != nil {
				t.Errorf("GetOCISecret: %v", err)
			}
		}()
	}

	wg.Wait()

	got, err := m.GetOCISecret(s.ID)
	require.NoError(t, err)
	assert.Contains(t, []string{StateActive, StatePendingDeletion}, got.LifecycleState)
}
