package route53_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// createZone is a small helper returning a new zone's id.
func createZone(t *testing.T, client *awsr53.Client, name, ref string) string {
	t.Helper()

	out, err := client.CreateHostedZone(context.Background(), &awsr53.CreateHostedZoneInput{
		Name:            aws.String(name),
		CallerReference: aws.String(ref),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone(%s): %v", name, err)
	}

	return aws.ToString(out.HostedZone.Id)
}

// TestSDKCreateHostedZoneDelegationSet proves the create response carries a
// four-name-server delegation set and the zone starts with SOA+NS records.
func TestSDKCreateHostedZoneDelegationSet(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	out, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("deleg.com."),
		CallerReference: aws.String("deleg-ref"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}

	if out.DelegationSet == nil || len(out.DelegationSet.NameServers) != 4 {
		t.Fatalf("DelegationSet = %+v, want 4 name servers", out.DelegationSet)
	}

	if aws.ToInt64(out.HostedZone.ResourceRecordSetCount) != 2 {
		t.Errorf("RRSetCount = %d, want 2 (SOA+NS)", aws.ToInt64(out.HostedZone.ResourceRecordSetCount))
	}

	// The zone id is opaque and uppercase, not a sequential "zone-N".
	id := aws.ToString(out.HostedZone.Id)
	if strings.Contains(id, "zone-") {
		t.Errorf("zone id %q is sequential, want an opaque Z... id", id)
	}

	// GetHostedZone also returns the delegation set.
	got, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(id)})
	if err != nil {
		t.Fatalf("GetHostedZone: %v", err)
	}

	if got.DelegationSet == nil || len(got.DelegationSet.NameServers) != 4 {
		t.Fatalf("GetHostedZone DelegationSet = %+v, want 4 name servers", got.DelegationSet)
	}
}

// TestSDKGetChange proves the change endpoint is routed and reports INSYNC so
// the ResourceRecordSetsChanged waiter completes.
func TestSDKGetChange(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	zoneID := createZone(t, client, "change.com.", "change-ref")

	changed, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionCreate,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            aws.String("a.change.com."),
					Type:            r53types.RRTypeA,
					TTL:             aws.Int64(60),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("1.2.3.4")}},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ChangeResourceRecordSets: %v", err)
	}

	changeID := aws.ToString(changed.ChangeInfo.Id)

	got, err := client.GetChange(ctx, &awsr53.GetChangeInput{Id: aws.String(changeID)})
	if err != nil {
		t.Fatalf("GetChange: %v", err)
	}

	if got.ChangeInfo.Status != r53types.ChangeStatusInsync {
		t.Errorf("GetChange status = %q, want INSYNC", got.ChangeInfo.Status)
	}
}

// TestSDKUniqueChangeIDs proves two changes get distinct change ids.
func TestSDKUniqueChangeIDs(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	zoneID := createZone(t, client, "uniq.com.", "uniq-ref")

	change := func(host string) string {
		out, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
			HostedZoneId: aws.String(zoneID),
			ChangeBatch: &r53types.ChangeBatch{
				Changes: []r53types.Change{{
					Action: r53types.ChangeActionCreate,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name:            aws.String(host),
						Type:            r53types.RRTypeA,
						TTL:             aws.Int64(60),
						ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("1.2.3.4")}},
					},
				}},
			},
		})
		if err != nil {
			t.Fatalf("ChangeResourceRecordSets(%s): %v", host, err)
		}

		return aws.ToString(out.ChangeInfo.Id)
	}

	if a, b := change("a.uniq.com."), change("b.uniq.com."); a == b {
		t.Errorf("two changes share change id %q; expected distinct ids", a)
	}
}

// TestSDKGetHostedZoneCount proves the count endpoint is routed.
func TestSDKGetHostedZoneCount(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	createZone(t, client, "count-a.com.", "count-a")
	createZone(t, client, "count-b.com.", "count-b")

	out, err := client.GetHostedZoneCount(ctx, &awsr53.GetHostedZoneCountInput{})
	if err != nil {
		t.Fatalf("GetHostedZoneCount: %v", err)
	}

	if aws.ToInt64(out.HostedZoneCount) != 2 {
		t.Errorf("HostedZoneCount = %d, want 2", aws.ToInt64(out.HostedZoneCount))
	}
}

