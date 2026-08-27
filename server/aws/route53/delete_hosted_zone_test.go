package route53_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// TestSDKDeleteHostedZoneNotEmpty pins the non-empty guard: a zone holding a
// user record cannot be deleted (400 HostedZoneNotEmpty), and once that record
// is removed the same delete succeeds.
func TestSDKDeleteHostedZoneNotEmpty(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()
	zoneID := createRoutingZone(t, client, "guard.com.")

	changeRecordSet(t, client, zoneID, r53types.ChangeActionCreate, &r53types.ResourceRecordSet{
		Name: aws.String("www.guard.com."), Type: r53types.RRTypeA, TTL: aws.Int64(300),
		ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
	})

	_, err := client.DeleteHostedZone(ctx, &awsr53.DeleteHostedZoneInput{Id: aws.String(zoneID)})

	var notEmpty *r53types.HostedZoneNotEmpty
	if !errors.As(err, &notEmpty) {
		t.Fatalf("DeleteHostedZone(non-empty) = %v, want HostedZoneNotEmpty", err)
	}

	// The zone (and its record) must still be there.
	if _, gerr := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(zoneID)}); gerr != nil {
		t.Fatalf("zone should survive a rejected delete, GetHostedZone: %v", gerr)
	}

	// Remove the user record; only the default SOA + NS remain, so delete works.
	changeRecordSet(t, client, zoneID, r53types.ChangeActionDelete, &r53types.ResourceRecordSet{
		Name: aws.String("www.guard.com."), Type: r53types.RRTypeA, TTL: aws.Int64(300),
		ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
	})

	if _, err := client.DeleteHostedZone(ctx, &awsr53.DeleteHostedZoneInput{Id: aws.String(zoneID)}); err != nil {
		t.Fatalf("DeleteHostedZone(now empty): %v", err)
	}

	_, err = client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(zoneID)})

	var notFound *r53types.NoSuchHostedZone
	if !errors.As(err, &notFound) {
		t.Fatalf("GetHostedZone after delete = %v, want NoSuchHostedZone", err)
	}
}
