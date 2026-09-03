package clouddns_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
)

// TestSDKChangesCreateConcurrentSameAdditionExactlyOneWins locks
// changes.create's atomicity under concurrency: two callers racing to add the
// SAME name+type record set to the same zone must not both succeed (Cloud
// DNS rejects the loser with alreadyExists), and the zone must end up with
// exactly one record set at that name+type — never zero (lost) and never
// duplicated. Before the fix, createChange validated the whole batch and only
// then applied it with no lock spanning the two steps, so two concurrent
// requests could both pass validation against the same pre-change state.
func TestSDKChangesCreateConcurrentSameAdditionExactlyOneWins(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name: "concurrent-add", DnsName: "concurrent-add.example.com.",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}

	const workers = 12

	var (
		wg     sync.WaitGroup
		oks    atomic.Int32
		exists atomic.Int32
		other  atomic.Int32
	)

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := svc.Changes.Create(testProject, "concurrent-add", &dns.Change{
				Additions: []*dns.ResourceRecordSet{
					{Name: "www.concurrent-add.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"}},
				},
			}).Context(ctx).Do()

			var gerr *googleapi.Error

			switch {
			case err == nil:
				oks.Add(1)
			case errors.As(err, &gerr) && gerr.Code == 409:
				exists.Add(1)
			default:
				other.Add(1)
			}
		}()
	}

	wg.Wait()

	if other.Load() != 0 {
		t.Fatalf("unexpected error kind from %d calls", other.Load())
	}

	if oks.Load() != 1 {
		t.Fatalf("successful changes.create = %d, want exactly 1 (rejected=%d)", oks.Load(), exists.Load())
	}

	rrsets, err := svc.ResourceRecordSets.List(testProject, "concurrent-add").
		Name("www.concurrent-add.example.com.").Type("A").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ResourceRecordSets.List: %v", err)
	}

	if len(rrsets.Rrsets) != 1 {
		t.Fatalf("rrsets for www.concurrent-add.example.com./A = %d, want 1", len(rrsets.Rrsets))
	}
}

// TestSDKDeleteZoneConcurrentWithChangesCreateNeverLosesRecord locks the
// applyMu lock added to serialize managedZones.delete's empty-check-then-
// delete span against changes.create: a concurrent changes.create adding a
// record must not be able to land in the window between delete's check and
// its call to the driver — every outcome must be consistent (either the
// added record survives because delete saw it and refused with
// containerNotEmpty, or delete won the race and the add then targets a
// deleted zone), never a zone deleted while a record the check should have
// seen silently disappears without a trace.
func TestSDKDeleteZoneConcurrentWithChangesCreateNeverLosesRecord(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name: "concurrent-del", DnsName: "concurrent-del.example.com.",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}

	var wg sync.WaitGroup

	var addErr, delErr error

	wg.Add(2)

	go func() {
		defer wg.Done()

		_, err := svc.Changes.Create(testProject, "concurrent-del", &dns.Change{
			Additions: []*dns.ResourceRecordSet{
				{Name: "www.concurrent-del.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"}},
			},
		}).Context(ctx).Do()
		addErr = err
	}()

	go func() {
		defer wg.Done()

		delErr = svc.ManagedZones.Delete(testProject, "concurrent-del").Context(ctx).Do()
	}()

	wg.Wait()

	zoneGone := delErr == nil

	if zoneGone {
		// Delete won: it must have seen an empty zone, so the add must have
		// failed against a zone that no longer exists (never silently applied
		// to a zone delete then claimed was empty).
		if addErr == nil {
			t.Fatalf("zone was deleted but the concurrent record addition also reported success")
		}

		return
	}

	// The add won (or both landed with the delete rejected): the zone must
	// still exist, and if the addition succeeded the record must be present.
	_, getErr := svc.ManagedZones.Get(testProject, "concurrent-del").Context(ctx).Do()
	if getErr != nil {
		t.Fatalf("zone missing after delete was rejected: %v", getErr)
	}

	if addErr == nil {
		rrsets, err := svc.ResourceRecordSets.List(testProject, "concurrent-del").
			Name("www.concurrent-del.example.com.").Type("A").Context(ctx).Do()
		if err != nil {
			t.Fatalf("ResourceRecordSets.List: %v", err)
		}

		if len(rrsets.Rrsets) != 1 {
			t.Fatalf("addition reported success but rrset count = %d, want 1", len(rrsets.Rrsets))
		}
	}
}
