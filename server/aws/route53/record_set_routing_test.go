package route53_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// changeRecordSet applies a single-change batch, failing the test on error.
func changeRecordSet(
	t *testing.T, client *awsr53.Client, zoneID string,
	action r53types.ChangeAction, rr *r53types.ResourceRecordSet,
) {
	t.Helper()

	_, err := client.ChangeResourceRecordSets(context.Background(), &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{Action: action, ResourceRecordSet: rr}},
		},
	})
	if err != nil {
		t.Fatalf("ChangeResourceRecordSets(%s %s): %v", action, aws.ToString(rr.SetIdentifier), err)
	}
}

func createRoutingZone(t *testing.T, client *awsr53.Client, name string) string {
	t.Helper()

	created, err := client.CreateHostedZone(context.Background(), &awsr53.CreateHostedZoneInput{
		Name: aws.String(name), CallerReference: aws.String(name),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}

	return aws.ToString(created.HostedZone.Id)
}

// weightedSetIdentifiers returns the SetIdentifiers of all A record sets at name.
func weightedSetIdentifiers(
	t *testing.T, client *awsr53.Client, zoneID, name string,
) []string {
	t.Helper()

	sets, err := client.ListResourceRecordSets(context.Background(), &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}

	var ids []string

	for i := range sets.ResourceRecordSets {
		rr := &sets.ResourceRecordSets[i]
		if aws.ToString(rr.Name) == name && rr.Type == r53types.RRTypeA {
			ids = append(ids, aws.ToString(rr.SetIdentifier))
		}
	}

	return ids
}

// TestSDKWeightedDeleteBySetIdentifier pins that DELETE of one weighted record
// set removes ONLY that SetIdentifier — the sibling weighted records sharing the
// same name+type are untouched.
func TestSDKWeightedDeleteBySetIdentifier(t *testing.T) {
	client := newRoute53Client(t)
	zoneID := createRoutingZone(t, client, "wtd.com.")
	name := "app.wtd.com."

	for id, w := range map[string]int64{"blue": 10, "green": 20, "red": 30} {
		changeRecordSet(t, client, zoneID, r53types.ChangeActionCreate, &r53types.ResourceRecordSet{
			Name: aws.String(name), Type: r53types.RRTypeA, TTL: aws.Int64(60),
			SetIdentifier: aws.String(id), Weight: aws.Int64(w),
			ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
		})
	}

	if got := weightedSetIdentifiers(t, client, zoneID, name); len(got) != 3 {
		t.Fatalf("before delete = %v, want 3 weighted sets", got)
	}

	// Delete only SetIdentifier=green.
	changeRecordSet(t, client, zoneID, r53types.ChangeActionDelete, &r53types.ResourceRecordSet{
		Name: aws.String(name), Type: r53types.RRTypeA, TTL: aws.Int64(60),
		SetIdentifier: aws.String("green"), Weight: aws.Int64(20),
		ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
	})

	got := weightedSetIdentifiers(t, client, zoneID, name)
	if len(got) != 2 {
		t.Fatalf("after delete green = %v, want blue+red to survive", got)
	}

	for _, id := range got {
		if id == "green" {
			t.Fatalf("green still present after delete: %v", got)
		}
	}
}

// TestSDKAliasRecordRoundTrip pins that an ALIAS record (AliasTarget, no TTL, no
// ResourceRecords) is stored and returned intact.
func TestSDKAliasRecordRoundTrip(t *testing.T) {
	client := newRoute53Client(t)
	zoneID := createRoutingZone(t, client, "alias.com.")
	name := "cdn.alias.com."

	changeRecordSet(t, client, zoneID, r53types.ChangeActionCreate, &r53types.ResourceRecordSet{
		Name: aws.String(name), Type: r53types.RRTypeA,
		AliasTarget: &r53types.AliasTarget{
			DNSName:              aws.String("d123.cloudfront.net."),
			HostedZoneId:         aws.String("Z2FDTNDATAQYW2"),
			EvaluateTargetHealth: true,
		},
	})

	rr := findRecordSet(t, listRecordSets(t, client, zoneID), name, r53types.RRTypeA)
	if rr.AliasTarget == nil {
		t.Fatalf("alias record read back with nil AliasTarget: %+v", rr)
	}
	if got := aws.ToString(rr.AliasTarget.DNSName); got != "d123.cloudfront.net." {
		t.Fatalf("AliasTarget.DNSName = %q, want d123.cloudfront.net.", got)
	}
	if got := aws.ToString(rr.AliasTarget.HostedZoneId); got != "Z2FDTNDATAQYW2" {
		t.Fatalf("AliasTarget.HostedZoneId = %q, want Z2FDTNDATAQYW2", got)
	}
	if !rr.AliasTarget.EvaluateTargetHealth {
		t.Fatalf("AliasTarget.EvaluateTargetHealth = false, want true")
	}
	if rr.TTL != nil {
		t.Fatalf("alias record TTL = %v, want nil", rr.TTL)
	}
	if len(rr.ResourceRecords) != 0 {
		t.Fatalf("alias record ResourceRecords = %v, want empty", rr.ResourceRecords)
	}
}

// TestSDKRoutingPolicyRoundTrip pins that latency (Region), failover, and
// geolocation routing metadata survive a create/list round-trip.
func TestSDKRoutingPolicyRoundTrip(t *testing.T) {
	client := newRoute53Client(t)
	zoneID := createRoutingZone(t, client, "routing.com.")

	changeRecordSet(t, client, zoneID, r53types.ChangeActionCreate, &r53types.ResourceRecordSet{
		Name: aws.String("lat.routing.com."), Type: r53types.RRTypeA, TTL: aws.Int64(60),
		SetIdentifier:   aws.String("us-east"),
		Region:          r53types.ResourceRecordSetRegionUsEast1,
		ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.10")}},
	})
	changeRecordSet(t, client, zoneID, r53types.ChangeActionCreate, &r53types.ResourceRecordSet{
		Name: aws.String("fo.routing.com."), Type: r53types.RRTypeA, TTL: aws.Int64(60),
		SetIdentifier:   aws.String("primary"),
		Failover:        r53types.ResourceRecordSetFailoverPrimary,
		ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.20")}},
	})
	changeRecordSet(t, client, zoneID, r53types.ChangeActionCreate, &r53types.ResourceRecordSet{
		Name: aws.String("geo.routing.com."), Type: r53types.RRTypeA, TTL: aws.Int64(60),
		SetIdentifier:   aws.String("in"),
		GeoLocation:     &r53types.GeoLocation{CountryCode: aws.String("IN")},
		ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.30")}},
	})

	sets := listRecordSets(t, client, zoneID)

	lat := findRecordSet(t, sets, "lat.routing.com.", r53types.RRTypeA)
	if lat.Region != r53types.ResourceRecordSetRegionUsEast1 {
		t.Fatalf("latency Region = %q, want us-east-1", lat.Region)
	}

	fo := findRecordSet(t, sets, "fo.routing.com.", r53types.RRTypeA)
	if fo.Failover != r53types.ResourceRecordSetFailoverPrimary {
		t.Fatalf("Failover = %q, want PRIMARY", fo.Failover)
	}

	geo := findRecordSet(t, sets, "geo.routing.com.", r53types.RRTypeA)
	if geo.GeoLocation == nil || aws.ToString(geo.GeoLocation.CountryCode) != "IN" {
		t.Fatalf("GeoLocation = %+v, want CountryCode IN", geo.GeoLocation)
	}
}

// TestSDKWeightedRecordRequiresSetIdentifier pins that a CREATE of a weighted
// routing record set (Weight set) with no SetIdentifier is rejected as
// InvalidChangeBatch, matching real Route 53, and that the record is not stored.
func TestSDKWeightedRecordRequiresSetIdentifier(t *testing.T) {
	client := newRoute53Client(t)
	zoneID := createRoutingZone(t, client, "wtd-req.com.")
	name := "app.wtd-req.com."

	_, err := client.ChangeResourceRecordSets(context.Background(), &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionCreate,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name: aws.String(name), Type: r53types.RRTypeA, TTL: aws.Int64(60),
					Weight:          aws.Int64(10),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
				},
			}},
		},
	})

	var invalid *r53types.InvalidChangeBatch
	if !errors.As(err, &invalid) {
		t.Fatalf("weighted CREATE without SetIdentifier: got %v, want InvalidChangeBatch", err)
	}

	if got := weightedSetIdentifiers(t, client, zoneID, name); len(got) != 0 {
		t.Fatalf("record stored despite rejection: %v", got)
	}
}

// listRecordSets returns all record sets in a zone.
func listRecordSets(t *testing.T, client *awsr53.Client, zoneID string) []r53types.ResourceRecordSet {
	t.Helper()

	out, err := client.ListResourceRecordSets(context.Background(), &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}

	return out.ResourceRecordSets
}
