package s3

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMatchesAcceptsBucketGet(t *testing.T) {
	h := New(nil)

	req := httptest.NewRequest(http.MethodGet, "/my-bucket/key.txt", nil)

	if !h.Matches(req) {
		t.Fatal("S3 Matches should accept a standard bucket/key GET")
	}
}

func TestMatchesAcceptsListBucketsRoot(t *testing.T) {
	h := New(nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if !h.Matches(req) {
		t.Fatal("S3 Matches should accept bare GET for ListBuckets")
	}
}

func TestMatchesRejectsDynamoDBTarget(t *testing.T) {
	h := New(nil)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.PutItem")

	if h.Matches(req) {
		t.Fatal("S3 Matches should reject X-Amz-Target requests")
	}
}

func TestMatchesRejectsActionInQuery(t *testing.T) {
	h := New(nil)

	req := httptest.NewRequest(http.MethodGet, "/?Action=DescribeInstances", nil)

	if h.Matches(req) {
		t.Fatal("S3 Matches should reject Action= (that's EC2 GET)")
	}
}

// Regression test for the form-encoded POST exclusion.
// Without this guard, S3 would claim EC2 requests.
func TestMatchesRejectsFormEncodedPost(t *testing.T) {
	h := New(nil)

	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader("Action=RunInstances"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")

	if h.Matches(req) {
		t.Fatal("S3 Matches should reject form-encoded POSTs (that's EC2)")
	}
}

func TestMatchesAcceptsPostWithBinaryBody(t *testing.T) {
	// S3 does use POST for some operations (e.g. PostObject). A POST with a
	// non-form Content-Type should still be routed to S3.
	h := New(nil)

	req := httptest.NewRequest(http.MethodPost, "/bucket/key",
		strings.NewReader("binary"))
	req.Header.Set("Content-Type", "application/octet-stream")

	if !h.Matches(req) {
		t.Fatal("S3 Matches should accept binary POSTs")
	}
}
