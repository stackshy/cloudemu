package route53_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// pageWalkLimit guards the manual Marker walks against a non-terminating
// paginator (a pagination bug that never clears IsTruncated).
const pageWalkLimit = 100

// TestSDKListHostedZonesPaginates walks ListHostedZones with a small MaxItems
// and asserts the Marker/NextMarker/IsTruncated contract returns every zone
// exactly once and terminates.
func TestSDKListHostedZonesPaginates(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	const total = 5
	want := make(map[string]int, total)
	for i := range total {
		id := createZone(t, client, fmt.Sprintf("zone-%d.com.", i), fmt.Sprintf("hz-ref-%d", i))
		want[id] = 0
	}

	got := make(map[string]int, total)
	var marker *string
	for pages := 0; ; pages++ {
		if pages > pageWalkLimit {
			t.Fatal("ListHostedZones did not terminate — IsTruncated never cleared")
		}

		out, err := client.ListHostedZones(ctx, &awsr53.ListHostedZonesInput{
			MaxItems: aws.Int32(2),
			Marker:   marker,
		})
		if err != nil {
			t.Fatalf("ListHostedZones: %v", err)
		}

		if n := len(out.HostedZones); n > 2 {
			t.Fatalf("page returned %d zones, want <= MaxItems 2", n)
		}

		for i := range out.HostedZones {
			got[aws.ToString(out.HostedZones[i].Id)]++
		}

		if !out.IsTruncated {
			if aws.ToString(out.NextMarker) != "" {
				t.Errorf("last page carries NextMarker %q, want empty", aws.ToString(out.NextMarker))
			}

			break
		}

		if aws.ToString(out.NextMarker) == "" {
			t.Fatal("truncated page has empty NextMarker")
		}

		marker = out.NextMarker
	}

	assertEachOnce(t, want, got)
}

// TestSDKListHealthChecksPaginates walks ListHealthChecks with a small MaxItems
// and asserts every health check is returned exactly once and the walk ends.
func TestSDKListHealthChecksPaginates(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	const total = 5
	want := make(map[string]int, total)
	for i := range total {
		out, err := client.CreateHealthCheck(ctx, &awsr53.CreateHealthCheckInput{
			CallerReference: aws.String(fmt.Sprintf("hc-ref-%d", i)),
			HealthCheckConfig: &r53types.HealthCheckConfig{
				IPAddress: aws.String(fmt.Sprintf("192.0.2.%d", i+1)),
				Port:      aws.Int32(80),
				Type:      r53types.HealthCheckTypeHttp,
			},
		})
		if err != nil {
			t.Fatalf("CreateHealthCheck: %v", err)
		}

		want[aws.ToString(out.HealthCheck.Id)] = 0
	}

	got := make(map[string]int, total)
	var marker *string
	for pages := 0; ; pages++ {
		if pages > pageWalkLimit {
			t.Fatal("ListHealthChecks did not terminate — IsTruncated never cleared")
		}

		out, err := client.ListHealthChecks(ctx, &awsr53.ListHealthChecksInput{
			MaxItems: aws.Int32(2),
			Marker:   marker,
		})
		if err != nil {
			t.Fatalf("ListHealthChecks: %v", err)
		}

		if n := len(out.HealthChecks); n > 2 {
			t.Fatalf("page returned %d checks, want <= MaxItems 2", n)
		}

		for i := range out.HealthChecks {
			got[aws.ToString(out.HealthChecks[i].Id)]++
		}

		if !out.IsTruncated {
			break
		}

		if aws.ToString(out.NextMarker) == "" {
			t.Fatal("truncated page has empty NextMarker")
		}

		marker = out.NextMarker
	}

	assertEachOnce(t, want, got)
}

// TestSDKListResourceRecordSetsIdentifierNoDupSkip creates a weighted record set
// whose siblings share one name+type but carry distinct SetIdentifiers, then
// paginates with a MaxItems that forces a page boundary inside that group. It
// asserts every sibling is returned exactly once — no duplicate (the old bug,
// which re-emitted the whole group) and no skip.
func TestSDKListResourceRecordSetsIdentifierNoDupSkip(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	zoneID := createZone(t, client, "weighted.com.", "wt-ref")

	const name = "app.weighted.com."
	setIDs := []string{"w1", "w2", "w3"}
	for i, sid := range setIDs {
		if _, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
			HostedZoneId: aws.String(zoneID),
			ChangeBatch: &r53types.ChangeBatch{
				Changes: []r53types.Change{{
					Action: r53types.ChangeActionCreate,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name:            aws.String(name),
						Type:            r53types.RRTypeA,
						SetIdentifier:   aws.String(sid),
						Weight:          aws.Int64(int64(i + 1)),
						TTL:             aws.Int64(60),
						ResourceRecords: []r53types.ResourceRecord{{Value: aws.String(fmt.Sprintf("192.0.2.%d", i+1))}},
					},
				}},
			},
		}); err != nil {
			t.Fatalf("ChangeResourceRecordSets(%s): %v", sid, err)
		}
	}

	// MaxItems 2 with the zone's SOA+NS ahead of the group guarantees the boundary
	// falls between the weighted siblings, exercising NextRecordIdentifier.
	paginator := awsr53.NewListResourceRecordSetsPaginator(client, &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		MaxItems:     aws.Int32(2),
	})

	seen := map[string]int{"w1": 0, "w2": 0, "w3": 0}
	for pages := 0; paginator.HasMorePages(); pages++ {
		if pages > pageWalkLimit {
			t.Fatal("ListResourceRecordSets did not terminate")
		}

		out, err := paginator.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for i := range out.ResourceRecordSets {
			if sid := aws.ToString(out.ResourceRecordSets[i].SetIdentifier); sid != "" {
				seen[sid]++
			}
		}
	}

	for _, sid := range setIDs {
		if seen[sid] != 1 {
			t.Errorf("weighted record %q returned %d times across pages, want exactly 1", sid, seen[sid])
		}
	}
}

// assertEachOnce fails the test unless got contains exactly the keys of want,
// each seen exactly once.
func assertEachOnce(t *testing.T, want, got map[string]int) {
	t.Helper()

	for id := range want {
		switch got[id] {
		case 1:
		case 0:
			t.Errorf("id %q was never returned across pages", id)
		default:
			t.Errorf("id %q returned %d times, want exactly 1", id, got[id])
		}
	}

	for id := range got {
		if _, ok := want[id]; !ok {
			t.Errorf("unexpected id %q returned", id)
		}
	}
}
