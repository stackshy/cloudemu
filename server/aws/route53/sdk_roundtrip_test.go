package route53_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

func newRoute53Client(t *testing.T) *awsr53.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Route53: cloud.Route53})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsr53.NewFromConfig(cfg, func(o *awsr53.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKRoute53ZoneLifecycle(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("example.com."),
		CallerReference: aws.String("ref-1"),
		HostedZoneConfig: &r53types.HostedZoneConfig{
			Comment:     aws.String("test zone"),
			PrivateZone: false,
		},
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}

	if created.HostedZone == nil || aws.ToString(created.HostedZone.Name) != "example.com." {
		t.Fatalf("CreateHostedZone = %+v, want name example.com.", created.HostedZone)
	}

	// The caller's CallerReference must round-trip, not be replaced with an
	// internal id.
	if got := aws.ToString(created.HostedZone.CallerReference); got != "ref-1" {
		t.Fatalf("CallerReference = %q, want ref-1", got)
	}

	zoneID := aws.ToString(created.HostedZone.Id)
	if zoneID == "" {
		t.Fatal("CreateHostedZone returned empty zone id")
	}

	got, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(zoneID)})
	if err != nil {
		t.Fatalf("GetHostedZone: %v", err)
	}

	if aws.ToString(got.HostedZone.Name) != "example.com." {
		t.Fatalf("GetHostedZone name = %q, want example.com.", aws.ToString(got.HostedZone.Name))
	}

	list, err := client.ListHostedZones(ctx, &awsr53.ListHostedZonesInput{})
	if err != nil {
		t.Fatalf("ListHostedZones: %v", err)
	}

	if len(list.HostedZones) != 1 || aws.ToString(list.HostedZones[0].Name) != "example.com." {
		t.Fatalf("ListHostedZones = %+v, want one zone example.com.", list.HostedZones)
	}

	if _, err := client.DeleteHostedZone(ctx, &awsr53.DeleteHostedZoneInput{Id: aws.String(zoneID)}); err != nil {
		t.Fatalf("DeleteHostedZone: %v", err)
	}

	_, err = client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(zoneID)})

	var notFound *r53types.NoSuchHostedZone
	if !errors.As(err, &notFound) {
		t.Fatalf("GetHostedZone after delete: got %v, want NoSuchHostedZone", err)
	}
}

func TestSDKRoute53RecordSets(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("records.com."),
		CallerReference: aws.String("ref-2"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}

	zoneID := aws.ToString(created.HostedZone.Id)

	_, err = client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionCreate,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            aws.String("www.records.com."),
					Type:            r53types.RRTypeA,
					TTL:             aws.Int64(300),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ChangeResourceRecordSets(CREATE): %v", err)
	}

	sets, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}

	if len(sets.ResourceRecordSets) != 1 {
		t.Fatalf("got %d record sets, want 1: %+v", len(sets.ResourceRecordSets), sets.ResourceRecordSets)
	}

	rr := sets.ResourceRecordSets[0]
	if aws.ToString(rr.Name) != "www.records.com." || rr.Type != r53types.RRTypeA || aws.ToInt64(rr.TTL) != 300 {
		t.Fatalf("record set = %+v, want www.records.com. A 300", rr)
	}

	if len(rr.ResourceRecords) != 1 || aws.ToString(rr.ResourceRecords[0].Value) != "192.0.2.1" {
		t.Fatalf("record values = %+v, want [192.0.2.1]", rr.ResourceRecords)
	}

	// UPSERT changes the value in place.
	_, err = client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionUpsert,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            aws.String("www.records.com."),
					Type:            r53types.RRTypeA,
					TTL:             aws.Int64(600),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.2")}},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ChangeResourceRecordSets(UPSERT): %v", err)
	}

	sets, err = client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets after upsert: %v", err)
	}

	if len(sets.ResourceRecordSets) != 1 || aws.ToInt64(sets.ResourceRecordSets[0].TTL) != 600 {
		t.Fatalf("after upsert = %+v, want single record TTL 600", sets.ResourceRecordSets)
	}

	// DELETE removes the record.
	_, err = client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionDelete,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            aws.String("www.records.com."),
					Type:            r53types.RRTypeA,
					TTL:             aws.Int64(600),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.2")}},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ChangeResourceRecordSets(DELETE): %v", err)
	}

	sets, err = client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets after delete: %v", err)
	}

	if len(sets.ResourceRecordSets) != 0 {
		t.Fatalf("got %d record sets after delete, want 0: %+v", len(sets.ResourceRecordSets), sets.ResourceRecordSets)
	}
}

func TestSDKRoute53Errors(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	_, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String("zone-missing")})

	var notFound *r53types.NoSuchHostedZone
	if !errors.As(err, &notFound) {
		t.Fatalf("GetHostedZone(missing): got %v, want NoSuchHostedZone", err)
	}

	_, err = client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String("zone-missing"),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionCreate,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            aws.String("a.missing."),
					Type:            r53types.RRTypeA,
					TTL:             aws.Int64(60),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("1.1.1.1")}},
				},
			}},
		},
	})
	if err == nil {
		t.Fatal("ChangeResourceRecordSets(missing zone): want error, got nil")
	}

	// A record-level conflict on an existing zone must surface as
	// InvalidChangeBatch, not the zone-level HostedZoneAlreadyExists.
	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("dup.com."),
		CallerReference: aws.String("dup-ref"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}

	zoneID := aws.ToString(created.HostedZone.Id)
	rec := r53types.Change{
		Action: r53types.ChangeActionCreate,
		ResourceRecordSet: &r53types.ResourceRecordSet{
			Name:            aws.String("a.dup.com."),
			Type:            r53types.RRTypeA,
			TTL:             aws.Int64(60),
			ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("1.2.3.4")}},
		},
	}

	change := &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch:  &r53types.ChangeBatch{Changes: []r53types.Change{rec}},
	}
	if _, err = client.ChangeResourceRecordSets(ctx, change); err != nil {
		t.Fatalf("ChangeResourceRecordSets(create): %v", err)
	}

	// Duplicate CREATE of the same record.
	_, err = client.ChangeResourceRecordSets(ctx, change)

	var badBatch *r53types.InvalidChangeBatch
	if !errors.As(err, &badBatch) {
		t.Fatalf("duplicate record CREATE: got %v, want InvalidChangeBatch", err)
	}

	// DELETE of a record that doesn't exist, on an existing zone → also
	// InvalidChangeBatch (not NoSuchHostedZone).
	missDelete := &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{{
			Action: r53types.ChangeActionDelete,
			ResourceRecordSet: &r53types.ResourceRecordSet{
				Name:            aws.String("ghost.dup.com."),
				Type:            r53types.RRTypeA,
				TTL:             aws.Int64(60),
				ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("9.9.9.9")}},
			},
		}}},
	}

	_, err = client.ChangeResourceRecordSets(ctx, missDelete)
	if !errors.As(err, &badBatch) {
		t.Fatalf("delete missing record: got %v, want InvalidChangeBatch", err)
	}
}
