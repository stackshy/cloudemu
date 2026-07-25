package route53_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
)

// TestSDKListOrderingDeterministic locks the #259 ordering fix at the wire
// level: ListHostedZones must return the same sequence on every call. The
// pre-fix bug was random map iteration order, so repetition is the signal.
func TestSDKListOrderingDeterministic(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	names := []string{"zeta.com.", "alpha.com.", "mid.com.", "beta.com.", "omega.com."}
	for i, name := range names {
		if _, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
			Name:            aws.String(name),
			CallerReference: aws.String("ordering-ref-" + string(rune('a'+i))),
		}); err != nil {
			t.Fatalf("CreateHostedZone(%s): %v", name, err)
		}
	}

	list := func() []string {
		out, err := client.ListHostedZones(ctx, &awsr53.ListHostedZonesInput{})
		if err != nil {
			t.Fatalf("ListHostedZones: %v", err)
		}

		got := make([]string, 0, len(out.HostedZones))
		for i := range out.HostedZones {
			got = append(got, aws.ToString(out.HostedZones[i].Name))
		}

		return got
	}

	first := list()
	if len(first) != len(names) {
		t.Fatalf("ListHostedZones returned %d zones, want %d: %v", len(first), len(names), first)
	}

	for call := 2; call <= 5; call++ {
		got := list()
		if len(got) != len(first) {
			t.Fatalf("call %d: got %d zones, want %d", call, len(got), len(first))
		}

		for i := range first {
			if got[i] != first[i] {
				t.Fatalf("call %d: order diverged at index %d: got %v, first call %v", call, i, got, first)
			}
		}
	}
}
