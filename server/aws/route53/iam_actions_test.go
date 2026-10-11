package route53

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestIAMChecks(t *testing.T) {
	const (
		hz     = "/2013-04-01/hostedzone"
		zone   = "arn:aws:route53:::hostedzone/Z1"
		health = "arn:aws:route53:::healthcheck/h1"
	)

	privateZone := `<CreateHostedZoneRequest><Name>p.com</Name><CallerReference>r</CallerReference>` +
		`<VPC><VPCId>VPC-1</VPCId><VPCRegion>us-east-1</VPCRegion></VPC></CreateHostedZoneRequest>`
	vpcBody := func(root string) string {
		return `<` + root + `><VPC><VPCId>vpc-1</VPCId><VPCRegion>us-east-1</VPCRegion></VPC></` + root + `>`
	}

	type check struct{ action, resource string }

	cases := []struct {
		name, method, target, body string
		checks                     []check
		cond                       map[string]string
		unknown                    bool
	}{
		{name: "create public zone", method: "POST", target: hz,
			body:   `<CreateHostedZoneRequest><Name>a.com</Name></CreateHostedZoneRequest>`,
			checks: []check{{"route53:CreateHostedZone", "*"}}},
		{name: "create private zone", method: "POST", target: hz, body: privateZone,
			checks: []check{{"route53:CreateHostedZone", "*"}, {"ec2:DescribeVpcs", "*"}},
			cond:   map[string]string{condVPCs: "VPCId=vpc-1,VPCRegion=us-east-1"}},
		{name: "create with a bad body", method: "POST", target: hz, body: `<x`, unknown: true},
		{name: "list zones", method: "GET", target: hz, checks: []check{{"route53:ListHostedZones", "*"}}},
		{name: "get zone", method: "GET", target: hz + "/Z1", checks: []check{{"route53:GetHostedZone", zone}}},
		{name: "delete zone", method: "DELETE", target: hz + "/Z1", checks: []check{{"route53:DeleteHostedZone", zone}}},
		{name: "update comment", method: "POST", target: hz + "/Z1", checks: []check{{"route53:UpdateHostedZoneComment", zone}}},
		{name: "list records", method: "GET", target: hz + "/Z1/rrset", checks: []check{{"route53:ListResourceRecordSets", zone}}},
		{name: "associate", method: "POST", target: hz + "/Z1/associatevpc", body: vpcBody("AssociateVPCWithHostedZoneRequest"),
			checks: []check{{"route53:AssociateVPCWithHostedZone", zone}, {"ec2:DescribeVpcs", "*"}},
			cond:   map[string]string{condVPCs: "VPCId=vpc-1,VPCRegion=us-east-1"}},
		{name: "disassociate", method: "POST", target: hz + "/Z1/disassociatevpc", body: vpcBody("DisassociateVPCFromHostedZoneRequest"),
			checks: []check{{"route53:DisassociateVPCFromHostedZone", zone}},
			cond:   map[string]string{condVPCs: "VPCId=vpc-1,VPCRegion=us-east-1"}},
		{name: "list by vpc", method: "GET", target: "/2013-04-01/hostedzonesbyvpc?vpcid=vpc-1&vpcregion=us-east-1",
			checks: []check{{"route53:ListHostedZonesByVPC", "*"}},
			cond:   map[string]string{condVPCs: "VPCId=vpc-1,VPCRegion=us-east-1"}},
		{name: "get change", method: "GET", target: "/2013-04-01/change/C1", checks: []check{{"route53:GetChange", "arn:aws:route53:::change/C1"}}},
		{name: "zone count", method: "GET", target: "/2013-04-01/hostedzonecount", checks: []check{{"route53:GetHostedZoneCount", "*"}}},
		{name: "by name", method: "GET", target: "/2013-04-01/hostedzonesbyname", checks: []check{{"route53:ListHostedZonesByName", "*"}}},
		{name: "test answer", method: "GET", target: "/2013-04-01/testdnsanswer", checks: []check{{"route53:TestDNSAnswer", "*"}}},
		{name: "create health check", method: "POST", target: "/2013-04-01/healthcheck", checks: []check{{"route53:CreateHealthCheck", "*"}}},
		{name: "list health checks", method: "GET", target: "/2013-04-01/healthcheck", checks: []check{{"route53:ListHealthChecks", "*"}}},
		{name: "get health check", method: "GET", target: "/2013-04-01/healthcheck/h1", checks: []check{{"route53:GetHealthCheck", health}}},
		{name: "update health check", method: "POST", target: "/2013-04-01/healthcheck/h1", checks: []check{{"route53:UpdateHealthCheck", health}}},
		{name: "delete health check", method: "DELETE", target: "/2013-04-01/healthcheck/h1", checks: []check{{"route53:DeleteHealthCheck", health}}},
		{name: "tag zone", method: "POST", target: "/2013-04-01/tags/hostedzone/Z1", checks: []check{{"route53:ChangeTagsForResource", zone}}},
		{name: "list health check tags", method: "GET", target: "/2013-04-01/tags/healthcheck/h1",
			checks: []check{{"route53:ListTagsForResource", health}}},
		{name: "tags of an unknown type", method: "GET", target: "/2013-04-01/tags/bucket/Z1", unknown: true},
		{name: "unknown sub-resource", method: "GET", target: hz + "/Z1/other", unknown: true},
		{name: "wrong method", method: "PUT", target: hz + "/Z1", unknown: true},
	}

	h := New(nil)
	s := awsauthz.Scope{AccountID: "123456789012", Region: "eu-west-1"}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))

			checks, cond, ok := h.IAMChecksWithContext(r, s)
			if ok == tc.unknown {
				t.Fatalf("ok = %v, want %v", ok, !tc.unknown)
			}

			got := make([]check, 0, len(checks))
			for _, c := range checks {
				if c.Mode != awsauthz.Required {
					t.Errorf("%s mode %d, want Required", c.Action, c.Mode)
				}

				got = append(got, check{c.Action, c.Resource})
			}

			if !tc.unknown && !reflect.DeepEqual(got, tc.checks) {
				t.Errorf("checks = %v, want %v", got, tc.checks)
			}

			for k, v := range tc.cond {
				if cond[k] != v {
					t.Errorf("cond %s = %q, want %q", k, cond[k], v)
				}
			}
		})
	}
}

