package cloudwatch

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

const monitoringAuth = "AWS4-HMAC-SHA256 Credential=AKID/20260101/us-east-1/monitoring/aws4_request, SignedHeaders=host, Signature=0"

func cwRequest(path, ctype, body string, header map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", ctype)
	r.Header.Set("Authorization", monitoringAuth)

	for k, v := range header {
		r.Header.Set(k, v)
	}

	return r
}

func gzipBytes(s string) string {
	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()

	return buf.String()
}

func TestIAMChecksFollowsDispatch(t *testing.T) {
	h := New(nil)
	cbor := map[string]string{protocolHeader: protocolValue}
	target := map[string]string{jsonTargetHeader: jsonTargetPrefix + "PutMetricData"}

	cases := []struct {
		name string
		req  *http.Request
		want string
	}{
		{"cbor", cwRequest(pathPrefix+"GraniteServiceVersion20100801/operation/PutMetricData", "application/cbor", "", cbor),
			"cloudwatch:PutMetricData"},
		{"cbor with a query Action", cwRequest(pathPrefix+"GraniteServiceVersion20100801/operation/PutMetricData?Action=DescribeAlarms",
			"application/cbor", "", cbor), "cloudwatch:PutMetricData"},
		{"json target with a query Action", cwRequest("/?Action=DescribeAlarms", "application/x-amz-json-1.0", "{}", target),
			"cloudwatch:PutMetricData"},
		{"json target with a form body", cwRequest("/", formContentTypeForTest, "Action=DescribeAlarms", target),
			"cloudwatch:PutMetricData"},
		{"query", cwRequest("/", formContentTypeForTest, "Action=DescribeAlarms", nil), "cloudwatch:DescribeAlarms"},
		{"gzip query", cwRequest("/", formContentTypeForTest, gzipBytes("Action=PutMetricData"),
			map[string]string{"Content-Encoding": "gzip"}), "cloudwatch:PutMetricData"},
	}

	for _, tc := range cases {
		checks, ok := h.IAMChecks(tc.req, awsauthz.Scope{})
		if !ok || len(checks) != 1 || checks[0].Action != tc.want {
			t.Errorf("%s: got %+v ok=%v, want %s", tc.name, checks, ok, tc.want)
		}
	}

	for name, req := range map[string]*http.Request{
		"cbor without an op":        cwRequest(pathPrefix+"GraniteServiceVersion20100801/operation/", "application/cbor", "", cbor),
		"query without an Action":   cwRequest("/", formContentTypeForTest, "Version=1", nil),
		"query that does not parse": cwRequest("/", formContentTypeForTest, "Action=X&y=%zz", nil),
	} {
		if checks, ok := h.IAMChecks(req, awsauthz.Scope{}); ok {
			t.Errorf("%s: got %+v, want ok=false", name, checks)
		}
	}
}

func TestWriteAccessDeniedPerProtocol(t *testing.T) {
	h := New(nil)

	for name, tc := range map[string]struct {
		req   *http.Request
		ctype string
		code  string
	}{
		"query": {cwRequest("/", formContentTypeForTest, "Action=PutMetricData", nil), "text/xml", "<Code>AccessDenied</Code>"},
		"json": {cwRequest("/", "application/x-amz-json-1.0", "{}", map[string]string{jsonTargetHeader: jsonTargetPrefix + "PutMetricData"}),
			jsonContentType, "AccessDeniedException"},
		"cbor": {cwRequest(pathPrefix+"GraniteServiceVersion20100801/operation/PutMetricData", "application/cbor", "",
			map[string]string{protocolHeader: protocolValue}), "application/cbor", "AccessDeniedException"},
	} {
		rec := httptest.NewRecorder()
		h.WriteAccessDenied(rec, tc.req, "denied")

		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), tc.code) ||
			!strings.HasPrefix(rec.Header().Get("Content-Type"), tc.ctype) {
			t.Errorf("%s: %d %q %q", name, rec.Code, rec.Header().Get("Content-Type"), rec.Body)
		}
	}
}

const formContentTypeForTest = "application/x-www-form-urlencoded"