// TestSDKListHostedZonesByName proves the by-name listing is routed and sorted.
func TestSDKListHostedZonesByName(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	createZone(t, client, "zeta.com.", "z")
	createZone(t, client, "alpha.com.", "a")

	out, err := client.ListHostedZonesByName(ctx, &awsr53.ListHostedZonesByNameInput{})
	if err != nil {
		t.Fatalf("ListHostedZonesByName: %v", err)
	}

	if len(out.HostedZones) != 2 {
		t.Fatalf("got %d zones, want 2", len(out.HostedZones))
	}

	if aws.ToString(out.HostedZones[0].Name) != "alpha.com." {
		t.Errorf("first zone = %q, want alpha.com. (sorted)", aws.ToString(out.HostedZones[0].Name))
	}
}

// TestSDKTestDNSAnswer proves the testdnsanswer endpoint resolves a record.
func TestSDKTestDNSAnswer(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	zoneID := createZone(t, client, "dnsanswer.com.", "dns-ref")

	if _, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionCreate,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            aws.String("host.dnsanswer.com."),
					Type:            r53types.RRTypeA,
					TTL:             aws.Int64(60),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("203.0.113.5")}},
				},
			}},
		},
	}); err != nil {
		t.Fatalf("ChangeResourceRecordSets: %v", err)
	}

	out, err := client.TestDNSAnswer(ctx, &awsr53.TestDNSAnswerInput{
		HostedZoneId: aws.String(zoneID),
		RecordName:   aws.String("host.dnsanswer.com."),
		RecordType:   r53types.RRTypeA,
	})
	if err != nil {
		t.Fatalf("TestDNSAnswer: %v", err)
	}

	if out.ResponseCode == nil || aws.ToString(out.ResponseCode) != "NOERROR" {
		t.Errorf("ResponseCode = %v, want NOERROR", out.ResponseCode)
	}

	if len(out.RecordData) != 1 || out.RecordData[0] != "203.0.113.5" {
		t.Errorf("RecordData = %v, want [203.0.113.5]", out.RecordData)
	}
}

// TestSDKListRecordSetsPaging proves MaxItems truncates and IsTruncated /
// NextRecordName are set.
func TestSDKListRecordSetsPaging(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	zoneID := createZone(t, client, "paging.com.", "page-ref")

	for _, host := range []string{"c.paging.com.", "a.paging.com.", "b.paging.com."} {
		if _, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
			HostedZoneId: aws.String(zoneID),
			ChangeBatch: &r53types.ChangeBatch{
				Changes: []r53types.Change{{
					Action: r53types.ChangeActionCreate,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name:            aws.String(host),
						Type:            r53types.RRTypeA,
						TTL:             aws.Int64(60),
						ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("1.1.1.1")}},
					},
				}},
			},
		}); err != nil {
			t.Fatalf("ChangeResourceRecordSets(%s): %v", host, err)
		}
	}

	out, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		MaxItems:     aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}

	if len(out.ResourceRecordSets) != 2 {
		t.Fatalf("got %d record sets, want 2 (MaxItems)", len(out.ResourceRecordSets))
	}

	if !out.IsTruncated {
		t.Error("IsTruncated = false, want true")
	}

	if aws.ToString(out.NextRecordName) == "" {
		t.Error("NextRecordName is empty on a truncated page")
	}

	// Records are returned in DNS name order.
	if aws.ToString(out.ResourceRecordSets[0].Name) > aws.ToString(out.ResourceRecordSets[1].Name) {
		t.Errorf("record sets not sorted: %q then %q",
			aws.ToString(out.ResourceRecordSets[0].Name), aws.ToString(out.ResourceRecordSets[1].Name))
	}
}
