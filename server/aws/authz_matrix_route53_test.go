package aws

import (
	"context"
	"net/http"
	"strings"
	"testing"

	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

const (
	r53Path   = "/2013-04-01/hostedzone"
	zoneARN   = "arn:aws:route53:::hostedzone/"
	r53XMLCT  = "application/xml"
	r53Signed = "route53"
)

func newZone(t *testing.T, cloud *awsprovider.Provider, name string) string {
	t.Helper()

	z, err := cloud.Route53.CreateZone(context.Background(), dnsdriver.ZoneConfig{Name: name})
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	return z.ID
}

func hasRecord(t *testing.T, cloud *awsprovider.Provider, zoneID, name, rtype string) bool {
	t.Helper()

	recs, err := cloud.Route53.ListRecords(context.Background(), zoneID)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}

	for _, r := range recs {
		if strings.EqualFold(strings.TrimSuffix(r.Name, "."), name) && r.Type == rtype {
			return true
		}
	}

	return false
}

func changeRecord(zoneID, name, rtype, value string) sreq {
	body := `<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><ChangeBatch><Changes>` +
		`<Change><Action>CREATE</Action><ResourceRecordSet><Name>` + name + `</Name><Type>` + rtype + `</Type><TTL>60</TTL>` +
		`<ResourceRecords><ResourceRecord><Value>` + value + `</Value></ResourceRecord></ResourceRecords>` +
		`</ResourceRecordSet></Change></Changes></ChangeBatch></ChangeResourceRecordSetsRequest>`

	return sreq{path: r53Path + "/" + zoneID + "/rrset", ctype: r53XMLCT, body: body, service: r53Signed}
}

// TestAuthzMatrixRoute53 covers hosted zone ARNs, the record set condition
// keys, a single Deny, and the ec2:DescribeVpcs a private zone needs.
func TestAuthzMatrixRoute53(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	z1 := newZone(t, cloud, "one.example.com")
	z2 := newZone(t, cloud, "two.example.com")

	txtOnly := userWithPolicy(t, cloud, "txtonly", policyDoc(
		stmtCond("Allow", "route53:ChangeResourceRecordSets", zoneARN+z1,
			`{"ForAllValues:StringEquals":{"route53:ChangeResourceRecordSetsRecordTypes":["TXT"]}}`),
		stmt("Allow", "route53:GetHostedZone", zoneARN+z1),
	))

	t.Run("TXT change allowed by the record type condition", func(t *testing.T) {
		status, body := doSigned(t, ts, txtOnly, changeRecord(z1, "txt.one.example.com", "TXT", `"hello"`))
		wantNotDenied(t, status, body)

		if !hasRecord(t, cloud, z1, "txt.one.example.com", "TXT") {
			t.Fatalf("TXT record not created: %d %s", status, body)
		}
	})

	t.Run("A change denied by the record type condition", func(t *testing.T) {
		status, body := doSigned(t, ts, txtOnly, changeRecord(z1, "a.one.example.com", "A", "192.0.2.1"))
		wantDenied(t, status, body, "route53:ChangeResourceRecordSets on resource: "+zoneARN+z1)

		if hasRecord(t, cloud, z1, "a.one.example.com", "A") {
			t.Fatal("a denied change created the A record")
		}
	})

	t.Run("another zone is out of scope", func(t *testing.T) {
		status, body := doSigned(t, ts, txtOnly, changeRecord(z2, "txt.two.example.com", "TXT", `"x"`))
		wantDenied(t, status, body, xmlAccessDenied)

		status, body = doSigned(t, ts, txtOnly, sreq{method: http.MethodGet, path: r53Path + "/" + z2, service: r53Signed})
		wantDenied(t, status, body, "route53:GetHostedZone on resource: "+zoneARN+z2)

		status, body = doSigned(t, ts, txtOnly, sreq{method: http.MethodGet, path: r53Path + "/" + z1, service: r53Signed})
		wantNotDenied(t, status, body)
	})

	t.Run("deny one zone delete", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "denyzone", policyDoc(stmt("Allow", "*", "*"),
			stmt("Deny", "route53:DeleteHostedZone", zoneARN+z1)))

		status, body := doSigned(t, ts, u, sreq{method: http.MethodDelete, path: r53Path + "/" + z1, service: r53Signed})
		wantDenied(t, status, body, "with an explicit deny")

		if _, err := cloud.Route53.GetZone(context.Background(), z1); err != nil {
			t.Fatal("a denied DeleteHostedZone removed the zone")
		}

		status, body = doSigned(t, ts, u, sreq{method: http.MethodDelete, path: r53Path + "/" + z2, service: r53Signed})
		wantNotDenied(t, status, body)
	})

	t.Run("private zone needs ec2:DescribeVpcs", func(t *testing.T) {
		create := sreq{path: r53Path, ctype: r53XMLCT, service: r53Signed,
			body: `<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><Name>priv.example.com</Name>` +
				`<CallerReference>priv</CallerReference><VPC><VPCId>vpc-1</VPCId><VPCRegion>us-east-1</VPCRegion></VPC>` +
				`<HostedZoneConfig><PrivateZone>true</PrivateZone></HostedZoneConfig></CreateHostedZoneRequest>`}

		r53Only := userWithPolicy(t, cloud, "r53only", allow("route53:CreateHostedZone"))
		status, body := doSigned(t, ts, r53Only, create)
		wantDenied(t, status, body, "ec2:DescribeVpcs")

		vpcScoped := userWithPolicy(t, cloud, "vpcscoped", policyDoc(
			stmtCond("Allow", "route53:CreateHostedZone", "*", `{"StringEquals":{"route53:VPCs":"VPCId=vpc-1,VPCRegion=us-east-1"}}`),
			stmt("Allow", "ec2:DescribeVpcs", "*")))

		status, body = doSigned(t, ts, vpcScoped, create)
		wantNotDenied(t, status, body)

		other := create
		other.body = strings.Replace(strings.Replace(create.body, "vpc-1", "vpc-2", 1), "<CallerReference>priv", "<CallerReference>priv2", 1)
		status, body = doSigned(t, ts, vpcScoped, other)
		wantDenied(t, status, body, "route53:CreateHostedZone")
	})

	t.Run("tags of an unknown resource type", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "tagger", allow("route53:ListTagsForResource"))
		status, body := doSigned(t, ts, u, sreq{method: http.MethodGet, path: "/2013-04-01/tags/bucket/" + z1, service: r53Signed})
		wantDenied(t, status, body, xmlAccessDenied)

		status, body = doSigned(t, ts, u, sreq{method: http.MethodGet, path: "/2013-04-01/tags/hostedzone/" + z1, service: r53Signed})
		wantNotDenied(t, status, body)
	})
}
