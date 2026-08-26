package route53_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// TestSDKListRecordSetsReversedLabelOrder locks the AWS ordering guarantee:
// ListResourceRecordSets sorts by DNS name with the labels reversed (so the
// zone apex sorts before its subdomains), then by record type. The apex NS+SOA
// must come first, then the a/m/z subdomains in order.
func TestSDKListRecordSetsReversedLabelOrder(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("order.com."),
		CallerReference: aws.String("order-ref"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	zoneID := aws.ToString(created.HostedZone.Id)

	for _, sub := range []string{"z", "a", "m"} {
		if _, cerr := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
			HostedZoneId: aws.String(zoneID),
			ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{{
				Action: r53types.ChangeActionCreate,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            aws.String(sub + ".order.com."),
					Type:            r53types.RRTypeA,
					TTL:             aws.Int64(300),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
				},
			}}},
		}); cerr != nil {
			t.Fatalf("create %s: %v", sub, cerr)
		}
	}

	sets, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}

	type nt struct {
		name string
		typ  r53types.RRType
	}

	got := make([]nt, 0, len(sets.ResourceRecordSets))
	for i := range sets.ResourceRecordSets {
		got = append(got, nt{aws.ToString(sets.ResourceRecordSets[i].Name), sets.ResourceRecordSets[i].Type})
	}

	// Apex first (NS before SOA in ASCII type order), then subdomains a/m/z.
	want := []nt{
		{"order.com.", r53types.RRTypeNs},
		{"order.com.", r53types.RRTypeSoa},
		{"a.order.com.", r53types.RRTypeA},
		{"m.order.com.", r53types.RRTypeA},
		{"z.order.com.", r53types.RRTypeA},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d record sets, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch at %d: got %+v, want %+v (full: %+v)", i, got[i], want[i], got)
		}
	}
}

// TestSDKListRecordSetsTrickyOrdering locks the flat reversed-dotted sort for the
// cases a per-label compare gets wrong: a hyphen (0x2d) and wildcard (0x2a) both
// sort below the label terminator '.' (0x2e). So api-v2.order.com. sorts BEFORE
// api.order.com., *.order.com. sorts before a.order.com., and a nested
// subdomain x.api.order.com. sorts after api.order.com. The full order is then
// walked one record at a time to prove pagination matches the sort exactly.
func TestSDKListRecordSetsTrickyOrdering(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("order.com."),
		CallerReference: aws.String("tricky-ref"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	zoneID := aws.ToString(created.HostedZone.Id)

	// Create in a deliberately scrambled input order.
	for _, name := range []string{
		"api.order.com.", "x.api.order.com.", "a.order.com.", "*.order.com.", "api-v2.order.com.",
	} {
		if _, cerr := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
			HostedZoneId: aws.String(zoneID),
			ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{{
				Action: r53types.ChangeActionCreate,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            aws.String(name),
					Type:            r53types.RRTypeA,
					TTL:             aws.Int64(300),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
				},
			}}},
		}); cerr != nil {
			t.Fatalf("create %s: %v", name, cerr)
		}
	}

	sets, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}

	type nt struct {
		name string
		typ  r53types.RRType
	}

	got := make([]nt, 0, len(sets.ResourceRecordSets))
	for i := range sets.ResourceRecordSets {
		got = append(got, nt{aws.ToString(sets.ResourceRecordSets[i].Name), sets.ResourceRecordSets[i].Type})
	}

	want := []nt{
		{"order.com.", r53types.RRTypeNs},
		{"order.com.", r53types.RRTypeSoa},
		{"*.order.com.", r53types.RRTypeA},
		{"a.order.com.", r53types.RRTypeA},
		{"api-v2.order.com.", r53types.RRTypeA},
		{"api.order.com.", r53types.RRTypeA},
		{"x.api.order.com.", r53types.RRTypeA},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d record sets, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch at %d: got %+v, want %+v (full: %+v)", i, got[i], want[i], got)
		}
	}

	// Paginate MaxItems=1 through the whole set; page order must equal the sort.
	key := func(n nt) string { return n.name + "/" + string(n.typ) }
	wantSeq := make([]string, 0, len(want))
	for i := range want {
		wantSeq = append(wantSeq, key(want[i]))
	}

	gotSeq := make([]string, 0, len(wantSeq))
	var startName *string
	var startType r53types.RRType
	for i := 0; i < len(wantSeq)+1; i++ {
		page, perr := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
			HostedZoneId:    aws.String(zoneID),
			MaxItems:        aws.Int32(1),
			StartRecordName: startName,
			StartRecordType: startType,
		})
		if perr != nil {
			t.Fatalf("ListResourceRecordSets(page): %v", perr)
		}
		if len(page.ResourceRecordSets) != 1 {
			t.Fatalf("page returned %d records, want 1", len(page.ResourceRecordSets))
		}

		rr := page.ResourceRecordSets[0]
		gotSeq = append(gotSeq, key(nt{aws.ToString(rr.Name), rr.Type}))

		if !page.IsTruncated {
			break
		}
		startName, startType = page.NextRecordName, page.NextRecordType
	}

	if len(gotSeq) != len(wantSeq) {
		t.Fatalf("paged %d records, want %d: %v", len(gotSeq), len(wantSeq), gotSeq)
	}
	for i := range wantSeq {
		if gotSeq[i] != wantSeq[i] {
			t.Fatalf("pagination diverged from sort at %d: got %v, want %v", i, gotSeq, wantSeq)
		}
	}
}

