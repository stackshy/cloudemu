package ec2

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// TestRunInstancesClientTokenConcurrent pins that two concurrent RunInstances
// carrying the same ClientToken provision exactly one instance set — the token
// check-and-reserve is atomic, so a race cannot double-provision. Run with -race.
func TestRunInstancesClientTokenConcurrent(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := defaultConfig()
	cfg.ClientToken = "shared-token"

	const goroutines = 8

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results [][]driver.Instance
	)

	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			got, err := m.RunInstances(ctx, cfg, 1)
			if err != nil {
				t.Errorf("RunInstances: %v", err)
				return
			}

			mu.Lock()
			results = append(results, got)
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Every caller must observe the same single instance id.
	wantID := results[0][0].ID
	for _, r := range results {
		if len(r) != 1 || r[0].ID != wantID {
			t.Fatalf("caller saw instances %v, want exactly [%q]", instanceIDsOf(r), wantID)
		}
	}

	// The backend must hold exactly one instance, not one per goroutine.
	all, err := m.DescribeInstances(ctx, nil, nil)
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	if len(all) != 1 {
		t.Fatalf("backend holds %d instances, want 1 (no double-provisioning)", len(all))
	}

	if all[0].ID != wantID {
		t.Fatalf("backend instance %q, want %q", all[0].ID, wantID)
	}
}

// TestRunInstancesClientTokenSequential pins that sequential retries with the
// same ClientToken keep returning the same instance set (idempotency), and that
// a different token provisions a distinct instance.
func TestRunInstancesClientTokenSequential(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := defaultConfig()
	cfg.ClientToken = "token-a"

	first, err := m.RunInstances(ctx, cfg, 1)
	if err != nil {
		t.Fatalf("RunInstances (first): %v", err)
	}

	retry, err := m.RunInstances(ctx, cfg, 1)
	if err != nil {
		t.Fatalf("RunInstances (retry): %v", err)
	}

	if len(retry) != 1 || retry[0].ID != first[0].ID {
		t.Fatalf("retry returned %v, want same as first %q", instanceIDsOf(retry), first[0].ID)
	}

	cfg.ClientToken = "token-b"

	other, err := m.RunInstances(ctx, cfg, 1)
	if err != nil {
		t.Fatalf("RunInstances (other token): %v", err)
	}

	if other[0].ID == first[0].ID {
		t.Fatal("a different ClientToken returned the same instance, want a new one")
	}

	all, err := m.DescribeInstances(ctx, nil, nil)
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	if len(all) != 2 {
		t.Fatalf("backend holds %d instances, want 2", len(all))
	}
}

func instanceIDsOf(insts []driver.Instance) []string {
	ids := make([]string, len(insts))
	for i := range insts {
		ids[i] = insts[i].ID
	}

	return ids
}
