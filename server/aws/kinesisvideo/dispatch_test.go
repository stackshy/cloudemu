package kinesisvideo_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	kvsrv "github.com/stackshy/cloudemu/v2/server/aws/kinesisvideo"
)

func newHandler(t *testing.T) *kvsrv.Handler {
	t.Helper()

	return kvsrv.New(cloudemu.NewAWS().KinesisVideo)
}

func post(path, body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
}

func TestMatchesUniquePaths(t *testing.T) {
	h := newHandler(t)

	cases := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{"createStream", post("/createStream", "{}"), true},
		{"describeSignalingChannel", post("/describeSignalingChannel", "{}"), true},
		{"listStreams", post("/listStreams", "{}"), true},
		{"get rejected", httptest.NewRequest(http.MethodGet, "/createStream", nil), false},
		{"unknown path", post("/frobnicate", "{}"), false},
		{"bucket-like path", post("/my-bucket/key", "{}"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.Matches(tc.req); got != tc.want {
				t.Fatalf("Matches(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestMatchesSharedTagPathScopedByARN(t *testing.T) {
	h := newHandler(t)

	kvARN := `{"ResourceARN":"arn:aws:kinesisvideo:us-east-1:000000000000:channel/c/1700000000"}`
	spARN := `{"resourceArn":"arn:aws:savingsplans::000000000000:savingsplan/sp-1"}`

	if !h.Matches(post("/TagResource", kvARN)) {
		t.Fatal("Matches(/TagResource) with a kinesisvideo ARN = false, want true")
	}

	if h.Matches(post("/TagResource", spARN)) {
		t.Fatal("Matches(/TagResource) with a savingsplans ARN = true, want false (must fall through)")
	}
}

// TestMatchesRestoresBody proves peeking the body in Matches leaves it intact for
// the downstream ServeHTTP / next handler.
func TestMatchesRestoresBody(t *testing.T) {
	h := newHandler(t)
	body := `{"ResourceARN":"arn:aws:kinesisvideo:us-east-1:000000000000:channel/c/1700000000"}`
	r := post("/TagResource", body)

	_ = h.Matches(r)

	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}

	if string(got) != body {
		t.Fatalf("body after Matches = %q, want %q", got, body)
	}
}

// TestCoexistsWithSavingsPlans proves that when both handlers are registered,
// a /TagResource with a Savings Plans ARN is not swallowed by Kinesis Video.
func TestCoexistsWithSavingsPlans(t *testing.T) {
	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		KinesisVideo: cloud.KinesisVideo,
		SavingsPlans: true,
		AccountID:    "000000000000",
		Region:       "us-east-1",
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// A Savings Plans TagResource for a non-existent plan must reach Savings
	// Plans (a resource error), not Kinesis Video's InvalidResourceFormat.
	resp, err := http.Post(ts.URL+"/TagResource", "application/json",
		strings.NewReader(`{"resourceArn":"arn:aws:savingsplans::000000000000:savingsplan/sp-missing","tags":{"a":"b"}}`))
	if err != nil {
		t.Fatalf("POST /TagResource: %v", err)
	}
	defer resp.Body.Close()

	errType := resp.Header.Get("X-Amzn-Errortype")
	if strings.Contains(errType, "InvalidResourceFormat") {
		t.Fatalf("savingsplans TagResource was handled by kinesisvideo (errortype %q)", errType)
	}
}
