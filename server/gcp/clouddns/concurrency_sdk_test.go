package clouddns_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
)

// TestSDKChangesCreateConcurrentSameAdditionExactlyOneWins locks
// changes.create's atomicity for the SAME name+type record set: two callers
// racing to add it must not both succeed (Cloud DNS rejects the loser with
// alreadyExists), and the zone must end up with exactly one record set —
// never zero (lost) and never duplicated. Note this same-key case is actually
// guaranteed by CreateRecord's atomic SetIfAbsent (providers/gcp/clouddns)
// regardless of applyMu, since both additions land on the same store key; see
// TestSDKChangesCreateConcurrentCNAMEExclusivityAcrossKeys below for the
// cross-key case that only applyMu protects.
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

// TestSDKDeleteZoneConcurrentWithChangesCreateNeverLosesRecord exercises
// managedZones.delete racing a changes.create on the same zone: every outcome
// must be consistent (either the added record survives because delete saw it
// and refused with containerNotEmpty, or delete won the race and the add then
// targets a deleted zone), never a zone deleted while a record the check
// should have seen silently disappears without a trace. This scenario is
// covered by applyMu but does not reliably falsify on its own if applyMu is
// removed — see TestSDKChangesCreateConcurrentCNAMEExclusivityAcrossKeys for
// the test that does.
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

// TestSDKChangesCreateConcurrentCNAMEExclusivityAcrossKeys is the test that
// actually falsifies on a reverted applyMu. A CNAME addition and an A
// addition for the SAME name land on different dns driver store keys
// (recordKey folds in the record type), so CreateRecord's per-key
// SetIfAbsent — which is what protects the same-key case above — cannot see
// or prevent this collision. Cloud DNS forbids a name from carrying both a
// CNAME and any other record type; checkCNAME enforces that by reading the
// zone's current records before a batch applies. Without applyMu serializing
// changes.create's validate-then-apply span, two concurrent batches (one
// adding the CNAME, one adding the A) can each read the pre-change state —
// neither sees the other's not-yet-applied addition — conclude there's no
// conflict, and both apply, leaving the zone with both. With applyMu in
// place, the second batch to run always observes the first's already-applied
// record and is rejected.
func TestSDKChangesCreateConcurrentCNAMEExclusivityAcrossKeys(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name: "cname-exclusivity", DnsName: "cname-exclusivity.example.com.",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}

	const trials = 100

	var wg sync.WaitGroup

	for i := range trials {
		name := fmt.Sprintf("race%d.cname-exclusivity.example.com.", i)

		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = svc.Changes.Create(testProject, "cname-exclusivity", &dns.Change{
				Additions: []*dns.ResourceRecordSet{
					{Name: name, Type: "CNAME", Ttl: 300, Rrdatas: []string{"target.example.com."}},
				},
			}).Context(ctx).Do()
		}()

		go func() {
			defer wg.Done()

			_, _ = svc.Changes.Create(testProject, "cname-exclusivity", &dns.Change{
				Additions: []*dns.ResourceRecordSet{
					{Name: name, Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"}},
				},
			}).Context(ctx).Do()
		}()
	}

	wg.Wait()

	for i := range trials {
		name := fmt.Sprintf("race%d.cname-exclusivity.example.com.", i)

		rrsets, err := svc.ResourceRecordSets.List(testProject, "cname-exclusivity").Name(name).Context(ctx).Do()
		if err != nil {
			t.Fatalf("ResourceRecordSets.List(%s): %v", name, err)
		}

		if len(rrsets.Rrsets) > 1 {
			t.Fatalf("name %s has %d record sets, want at most 1 (CNAME exclusivity violated): %+v",
				name, len(rrsets.Rrsets), rrsets.Rrsets)
		}
	}
}
