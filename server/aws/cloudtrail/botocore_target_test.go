package cloudtrail_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestBotocoreQualifiedTargetDispatches covers that the fully-qualified
// X-Amz-Target botocore (AWS CLI/boto3) sends,
// "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101.<Op>", dispatches to
// CloudTrail like the aws-sdk-go-v2 short form, through the full AWS
// server dispatcher.
func TestBotocoreQualifiedTargetDispatches(t *testing.T) {
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloudemu.NewAWS())))
	t.Cleanup(ts.Close)

	call := func(target, body string) map[string]any {
		t.Helper()

		req, err := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}

		req.Header.Set("Content-Type", "application/x-amz-json-1.1")
		req.Header.Set("X-Amz-Target", target)
		req.Header.Set("Authorization",
			"AWS4-HMAC-SHA256 Credential=test/20260101/us-east-1/cloudtrail/aws4_request, SignedHeaders=host, Signature=x")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		defer resp.Body.Close()

		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("%s: decode: %v", target, err)
		}

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d body %v", target, resp.StatusCode, out)
		}

		return out
	}

	const qualified = "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101."

	call(qualified+"CreateTrail", `{"Name":"boto-trail","S3BucketName":"logs"}`)

	for _, target := range []string{qualified + "DescribeTrails", "CloudTrail_20131101.DescribeTrails"} {
		out := call(target, `{}`)

		trails, _ := out["trailList"].([]any)
		if len(trails) != 1 {
			t.Fatalf("%s: trailList = %v, want the created trail", target, out["trailList"])
		}
	}
}
