package route53_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// TestSDKZoneNameNormalizedToFQDN locks that a zone created without a trailing
// dot is stored and returned as an FQDN (with the dot), matching real Route 53.
func TestSDKZoneNameNormalizedToFQDN(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("nodot-zone.com"),
		CallerReference: aws.String("nd-1"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}

	if got := aws.ToString(created.HostedZone.Name); got != "nodot-zone.com." {
		t.Fatalf("CreateHostedZone name = %q, want nodot-zone.com.", got)
	}

	got, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: created.HostedZone.Id})
	if err != nil {
		t.Fatalf("GetHostedZone: %v", err)
	}
	if name := aws.ToString(got.HostedZone.Name); name != "nodot-zone.com." {
		t.Fatalf("GetHostedZone name = %q, want nodot-zone.com.", name)
	}
}

// TestSDKRecordNameNormalizedToFQDN locks that a record created without a
// trailing dot is stored and returned as an FQDN, and can be resolved with
// either the dotted or undotted name (TestDNSAnswer).
func TestSDKRecordNameNormalizedToFQDN(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("recnorm.com."),
		CallerReference: aws.String("rn-1"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	zoneID := aws.ToString(created.HostedZone.Id)

	// Create the record with NO trailing dot.
	if _, cerr := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{{
			Action: r53types.ChangeActionCreate,
			ResourceRecordSet: &r53types.ResourceRecordSet{
				Name:            aws.String("www.recnorm.com"),
				Type:            r53types.RRTypeA,
				TTL:             aws.Int64(300),
				ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.7")}},
			},
		}}},
	}); cerr != nil {
		t.Fatalf("ChangeResourceRecordSets(CREATE): %v", cerr)
	}

	// It is listed back with the trailing dot.
	sets, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}
	findRecordSet(t, sets.ResourceRecordSets, "www.recnorm.com.", r53types.RRTypeA)

	// Lookup works both ways: dotted and undotted query names resolve.
	for _, q := range []string{"www.recnorm.com.", "www.recnorm.com"} {
		out, aerr := client.TestDNSAnswer(ctx, &awsr53.TestDNSAnswerInput{
			HostedZoneId: aws.String(zoneID),
			RecordName:   aws.String(q),
			RecordType:   r53types.RRTypeA,
		})
		if aerr != nil {
			t.Fatalf("TestDNSAnswer(%q): %v", q, aerr)
		}
		if aws.ToString(out.ResponseCode) != "NOERROR" {
			t.Fatalf("TestDNSAnswer(%q) response = %q, want NOERROR", q, aws.ToString(out.ResponseCode))
		}
		if len(out.RecordData) != 1 || out.RecordData[0] != "192.0.2.7" {
			t.Fatalf("TestDNSAnswer(%q) data = %v, want [192.0.2.7]", q, out.RecordData)
		}
	}
}
