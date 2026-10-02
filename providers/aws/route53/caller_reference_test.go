package route53

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

func TestCreateZoneReusedCallerReference(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	first, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com", CallerReference: "ref-1"})
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	if _, err = m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com", CallerReference: "ref-1"}); !errors.IsAlreadyExists(err) {
		t.Fatalf("reused CallerReference: err = %v, want AlreadyExists", err)
	}

	if _, err = m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com", CallerReference: "ref-2"}); err != nil {
		t.Fatalf("new CallerReference: %v", err)
	}

	zones, err := m.ListZones(ctx, scope.Scope{})
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}

	if len(zones) != 2 {
		t.Fatalf("zone count = %d, want 2 (no duplicate on retry)", len(zones))
	}

	// Deleting the zone frees its reference.
	if err = m.DeleteZone(ctx, first.ID); err != nil {
		t.Fatalf("DeleteZone: %v", err)
	}

	if _, err = m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com", CallerReference: "ref-1"}); err != nil {
		t.Fatalf("CallerReference reuse after delete: %v", err)
	}
}

func TestCreateZoneEmptyCallerReferenceNeverCollides(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	for range 2 {
		if _, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com"}); err != nil {
			t.Fatalf("CreateZone without CallerReference: %v", err)
		}
	}
}

func TestCreateZoneCallerReferenceConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	const n = 20

	errs := make([]error, n)

	var wg sync.WaitGroup

	start := make(chan struct{})

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			_, errs[i] = m.CreateZone(ctx, driver.ZoneConfig{Name: "example.com", CallerReference: "burst"})
		}()
	}

	close(start)
	wg.Wait()

	created := 0

	for i := range n {
		switch {
		case errs[i] == nil:
			created++
		case !errors.IsAlreadyExists(errs[i]):
			t.Fatalf("call %d: err = %v, want nil or AlreadyExists", i, errs[i])
		}
	}

	zones, err := m.ListZones(ctx, scope.Scope{})
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}

	if created != 1 || len(zones) != 1 {
		t.Fatalf("created=%d zones=%d, want exactly 1 zone for one CallerReference", created, len(zones))
	}
}