func TestIAMChecksRecordSetConditions(t *testing.T) {
	body := `<ChangeResourceRecordSetsRequest><ChangeBatch><Changes>` +
		`<Change><Action>CREATE</Action><ResourceRecordSet><Name>WWW.Example.com.</Name><Type>a</Type></ResourceRecordSet></Change>` +
		`<Change><Action>UPSERT</Action><ResourceRecordSet><Name>*.example.com</Name><Type>TXT</Type></ResourceRecordSet></Change>` +
		`<Change><Action>CREATE</Action><ResourceRecordSet><Name>www.example.com</Name><Type>A</Type></ResourceRecordSet></Change>` +
		`</Changes></ChangeBatch></ChangeResourceRecordSetsRequest>`

	r := httptest.NewRequest(http.MethodPost, "/2013-04-01/hostedzone/Z1/rrset", strings.NewReader(body))

	checks, cond, ok := New(nil).IAMChecksWithContext(r, awsauthz.Scope{AccountID: "123456789012"})
	if !ok || len(checks) != 1 || checks[0].Action != "route53:ChangeResourceRecordSets" ||
		checks[0].Resource != "arn:aws:route53:::hostedzone/Z1" {
		t.Fatalf("checks = %v, ok = %v", checks, ok)
	}

	sep := iamdriver.ConditionValueSeparator
	want := map[string]string{
		condRecordActions: "CREATE" + sep + "UPSERT",
		condRecordTypes:   "A" + sep + "TXT",
		condRecordNames:   "www.example.com" + sep + `\052.example.com`,
	}

	for k, v := range want {
		if cond[k] != v {
			t.Errorf("%s = %q, want %q", k, cond[k], v)
		}
	}

	if rest := readAll(t, r); rest != body {
		t.Error("the body was not put back for dispatch")
	}
}

func TestNormalizeRecordName(t *testing.T) {
	for in, want := range map[string]string{
		"Example.COM.":        "example.com",
		"*.example.com":       `\052.example.com`,
		`\052.example.com.`:   `\052.example.com`,
		"a_b-c.example.com":   "a_b-c.example.com",
		"sp ace.example.com":  `sp\040ace.example.com`,
		`back\slash.example`:  `back\134slash.example`,
		`\0.example.com`:      `\134` + "0.example.com",
		"under_score_1.x.y.z": "under_score_1.x.y.z",
	} {
		if got := normalizeRecordName(in); got != want {
			t.Errorf("normalizeRecordName(%q) = %q, want %q", in, got, want)
		}
	}
}

func readAll(t *testing.T, r *http.Request) string {
	t.Helper()

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return string(raw)
}
