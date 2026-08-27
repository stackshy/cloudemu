package route53_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// TestSDKDeleteRequiresExactMatch locks that a DELETE must repeat the record
// set's exact TTL and values: a mismatched DELETE is rejected with
// InvalidChangeBatch (leaving the record intact), and an exact-match DELETE
// succeeds. This mirrors real Route 53's concurrent-modification guard.
func TestSDKDeleteRequiresExactMatch(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("delmatch.com."),
		CallerReference: aws.String("del-ref"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	zoneID := aws.ToString(created.HostedZone.Id)

	if _, cerr := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{{
			Action: r53types.ChangeActionCreate,
			ResourceRecordSet: &r53types.ResourceRecordSet{
				Name:            aws.String("host.delmatch.com."),
				Type:            r53types.RRTypeA,
				TTL:             aws.Int64(300),
				ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.50")}},
			},
		}}},
	}); cerr != nil {
		t.Fatalf("create record: %v", cerr)
	}

	// A DELETE with a mismatched TTL and value must be rejected.
	_, err = client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{{
			Action: r53types.ChangeActionDelete,
			ResourceRecordSet: &r53types.ResourceRecordSet{
				Name:            aws.String("host.delmatch.com."),
				Type:            r53types.RRTypeA,
				TTL:             aws.Int64(999),
				ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("10.0.0.99")}},
			},
		}}},
	})

	var badBatch *r53types.InvalidChangeBatch
	if !errors.As(err, &badBatch) {
		t.Fatalf("mismatched DELETE: got %v, want InvalidChangeBatch", err)
	}

	// The record must still be present after the rejected DELETE.
	sets, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}
	findRecordSet(t, sets.ResourceRecordSets, "host.delmatch.com.", r53types.RRTypeA)

	// An exact-match DELETE succeeds.
	if _, cerr := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{{
			Action: r53types.ChangeActionDelete,
			ResourceRecordSet: &r53types.ResourceRecordSet{
				Name:            aws.String("host.delmatch.com."),
				Type:            r53types.RRTypeA,
				TTL:             aws.Int64(300),
				ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.50")}},
			},
		}}},
	}); cerr != nil {
		t.Fatalf("exact-match DELETE should succeed, got: %v", cerr)
	}

	after, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets after delete: %v", err)
	}
	for i := range after.ResourceRecordSets {
		if aws.ToString(after.ResourceRecordSets[i].Name) == "host.delmatch.com." &&
			after.ResourceRecordSets[i].Type == r53types.RRTypeA {
			t.Fatal("record still present after exact-match DELETE")
		}
	}
}
