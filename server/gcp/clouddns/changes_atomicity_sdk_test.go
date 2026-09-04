package clouddns_test

import (
	"context"
	"testing"

	dns "google.golang.org/api/dns/v1"
)

// TestSDKChangesCreateDuplicateDeletionRejectedAtomically guards a real-user
// e2e finding: a batch naming the same (name,type) twice in its deletions
// used to fail with a 404 on the SECOND occurrence, but only after the FIRST
// occurrence had already deleted the record — a half-applied "atomic" change.
// Cloud DNS's changes.create is documented as all-or-nothing, so such a batch
// must be rejected up front, before any mutation, leaving the record intact.
func TestSDKChangesCreateDuplicateDeletionRejectedAtomically(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	createZone(t, svc, &dns.ManagedZone{Name: "dupdel-zone", DnsName: "dupdel.example.com."})

	rr := &dns.ResourceRecordSet{Name: "www.dupdel.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"}}
	if err := addRecord(t, svc, "dupdel-zone", rr); err != nil {
		t.Fatalf("seed addRecord: %v", err)
	}

	_, err := svc.Changes.Create(testProject, "dupdel-zone", &dns.Change{
		Deletions: []*dns.ResourceRecordSet{rr, rr},
	}).Context(ctx).Do()
	if code := apiCode(t, err); code != 400 {
		t.Fatalf("duplicate deletion in one batch: got %v, want 400", err)
	}

	rec := findRecord(t, svc, ctx, "dupdel-zone", "www.dupdel.example.com.")
	if rec == nil {
		t.Fatal("record was deleted despite the batch being rejected — non-atomic partial apply")
	}
}

// TestSDKChangesCreateDuplicateAdditionRejectedAtomically is the additions-side
// counterpart: two additions for the same (name,type) in one batch used to let
// the first SetIfAbsent succeed and only the second fail with 409, again
// leaving a mutation behind a batch that reported failure.
func TestSDKChangesCreateDuplicateAdditionRejectedAtomically(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	createZone(t, svc, &dns.ManagedZone{Name: "dupadd-zone", DnsName: "dupadd.example.com."})

	_, err := svc.Changes.Create(testProject, "dupadd-zone", &dns.Change{
		Additions: []*dns.ResourceRecordSet{
			{Name: "www.dupadd.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"}},
			{Name: "www.dupadd.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.2"}},
		},
	}).Context(ctx).Do()
	if code := apiCode(t, err); code != 400 {
		t.Fatalf("duplicate addition in one batch: got %v, want 400", err)
	}

	rec := findRecord(t, svc, ctx, "dupadd-zone", "www.dupadd.example.com.")
	if rec != nil {
		t.Fatalf("record was created despite the batch being rejected — non-atomic partial apply: %+v", rec)
	}
}
