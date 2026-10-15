package vtl

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRenderSlotsBoundConcurrency fills every render slot and checks a further
// render waits for one, giving up when its context ends.
func TestRenderSlotsBoundConcurrency(t *testing.T) {
	for range cap(renderSlots) {
		renderSlots <- struct{}{}
	}

	defer func() {
		for range cap(renderSlots) {
			<-renderSlots
		}
	}()

	tmpl, err := Parse("x")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := tmpl.Render(ctx, nil, RenderOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("render with no free slot: err = %v", err)
	}
}

// TestInFlightBudgetIsShared checks that concurrent renders draw on one
// process-wide pool and return their bytes when they end.
func TestInFlightBudgetIsShared(t *testing.T) {
	a, b, c := newSharedBudget(), newSharedBudget(), newSharedBudget()

	if err := a.charge(MaxAllocBytes - 1); err != nil {
		t.Fatal(err)
	}

	if err := b.charge(MaxAllocBytes - 1); err != nil {
		t.Fatal(err)
	}

	if err := c.check(MaxAllocBytes / 2); !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("third render fit a full pool: %v", err)
	}

	if err := c.charge(MaxAllocBytes / 2); !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("third render charged a full pool: %v", err)
	}

	a.release()
	b.release()
	c.release()

	if got := inFlightBytes.Load(); got != 0 {
		t.Fatalf("pool holds %d bytes after release", got)
	}
}
