package route53_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/smithy-go"
)

// TestSDKTagsHonourResourceType locks that the tagging API acts only on a
// resource of the type the request names: a hosted zone id sent as a health
// check, an id that names nothing and an unknown type are all rejected, and
// none of them leaves tags behind.
func TestSDKTagsHonourResourceType(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	zone, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("tagged.com."),
		CallerReference: aws.String("tag-type-ref"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}

	zoneID := strings.TrimPrefix(aws.ToString(zone.HostedZone.Id), "/hostedzone/")

	hc, err := client.CreateHealthCheck(ctx, &awsr53.CreateHealthCheckInput{
		CallerReference: aws.String("hc-ref"),
		HealthCheckConfig: &r53types.HealthCheckConfig{
			Type:             r53types.HealthCheckTypeHttp,
			IPAddress:        aws.String("192.0.2.1"),
			Port:             aws.Int32(80),
			ResourcePath:     aws.String("/"),
			RequestInterval:  aws.Int32(30),
			FailureThreshold: aws.Int32(3),
		},
	})
	if err != nil {
		t.Fatalf("CreateHealthCheck: %v", err)
	}

	hcID := aws.ToString(hc.HealthCheck.Id)

	tag := func(kind r53types.TagResourceType, id string) error {
		_, err := client.ChangeTagsForResource(ctx, &awsr53.ChangeTagsForResourceInput{
			ResourceType: kind,
			ResourceId:   aws.String(id),
			AddTags:      []r53types.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
		})

		return err
	}

	cases := []struct {
		name string
		kind r53types.TagResourceType
		id   string
		code string
	}{
		{"zone id as health check", r53types.TagResourceTypeHealthcheck, zoneID, "NoSuchHealthCheck"},
		{"health check id as zone", r53types.TagResourceTypeHostedzone, hcID, "NoSuchHostedZone"},
		{"missing zone", r53types.TagResourceTypeHostedzone, "ZMISSING", "NoSuchHostedZone"},
		{"unknown type", r53types.TagResourceType("bucket"), zoneID, "InvalidInput"},
	}

	for _, tc := range cases {
		var apiErr smithy.APIError
		if err := tag(tc.kind, tc.id); !errors.As(err, &apiErr) || apiErr.ErrorCode() != tc.code {
			t.Errorf("%s: got %v, want %s", tc.name, err, tc.code)
		}
	}

	for _, id := range []string{zoneID, hcID} {
		kind := r53types.TagResourceTypeHostedzone
		if id == hcID {
			kind = r53types.TagResourceTypeHealthcheck
		}

		out, err := client.ListTagsForResource(ctx, &awsr53.ListTagsForResourceInput{ResourceType: kind, ResourceId: aws.String(id)})
		if err != nil {
			t.Fatalf("ListTagsForResource %s: %v", id, err)
		}

		if n := len(out.ResourceTagSet.Tags); n != 0 {
			t.Errorf("%s has %d tags after rejected requests, want 0", id, n)
		}
	}

	if err := tag(r53types.TagResourceTypeHealthcheck, hcID); err != nil {
		t.Fatalf("tag health check: %v", err)
	}

	if err := tag(r53types.TagResourceTypeHostedzone, zoneID); err != nil {
		t.Fatalf("tag zone: %v", err)
	}
}
