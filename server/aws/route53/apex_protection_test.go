package route53_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// TestSDKApexSOANSProtected pins that ChangeResourceRecordSets rejects a batch
// that would delete the zone's mandatory apex SOA or NS record set outright,
// matching real Route 53's "A HostedZone must contain exactly one SOA record"
// / "...must contain at least one NS record for the zone itself"
// InvalidChangeBatch errors — while still allowing an in-place edit (a DELETE
// paired with a CREATE of the same record set in one batch) since the batch's
// net effect still leaves the apex record standing.
func TestSDKApexSOANSProtected(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()
	zoneID := createRoutingZone(t, client, "apex.com.")

	soa, ns := apexRecordSets(t, client, zoneID)

	// A bare DELETE of the apex SOA (exact match) is rejected.
	_, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{Action: r53types.ChangeActionDelete, ResourceRecordSet: soa}},
		},
	})

	var badBatch *r53types.InvalidChangeBatch
	if !errors.As(err, &badBatch) {
		t.Fatalf("DELETE apex SOA = %v, want InvalidChangeBatch", err)
	}

	// A bare DELETE of the apex NS is rejected the same way.
	_, err = client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{Action: r53types.ChangeActionDelete, ResourceRecordSet: ns}},
		},
	})
	if !errors.As(err, &badBatch) {
		t.Fatalf("DELETE apex NS = %v, want InvalidChangeBatch", err)
	}

	// Both rejected batches must leave the zone untouched: still exactly the
	// seeded SOA + NS record sets, nothing partially applied.
	rrsets, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{HostedZoneId: aws.String(zoneID)})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}

	if got := len(rrsets.ResourceRecordSets); got != 2 {
		t.Fatalf("record set count after rejected apex deletes = %d, want 2 (SOA+NS)", got)
	}

	// An in-place edit — DELETE the current SOA and CREATE its replacement in
	// the same batch — is allowed: the batch's net effect still leaves an SOA
	// record standing.
	edited := *soa
	edited.ResourceRecords = []r53types.ResourceRecord{{
		Value: aws.String(aws.ToString(soa.ResourceRecords[0].Value) + "-edited"),
	}}

	_, err = client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{
			{Action: r53types.ChangeActionDelete, ResourceRecordSet: soa},
			{Action: r53types.ChangeActionCreate, ResourceRecordSet: &edited},
		}},
	})
	if err != nil {
		t.Fatalf("DELETE+CREATE apex SOA (edit) = %v, want success", err)
	}

	// An UPSERT of the apex NS is likewise allowed.
	editedNS := *ns
	editedNS.ResourceRecords = append(append([]r53types.ResourceRecord(nil), ns.ResourceRecords...),
		r53types.ResourceRecord{Value: aws.String("ns-extra.awsdns-00.com.")})

	_, err = client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{Action: r53types.ChangeActionUpsert, ResourceRecordSet: &editedNS}},
		},
	})
	if err != nil {
		t.Fatalf("UPSERT apex NS (edit) = %v, want success", err)
	}
}

// apexRecordSets returns the zone's auto-seeded apex SOA and NS record sets,
// failing the test if either is missing.
func apexRecordSets(t *testing.T, client *awsr53.Client, zoneID string) (soa, ns *r53types.ResourceRecordSet) {
	t.Helper()

	rrsets, err := client.ListResourceRecordSets(context.Background(), &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}

	for i := range rrsets.ResourceRecordSets {
		switch rrsets.ResourceRecordSets[i].Type {
		case r53types.RRTypeSoa:
			soa = &rrsets.ResourceRecordSets[i]
		case r53types.RRTypeNs:
			ns = &rrsets.ResourceRecordSets[i]
		}
	}

	if soa == nil || ns == nil {
		t.Fatalf("expected auto-seeded SOA and NS record sets, got %d record sets", len(rrsets.ResourceRecordSets))
	}

	return soa, ns
}
