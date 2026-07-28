package elb

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

const concurrentUpdates = 16

func newLBForAttributes(t *testing.T) (*Mock, string) {
	t.Helper()

	m := New(config.NewOptions())

	lb, err := m.CreateLoadBalancer(context.Background(), driver.LBConfig{
		Name: "test-lb",
		Type: "application",
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	return m, lb.ARN
}

// TestUpdateLBAttributes_ConcurrentUpdatesAllLand pins both halves of the
// attribute bug. Under `-race` it catches the concurrent map write that used
// to crash the process; the final count catches the lost update that a
// Get-then-Put sequence produces once the lock is dropped in between.
func TestUpdateLBAttributes_ConcurrentUpdatesAllLand(t *testing.T) {
	t.Parallel()

	m, arn := newLBForAttributes(t)
	ctx := context.Background()

	var wg sync.WaitGroup

	for i := range concurrentUpdates {
		wg.Add(2)

		go func() {
			defer wg.Done()

			key := "custom.key." + strconv.Itoa(i)
			if _, err := m.UpdateLBAttributes(ctx, arn, func(a *driver.LBAttributes) {
				a.Extra[key] = strconv.Itoa(i)
			}); err != nil {
				t.Errorf("UpdateLBAttributes: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.GetLBAttributes(ctx, arn); err != nil {
				t.Errorf("GetLBAttributes: %v", err)
			}
		}()
	}

	wg.Wait()

	attrs, err := m.GetLBAttributes(ctx, arn)
	if err != nil {
		t.Fatalf("GetLBAttributes: %v", err)
	}

	for i := range concurrentUpdates {
		key := "custom.key." + strconv.Itoa(i)
		if got, ok := attrs.Extra[key]; !ok || got != strconv.Itoa(i) {
			t.Errorf("%s = %q (present=%v), want %d — a concurrent update was lost",
				key, got, ok, i)
		}
	}
}

// TestUpdateLBAttributes_ReturnedExtraIsIndependent checks the caller cannot
// reach into stored state through the map it was handed back.
func TestUpdateLBAttributes_ReturnedExtraIsIndependent(t *testing.T) {
	t.Parallel()

	m, arn := newLBForAttributes(t)
	ctx := context.Background()

	returned, err := m.UpdateLBAttributes(ctx, arn, func(a *driver.LBAttributes) {
		a.Extra["a"] = "1"
	})
	if err != nil {
		t.Fatalf("UpdateLBAttributes: %v", err)
	}

	returned.Extra["a"] = "tampered"
	returned.Extra["b"] = "added"

	stored, err := m.GetLBAttributes(ctx, arn)
	if err != nil {
		t.Fatalf("GetLBAttributes: %v", err)
	}

	if stored.Extra["a"] != "1" {
		t.Errorf(`stored Extra["a"] = %q, want "1" — mutating the returned map reached stored state`,
			stored.Extra["a"])
	}

	if _, ok := stored.Extra["b"]; ok {
		t.Error("a key added to the returned map appeared in stored state")
	}
}

func TestUpdateLBAttributes_UnknownARN(t *testing.T) {
	t.Parallel()

	m := New(config.NewOptions())

	if _, err := m.UpdateLBAttributes(context.Background(), "arn:aws:elasticloadbalancing:::nope",
		func(*driver.LBAttributes) {}); err == nil {
		t.Error("got nil error for an unknown load balancer, want not-found")
	}
}