// TestSDKListRecordSetsPaginationWalks locks that StartRecordName/StartRecordType
// pagination walks the reversed-label order correctly: paging one record at a
// time visits every record exactly once, in the same order as a full listing.
func TestSDKListRecordSetsPaginationWalks(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("page.com."),
		CallerReference: aws.String("page-ref"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	zoneID := aws.ToString(created.HostedZone.Id)

	for _, sub := range []string{"z", "a", "m", "b", "y"} {
		if _, cerr := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
			HostedZoneId: aws.String(zoneID),
			ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{{
				Action: r53types.ChangeActionCreate,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            aws.String(sub + ".page.com."),
					Type:            r53types.RRTypeA,
					TTL:             aws.Int64(300),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
				},
			}}},
		}); cerr != nil {
			t.Fatalf("create %s: %v", sub, cerr)
		}
	}

	full, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets(full): %v", err)
	}

	key := func(name string, typ r53types.RRType) string { return name + "/" + string(typ) }

	wantSeq := make([]string, 0, len(full.ResourceRecordSets))
	for i := range full.ResourceRecordSets {
		wantSeq = append(wantSeq,
			key(aws.ToString(full.ResourceRecordSets[i].Name), full.ResourceRecordSets[i].Type))
	}

	// Page one record at a time following NextRecordName/NextRecordType.
	gotSeq := make([]string, 0, len(wantSeq))
	var startName *string
	var startType r53types.RRType
	for i := 0; i < len(wantSeq)+1; i++ {
		page, perr := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
			HostedZoneId:    aws.String(zoneID),
			MaxItems:        aws.Int32(1),
			StartRecordName: startName,
			StartRecordType: startType,
		})
		if perr != nil {
			t.Fatalf("ListResourceRecordSets(page): %v", perr)
		}
		if len(page.ResourceRecordSets) != 1 {
			t.Fatalf("page returned %d records, want 1", len(page.ResourceRecordSets))
		}

		rr := page.ResourceRecordSets[0]
		gotSeq = append(gotSeq, key(aws.ToString(rr.Name), rr.Type))

		if !page.IsTruncated {
			break
		}
		startName, startType = page.NextRecordName, page.NextRecordType
	}

	if len(gotSeq) != len(wantSeq) {
		t.Fatalf("paged %d records, want %d: %v", len(gotSeq), len(wantSeq), gotSeq)
	}
	for i := range wantSeq {
		if gotSeq[i] != wantSeq[i] {
			t.Fatalf("pagination diverged at %d: got %v, want %v", i, gotSeq, wantSeq)
		}
	}
}
