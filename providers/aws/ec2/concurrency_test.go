package ec2

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// TestConcurrentInstanceLifecycleRaceFree hammers a single instance with
// concurrent Start/Stop/Reboot/Describe/CreateTags/GetInstanceAttribute calls.
// Every one of these paths reads or mutates fields on the shared *instanceData,
// so without per-entity synchronization the -race detector flags a data race on
// State, Tags or settle. It must run clean under `go test -race`.
func TestConcurrentInstanceLifecycleRaceFree(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "ami-123", InstanceType: "t2.micro"}, 1)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	id := insts[0].ID
	ids := []string{id}

	const workers = 8

	const iterations = 40

	var wg sync.WaitGroup

	run := func(fn func()) {
		defer wg.Done()

		for i := 0; i < iterations; i++ {
			fn()
		}
	}

	wg.Add(6 * workers)

	for w := 0; w < workers; w++ {
		go run(func() { _ = m.StartInstances(ctx, ids) })
		go run(func() { _ = m.StopInstances(ctx, ids) })
		go run(func() { _ = m.RebootInstances(ctx, ids) })
		go run(func() { _, _ = m.DescribeInstances(ctx, ids, nil) })
		go run(func() { _ = m.TagResource(ctx, id, map[string]string{"k": "v"}) })
		go run(func() { _, _ = m.GetInstanceAttribute(ctx, id, attrMonitoring) })
	}

	wg.Wait()

	// The instance must still be describable and in a valid terminal state after
	// the storm, proving the locking preserved a consistent view.
	out, err := m.DescribeInstances(ctx, ids, nil)
	if err != nil {
		t.Fatalf("DescribeInstances after storm: %v", err)
	}

	if len(out) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(out))
	}
}
