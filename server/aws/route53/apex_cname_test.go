package route53_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// TestSDKApexCNAMERejected locks that a CNAME at the zone apex is rejected with
// InvalidChangeBatch, as real Route 53 does — the apex must use A/AAAA or an
// ALIAS, since it carries the SOA and NS records.
func TestSDKApexCNAMERejected(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("apex-cname.com."),
		CallerReference: aws.String("apex-ref"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	zoneID := aws.ToString(created.HostedZone.Id)

	_, err = client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{{
			Action: r53types.ChangeActionCreate,
			ResourceRecordSet: &r53types.ResourceRecordSet{
				Name:            aws.String("apex-cname.com."),
				Type:            r53types.RRTypeCname,
				TTL:             aws.Int64(300),
				ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("target.example.com.")}},
			},
		}}},
	})

	var badBatch *r53types.InvalidChangeBatch
	if !errors.As(err, &badBatch) {
		t.Fatalf("apex CNAME: got %v, want InvalidChangeBatch", err)
	}

	// A CNAME below the apex is fine.
	if _, cerr := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{{
			Action: r53types.ChangeActionCreate,
			ResourceRecordSet: &r53types.ResourceRecordSet{
				Name:            aws.String("www.apex-cname.com."),
				Type:            r53types.RRTypeCname,
				TTL:             aws.Int64(300),
				ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("target.example.com.")}},
			},
		}}},
	}); cerr != nil {
		t.Fatalf("non-apex CNAME should be accepted, got: %v", cerr)
	}
}
