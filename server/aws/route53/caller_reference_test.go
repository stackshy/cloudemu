package route53_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
)

// TestSDKCallerReferencePersisted locks that the caller-supplied CallerReference
// is persisted and returned verbatim on GetHostedZone and ListHostedZones — the
// pre-fix bug returned the zone name instead.
func TestSDKCallerReferencePersisted(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("cref.com."),
		CallerReference: aws.String("my-idempotency-token-42"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	zoneID := aws.ToString(created.HostedZone.Id)

	got, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(zoneID)})
	if err != nil {
		t.Fatalf("GetHostedZone: %v", err)
	}
	if ref := aws.ToString(got.HostedZone.CallerReference); ref != "my-idempotency-token-42" {
		t.Fatalf("GetHostedZone CallerReference = %q, want my-idempotency-token-42", ref)
	}

	list, err := client.ListHostedZones(ctx, &awsr53.ListHostedZonesInput{})
	if err != nil {
		t.Fatalf("ListHostedZones: %v", err)
	}
	var found bool
	for i := range list.HostedZones {
		if aws.ToString(list.HostedZones[i].Id) == zoneID {
			found = true
			if ref := aws.ToString(list.HostedZones[i].CallerReference); ref != "my-idempotency-token-42" {
				t.Fatalf("ListHostedZones CallerReference = %q, want my-idempotency-token-42", ref)
			}
		}
	}
	if !found {
		t.Fatal("created zone not present in ListHostedZones")
	}
}
