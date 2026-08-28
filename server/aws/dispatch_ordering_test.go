package aws_test

// Dispatch-ordering regression tests (Architecture Theme 2, #590).
//
// server.Server is a first-match-wins dispatcher: the FIRST registered handler
// whose Matches() returns true serves the request. Correctness therefore
// depends on a set of hand-ordered "register X before the catch-all Y"
// invariants documented only as prose comments in server/aws/aws.go New():
//
//   - RDS (and the other AWS query-protocol handlers: IAM, Redshift, ELBv2,
//     ElastiCache, SNS, STS, …) MUST register before the EC2 catch-all, whose
//     Matches() claims every form-encoded POST.
//   - GuardDuty, Lambda, Route 53, EFS, EKS (and the other REST handlers) MUST
//     register before the permissive S3 REST fallback.
//
// These tests drive the FULL production server (built via NewFromProvider so
// every handler is registered in the real order) with the exact ambiguous
// request shapes where both the specific handler and the broad catch-all
// match, and assert the more-specific handler wins by inspecting the response
// shape. If someone reorders the registrations (e.g. alphabetizing, or moving
// a catch-all earlier), these assertions fail instead of the routing silently
// degrading into runtime 404s / wrong responses.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fullAWSServer builds an httptest server over the complete AWS wire server
// with every handler registered in the real production order.
func fullAWSServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := awsserver.NewFromProvider(cloudemu.NewAWS())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

const formContentType = "application/x-www-form-urlencoded"

func doRequest(t *testing.T, ts *httptest.Server, method, path, body string, headers map[string]string) (int, string) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, ts.URL+path, rdr) //nolint:noctx // short-lived test request
	require.NoError(t, err)

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(raw)
}

// TestQueryProtocolHandlersWinBeforeEC2 covers the AWS query-protocol handlers
// that share the form-encoded-POST wire with the EC2 catch-all. Each request's
// Action is served only by the specific handler; EC2's fall-through would
// answer InvalidAction. The XML response root proves which handler served.
func TestQueryProtocolHandlersWinBeforeEC2(t *testing.T) {
	ts := fullAWSServer(t)

	cases := []struct {
		name   string
		action string
		// wantRoot is the XML response root the specific handler emits. The EC2
		// catch-all never produces it (it answers InvalidAction), so its
		// presence proves the specific handler won registration order.
		wantRoot string
	}{
		{"rds_before_ec2", "DescribeDBInstances", "DescribeDBInstancesResponse"},
		{"iam_before_ec2", "ListRoles", "ListRolesResponse"},
		{"redshift_before_ec2", "DescribeClusters", "DescribeClustersResponse"},
		{"elbv2_before_ec2", "DescribeLoadBalancers", "DescribeLoadBalancersResponse"},
		{"elasticache_before_ec2", "DescribeCacheClusters", "DescribeCacheClustersResponse"},
		{"sns_before_ec2", "ListTopics", "ListTopicsResponse"},
		{"sts_before_ec2", "GetCallerIdentity", "GetCallerIdentityResponse"},
		{"cloudformation_before_ec2", "ListStacks", "ListStacksResponse"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "Action=" + tc.action + "&Version=2015-01-01"
			status, resp := doRequest(t, ts, http.MethodPost, "/", body,
				map[string]string{"Content-Type": formContentType})

			assert.Equalf(t, http.StatusOK, status,
				"%s should be served by its specific handler, not the EC2 catch-all; body=%s", tc.action, resp)
			assert.Containsf(t, resp, tc.wantRoot,
				"expected %s response root %q (proves the specific handler won over EC2); got: %s",
				tc.action, tc.wantRoot, resp)
			assert.NotContainsf(t, resp, "InvalidAction",
				"%s was answered by the EC2 catch-all (InvalidAction) — registration order is broken", tc.action)
		})
	}
}

// TestRESTHandlersWinBeforeS3 covers the REST handlers rooted at their own path
// prefixes that would otherwise be swallowed by the permissive S3 REST fallback
// (whose Matches() claims essentially every non-query, non-JSON-RPC request).
// If S3 served these, it would treat the first path segment as a bucket name
// and answer a NoSuchBucket / XML error rather than the service shape below.
func TestRESTHandlersWinBeforeS3(t *testing.T) {
	ts := fullAWSServer(t)

	cases := []struct {
		name   string
		path   string
		marker string // substring only the specific handler's response contains
	}{
		{"guardduty_before_s3", "/detector", "detectorIds"},
		{"lambda_before_s3", "/2015-03-31/functions", "Functions"},
		{"route53_before_s3", "/2013-04-01/hostedzone", "ListHostedZonesResponse"},
		{"efs_before_s3", "/2015-02-01/file-systems", "FileSystems"},
		{"eks_before_s3", "/clusters", "clusters"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, resp := doRequest(t, ts, http.MethodGet, tc.path, "", nil)

			assert.Equalf(t, http.StatusOK, status,
				"GET %s should be served by its specific handler, not the S3 catch-all; body=%s", tc.path, resp)
			assert.Containsf(t, resp, tc.marker,
				"expected marker %q for GET %s (proves the specific handler won over S3); got: %s",
				tc.marker, tc.path, resp)
			assert.NotContainsf(t, resp, "NoSuchBucket",
				"GET %s was answered by the S3 catch-all (NoSuchBucket) — registration order is broken", tc.path)
		})
	}
}
