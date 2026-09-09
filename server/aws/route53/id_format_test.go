package route53_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
)

// TestSDKHostedZoneIDIsPrefixed pins the two halves of the AWS wire contract
// that a regression could silently drop: HostedZone.Id carries the
// "/hostedzone/" prefix (as real AWS returns), while the pagination
// Marker/NextMarker stay bare (no prefix). Asserting the literal wire format
// here is what makes the fix regression-proof — comparing the value against
// itself would pass even if the prefix were removed.
func TestSDKHostedZoneIDIsPrefixed(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	out, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("id-format.example.com."),
		CallerReference: aws.String("id-format-ref-1"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}

	id := aws.ToString(out.HostedZone.Id)
	if !strings.HasPrefix(id, "/hostedzone/") {
		t.Errorf("HostedZone.Id = %q, want /hostedzone/ prefix (real AWS format)", id)
	}
	// The change id keeps its own /change/ prefix, not /hostedzone/.
	if ci := aws.ToString(out.ChangeInfo.Id); strings.Contains(ci, "/hostedzone/") {
		t.Errorf("ChangeInfo.Id = %q, must not carry /hostedzone/", ci)
	}

	if getOut, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: out.HostedZone.Id}); err != nil {
		t.Errorf("GetHostedZone by prefixed id %q: %v", id, err)
	} else if gid := aws.ToString(getOut.HostedZone.Id); !strings.HasPrefix(gid, "/hostedzone/") {
		t.Errorf("GetHostedZone.Id = %q, want /hostedzone/ prefix", gid)
	}
}

// TestSDKListHostedZonesMarkerIsBare pins that NextMarker is returned bare
// (no /hostedzone/ prefix), matching the AWS ListHostedZones response.
func TestSDKListHostedZonesMarkerIsBare(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	for _, n := range []string{"marker-a.example.com.", "marker-b.example.com."} {
		if _, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
			Name:            aws.String(n),
			CallerReference: aws.String("marker-ref-" + n),
		}); err != nil {
			t.Fatalf("CreateHostedZone %s: %v", n, err)
		}
	}

	out, err := client.ListHostedZones(ctx, &awsr53.ListHostedZonesInput{MaxItems: aws.Int32(1)})
	if err != nil {
		t.Fatalf("ListHostedZones: %v", err)
	}
	if !out.IsTruncated {
		t.Fatal("expected a truncated first page with MaxItems=1 and >1 zone")
	}
	if nm := aws.ToString(out.NextMarker); nm == "" || strings.Contains(nm, "/hostedzone/") {
		t.Errorf("NextMarker = %q, want a bare zone id with no /hostedzone/ prefix", nm)
	}
}
