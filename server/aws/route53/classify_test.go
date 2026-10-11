package route53

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassify(t *testing.T) {
	const hz = "/2013-04-01/hostedzone"

	cases := []struct {
		method, target string
		op             opID
		id, tagType    string
		fail           failure
	}{
		{method: "POST", target: hz, op: opCreateHostedZone},
		{method: "GET", target: hz + "/", op: opListHostedZones},
		{method: "PUT", target: hz, op: opUnknown, fail: failMethod},
		{method: "GET", target: hz + "/Z1", op: opGetHostedZone, id: "Z1"},
		{method: "DELETE", target: hz + "/Z1", op: opDeleteHostedZone, id: "Z1"},
		{method: "POST", target: hz + "/Z1", op: opUpdateHostedZoneComment, id: "Z1"},
		{method: "PUT", target: hz + "/Z1", op: opUnknown, id: "Z1", fail: failMethod},
		{method: "POST", target: hz + "/Z1/rrset", op: opChangeResourceRecordSets, id: "Z1"},
		{method: "GET", target: hz + "/Z1/rrset/", op: opListResourceRecordSets, id: "Z1"},
		{method: "DELETE", target: hz + "/Z1/rrset", op: opUnknown, id: "Z1", fail: failMethod},
		{method: "POST", target: hz + "/Z1/associatevpc", op: opAssociateVPCWithHostedZone, id: "Z1"},
		{method: "GET", target: hz + "/Z1/associatevpc", op: opUnknown, id: "Z1", fail: failMethod},
		{method: "POST", target: hz + "/Z1/disassociatevpc", op: opDisassociateVPCFromHostedZone, id: "Z1"},
		{method: "GET", target: hz + "/Z1/other", op: opUnknown, id: "Z1", fail: failPath},
		{method: "GET", target: hz + "/Z1/rrset/x", op: opUnknown, id: "Z1", fail: failPath},

		{method: "GET", target: "/2013-04-01/change/C1", op: opGetChange, id: "C1"},
		{method: "GET", target: "/2013-04-01/change//change/C1", op: opGetChange, id: "C1"},
		{method: "POST", target: "/2013-04-01/change/C1", op: opUnknown, id: "C1", fail: failMethod},

		{method: "GET", target: "/2013-04-01/hostedzonecount", op: opGetHostedZoneCount},
		{method: "POST", target: "/2013-04-01/hostedzonecount", op: opUnknown, fail: failMethod},
		{method: "GET", target: "/2013-04-01/hostedzonesbyname", op: opListHostedZonesByName},
		{method: "GET", target: "/2013-04-01/hostedzonesbyvpc?vpcid=v&vpcregion=r", op: opListHostedZonesByVPC},
		{method: "GET", target: "/2013-04-01/testdnsanswer", op: opTestDNSAnswer},

		{method: "POST", target: "/2013-04-01/healthcheck", op: opCreateHealthCheck},
		{method: "GET", target: "/2013-04-01/healthcheck/", op: opListHealthChecks},
		{method: "DELETE", target: "/2013-04-01/healthcheck", op: opUnknown, fail: failMethod},
		{method: "GET", target: "/2013-04-01/healthcheck/h1", op: opGetHealthCheck, id: "h1"},
		{method: "POST", target: "/2013-04-01/healthcheck/h1", op: opUpdateHealthCheck, id: "h1"},
		{method: "DELETE", target: "/2013-04-01/healthcheck/h1", op: opDeleteHealthCheck, id: "h1"},
		{method: "PUT", target: "/2013-04-01/healthcheck/h1", op: opUnknown, id: "h1", fail: failMethod},

		{method: "POST", target: "/2013-04-01/tags/hostedzone/Z1", op: opChangeTagsForResource, id: "Z1", tagType: "hostedzone"},
		{method: "GET", target: "/2013-04-01/tags/healthcheck/h1", op: opListTagsForResource, id: "h1", tagType: "healthcheck"},
		{method: "DELETE", target: "/2013-04-01/tags/hostedzone/Z1", op: opUnknown, id: "Z1", tagType: "hostedzone", fail: failTags},
		{method: "GET", target: "/2013-04-01/tags/hostedzone", op: opUnknown, tagType: "hostedzone", fail: failTags},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.target, http.NoBody)

		op, a := classify(r)
		if op != tc.op || a.id != tc.id || a.tagType != tc.tagType || (op == opUnknown && a.fail != tc.fail) {
			t.Errorf("%s %s: got op %q id %q type %q fail %d, want %q %q %q %d",
				tc.method, tc.target, op, a.id, a.tagType, a.fail, tc.op, tc.id, tc.tagType, tc.fail)
		}
	}
}
