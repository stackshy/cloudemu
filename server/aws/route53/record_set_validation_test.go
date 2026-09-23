package route53_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

func plainRecord(name string, rtype r53types.RRType, values ...string) *r53types.ResourceRecordSet {
	rrs := make([]r53types.ResourceRecord, 0, len(values))
	for _, v := range values {
		rrs = append(rrs, r53types.ResourceRecord{Value: aws.String(v)})
	}

	return &r53types.ResourceRecordSet{
		Name: aws.String(name), Type: rtype, TTL: aws.Int64(300), ResourceRecords: rrs,
	}
}

func createChange(rr *r53types.ResourceRecordSet) r53types.Change {
	return r53types.Change{Action: r53types.ChangeActionCreate, ResourceRecordSet: rr}
}

// TestSDKRecordSetValidation checks each bad record set gets the code real
// Route 53 returns, and each good one is accepted.
func TestSDKRecordSetValidation(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()
	zoneID := createZone(t, client, "rrval.com.", "rrval-ref")

	alias := &r53types.AliasTarget{
		DNSName: aws.String("lb.example.com."), HostedZoneId: aws.String("Z35SXDOTRQ7X7K"),
	}
	aliasWithTTL := &r53types.ResourceRecordSet{
		Name: aws.String("mix.rrval.com."), Type: r53types.RRTypeA, AliasTarget: alias, TTL: aws.Int64(60),
	}

	tests := []struct {
		name string
		rr   *r53types.ResourceRecordSet
		want string
	}{
		{name: "A not an IP", rr: plainRecord("a.rrval.com.", r53types.RRTypeA, "not-an-ip"), want: "InvalidChangeBatch"},
		{name: "A given IPv6", rr: plainRecord("a6.rrval.com.", r53types.RRTypeA, "2001:db8::1"), want: "InvalidChangeBatch"},
		{name: "AAAA given IPv4", rr: plainRecord("q.rrval.com.", r53types.RRTypeAaaa, "10.0.0.1"), want: "InvalidChangeBatch"},
		{name: "CNAME two values", rr: plainRecord("c.rrval.com.", r53types.RRTypeCname, "a.com.", "b.com."), want: "InvalidChangeBatch"},
		{name: "alias with TTL", rr: aliasWithTTL, want: "InvalidInput"},
		{name: "no alias and no TTL", rr: &r53types.ResourceRecordSet{Name: aws.String("n.rrval.com."), Type: r53types.RRTypeA},
			want: "InvalidInput"},
		{name: "bogus type", rr: plainRecord("b.rrval.com.", "BOGUS", "x"), want: "InvalidInput"},
		{name: "valid A", rr: plainRecord("ok.rrval.com.", r53types.RRTypeA, "10.0.0.1")},
		{name: "valid AAAA", rr: plainRecord("ok6.rrval.com.", r53types.RRTypeAaaa, "2001:db8::1")},
		{name: "valid CNAME", rr: plainRecord("okc.rrval.com.", r53types.RRTypeCname, "a.com.")},
		{name: "valid alias", rr: &r53types.ResourceRecordSet{
			Name: aws.String("oka.rrval.com."), Type: r53types.RRTypeA, AliasTarget: alias,
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
				HostedZoneId: aws.String(zoneID),
				ChangeBatch:  &r53types.ChangeBatch{Changes: []r53types.Change{createChange(tc.rr)}},
			})

			assertRoute53Code(t, err, tc.want)
		})
	}
}

// assertRoute53Code checks err carries the wanted typed Route 53 error, or is
// nil when want is empty.
func assertRoute53Code(t *testing.T, err error, want string) {
	t.Helper()

	var badBatch *r53types.InvalidChangeBatch

	var badInput *r53types.InvalidInput

	switch want {
	case "":
		if err != nil {
			t.Fatalf("got %v, want success", err)
		}
	case "InvalidChangeBatch":
		if !errors.As(err, &badBatch) {
			t.Fatalf("got %v, want InvalidChangeBatch", err)
		}

		if len(badBatch.Messages) == 0 {
			t.Fatal("InvalidChangeBatch has no Messages")
		}
	case "InvalidInput":
		if !errors.As(err, &badInput) {
			t.Fatalf("got %v, want InvalidInput", err)
		}
	default:
		t.Fatalf("unknown code %q", want)
	}
}

// TestSDKRecordSetValidationIsAtomic checks a bad record later in a batch
// stops the earlier good record from being written.
func TestSDKRecordSetValidationIsAtomic(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()
	zoneID := createZone(t, client, "rratomic.com.", "rratomic-ref")

	_, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{
			createChange(plainRecord("good.rratomic.com.", r53types.RRTypeA, "10.0.0.1")),
			createChange(plainRecord("bad.rratomic.com.", r53types.RRTypeA, "not-an-ip")),
		}},
	})
	assertRoute53Code(t, err, "InvalidChangeBatch")

	out, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{HostedZoneId: aws.String(zoneID)})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}

	for _, rr := range out.ResourceRecordSets {
		if aws.ToString(rr.Name) == "good.rratomic.com." {
			t.Fatal("good record was written although the batch was rejected")
		}
	}
}

// TestSDKCreateHostedZoneDomainName checks a malformed zone name is
// InvalidDomainName, while punctuation Route 53 allows still passes.
func TestSDKCreateHostedZoneDomainName(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	_, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name: aws.String("not a domain!!"), CallerReference: aws.String("bad-dn"),
	})

	var badName *r53types.InvalidDomainName
	if !errors.As(err, &badName) {
		t.Fatalf("got %v, want InvalidDomainName", err)
	}

	createZone(t, client, "foo!!.com.", "punct-dn")
}

// TestSDKCalculatedHealthCheck checks a CALCULATED health check needs no
// endpoint, while an HTTP one still does.
func TestSDKCalculatedHealthCheck(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	_, err := client.CreateHealthCheck(ctx, &awsr53.CreateHealthCheckInput{
		CallerReference: aws.String("calc"),
		HealthCheckConfig: &r53types.HealthCheckConfig{
			Type: r53types.HealthCheckTypeCalculated, HealthThreshold: aws.Int32(1),
		},
	})
	if err != nil {
		t.Fatalf("CALCULATED health check: %v", err)
	}

	_, err = client.CreateHealthCheck(ctx, &awsr53.CreateHealthCheckInput{
		CallerReference:   aws.String("http-no-endpoint"),
		HealthCheckConfig: &r53types.HealthCheckConfig{Type: r53types.HealthCheckTypeHttp},
	})

	var badInput *r53types.InvalidInput
	if !errors.As(err, &badInput) {
		t.Fatalf("HTTP with no endpoint: got %v, want InvalidInput", err)
	}
}
