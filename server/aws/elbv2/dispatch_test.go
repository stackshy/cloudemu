package elbv2_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	elbv2pkg "github.com/stackshy/cloudemu/v2/server/aws/elbv2"
)

// TestDescribeTagsScopeGated guards the cross-cutting fix: elbv2 (registered
// before the EC2 query catch-all) must claim DescribeTags only for its own SigV4
// scope, so an EC2-scoped DescribeTags is not swallowed and returned empty.
func TestDescribeTagsScopeGated(t *testing.T) {
	h := elbv2pkg.New(cloudemu.NewAWS().ELB)
	body := "Action=DescribeTags&ResourceArns.member.1=arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/x"

	req := func(service string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Authorization",
			"AWS4-HMAC-SHA256 Credential=t/20260101/us-east-1/"+service+"/aws4_request, SignedHeaders=host, Signature=x")

		return r
	}

	if !h.Matches(req("elasticloadbalancing")) {
		t.Error("elbv2-scoped DescribeTags should be claimed by elbv2")
	}

	if h.Matches(req("ec2")) {
		t.Error("ec2-scoped DescribeTags must NOT be claimed by elbv2")
	}
}
