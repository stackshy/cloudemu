package cloudfront

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassify(t *testing.T) {
	const (
		dist = "/2020-05-31/distribution"
		tag  = "/2020-05-31/tagging"
		arn  = "arn:aws:cloudfront::123456789012:distribution/E1"
	)

	cases := []struct {
		method, target string
		op             opID
		id, inv, res   string
		fail           failure
	}{
		{method: "POST", target: dist, op: opCreateDistribution},
		{method: "POST", target: dist + "?WithTags", op: opCreateDistributionWithTags},
		{method: "GET", target: dist + "/", op: opListDistributions},
		{method: "DELETE", target: dist, op: opUnknown, fail: failMethod},
		{method: "GET", target: dist + "/E1", op: opGetDistribution, id: "E1"},
		{method: "DELETE", target: dist + "/E1", op: opDeleteDistribution, id: "E1"},
		{method: "PUT", target: dist + "/E1", op: opUnknown, id: "E1", fail: failMethod},
		{method: "GET", target: dist + "/E1/config", op: opGetDistributionConfig, id: "E1"},
		{method: "PUT", target: dist + "/E1/config", op: opUpdateDistribution, id: "E1"},
		{method: "POST", target: dist + "/E1/config", op: opUnknown, id: "E1", fail: failMethod},
		{method: "POST", target: dist + "/E1/invalidation", op: opCreateInvalidation, id: "E1"},
		{method: "GET", target: dist + "/E1/invalidation", op: opListInvalidations, id: "E1"},
		{method: "GET", target: dist + "/E1/invalidation/I1", op: opGetInvalidation, id: "E1", inv: "I1"},
		{method: "DELETE", target: dist + "/E1/invalidation/I1", op: opUnknown, id: "E1", inv: "I1", fail: failMethod},
		{method: "GET", target: dist + "/E1/other", op: opUnknown, id: "E1", fail: failPath},
		{method: "GET", target: dist + "/E1/config/x", op: opUnknown, id: "E1", fail: failPath},

		{method: "GET", target: tag + "?Resource=" + arn, op: opListTagsForResource, id: "E1", res: arn},
		{method: "POST", target: tag + "?Operation=Tag&Resource=" + arn, op: opTagResource, id: "E1", res: arn},
		{method: "POST", target: tag + "?Operation=Untag&Resource=" + arn, op: opUntagResource, id: "E1", res: arn},
		{method: "POST", target: tag + "?Operation=Other&Resource=" + arn, op: opUnknown, id: "E1", res: arn, fail: failTagging},
		{method: "DELETE", target: tag + "?Resource=" + arn, op: opUnknown, id: "E1", res: arn, fail: failTagging},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.target, http.NoBody)

		op, a := classify(r)
		if op != tc.op || a.id != tc.id || a.invalidationID != tc.inv || a.resource != tc.res ||
			(op == opUnknown && a.fail != tc.fail) {
			t.Errorf("%s %s: got op %q id %q inv %q res %q fail %d", tc.method, tc.target, op, a.id, a.invalidationID, a.resource, a.fail)
		}
	}
}

func TestParseDistributionARN(t *testing.T) {
	cases := []struct{ arn, account, id string }{
		{"arn:aws:cloudfront::123456789012:distribution/E1", "123456789012", "E1"},
		{"arn:aws-cn:cloudfront::999999999999:distribution/E2", "999999999999", "E2"},
		{"arn:aws:cloudfront:us-east-1:123456789012:distribution/E1", "", ""},
		{"arn:aws:cloudfront::123456789012:function/f", "", ""},
		{"arn:aws:cloudfront::123456789012:distribution/", "", ""},
		{"arn:aws:cloudfront::123456789012:distribution/a/b", "", ""},
		{"arn:aws:s3:::b/distribution/E1", "", ""},
		{"x/distribution/E1", "", ""},
		{"", "", ""},
	}

	for _, tc := range cases {
		if account, id := parseDistributionARN(tc.arn); account != tc.account || id != tc.id {
			t.Errorf("%q: got %q %q, want %q %q", tc.arn, account, id, tc.account, tc.id)
		}
	}
}
