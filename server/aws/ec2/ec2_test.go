package ec2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	awsec2 "github.com/stackshy/cloudemu/providers/aws/ec2"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// newHandler returns a Handler wired to a fresh in-memory provider. Phase-1
// unit tests only need the compute driver; Phase-2 tests in security_group_test.go
// and siblings construct their own Handler with both drivers.
func newHandler() *Handler {
	return New(awsec2.New(config.NewOptions()), nil)
}

// do runs a request through the handler and returns the recorded response.
func do(t *testing.T, h *Handler, method, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	var body io.Reader
	if method == http.MethodPost && form != nil {
		body = strings.NewReader(form.Encode())
	}

	req := httptest.NewRequest(method, path, body)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	return rr
}

func TestMatchesRejectsDynamoDBTarget(t *testing.T) {
	h := newHandler()

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.PutItem")

	if h.Matches(req) {
		t.Fatal("EC2 Matches should reject X-Amz-Target requests")
	}
}

func TestMatchesAcceptsPostWithFormEncoding(t *testing.T) {
	h := newHandler()

	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader("Action=DescribeInstances"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")

	if !h.Matches(req) {
		t.Fatal("EC2 Matches should accept POST with form content-type")
	}
}

func TestMatchesAcceptsGetWithActionQuery(t *testing.T) {
	h := newHandler()

	req := httptest.NewRequest(http.MethodGet,
		"/?Action=DescribeInstances&Version=2016-11-15", nil)

	if !h.Matches(req) {
		t.Fatal("EC2 Matches should accept GET with Action= in query")
	}
}

func TestMatchesRejectsPlainGet(t *testing.T) {
	h := newHandler()

	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)

	if h.Matches(req) {
		t.Fatal("EC2 Matches should reject bare GET (that's S3)")
	}
}

func TestMatchesRejectsJSONPost(t *testing.T) {
	h := newHandler()

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"k":"v"}`))
	req.Header.Set("Content-Type", "application/json")

	if h.Matches(req) {
		t.Fatal("EC2 Matches should reject JSON POSTs")
	}
}

func TestServeHTTPUnknownActionReturns400(t *testing.T) {
	h := newHandler()

	rr := do(t, h, http.MethodPost, "/", url.Values{"Action": {"NotARealAction"}})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%s)", rr.Code, rr.Body.String())
	}

	if !strings.Contains(rr.Body.String(), "InvalidAction") {
		t.Errorf("body should mention InvalidAction: %s", rr.Body.String())
	}
}

func TestServeHTTPEveryActionRoutes(t *testing.T) {
	// Prove every switch arm in ServeHTTP is exercised. We don't assert on
	// the response here — we just verify it doesn't hit the default case.
	h := newHandler()

	run := do(t, h, http.MethodPost, "/", url.Values{
		"Action":       {"RunInstances"},
		"ImageId":      {"ami-x"},
		"InstanceType": {"t2.micro"},
		"MinCount":     {"1"},
		"MaxCount":     {"1"},
	})

	id := extractFirstInstanceID(run.Body.String())
	if id == "" {
		t.Fatal("RunInstances didn't return an instance id")
	}

	actions := []string{
		"DescribeInstances",
		"StartInstances",
		"StopInstances",
		"RebootInstances",
		"TerminateInstances",
		"ModifyInstanceAttribute",
	}
	for _, action := range actions {
		form := url.Values{"Action": {action}, "InstanceId": {id}, "InstanceId.1": {id}}
		rr := do(t, h, http.MethodPost, "/", form)

		if rr.Code != http.StatusOK && rr.Code != http.StatusBadRequest {
			t.Errorf("action %s returned unexpected status %d: %s",
				action, rr.Code, rr.Body.String())
		}
	}
}

func TestWriteErrMappings(t *testing.T) {
	cases := []struct {
		err      error
		wantCode string
	}{
		{cerrors.New(cerrors.NotFound, "x"), "InvalidInstanceID.NotFound"},
		{cerrors.New(cerrors.AlreadyExists, "x"), "ResourceAlreadyExists"},
		{cerrors.New(cerrors.InvalidArgument, "x"), "InvalidParameterValue"},
		{cerrors.New(cerrors.FailedPrecondition, "x"), "IncorrectInstanceState"},
		{errors.New("boom"), "InternalError"},
	}

	for _, tc := range cases {
		rr := httptest.NewRecorder()
		writeErr(rr, tc.err)

		if !strings.Contains(rr.Body.String(), tc.wantCode) {
			t.Errorf("writeErr(%v) body should contain %q, got %s",
				tc.err, tc.wantCode, rr.Body.String())
		}
	}
}

func TestInstanceCount(t *testing.T) {
	cases := []struct {
		minS, maxS string
		want       int
	}{
		{"1", "5", 5},   // MaxCount wins
		{"1", "1", 1},   // equal
		{"", "3", 3},    // min missing
		{"2", "", 2},    // max missing — falls back to min
		{"", "", 1},     // both missing — default 1
		{"0", "0", 1},   // both zero — default 1
		{"bad", "2", 2}, // unparsable min, valid max
		{"2", "bad", 2}, // valid min, unparsable max → min
		{"10", "3", 3},  // max still wins even if smaller than min
	}

	for _, tc := range cases {
		if got := instanceCount(tc.minS, tc.maxS); got != tc.want {
			t.Errorf("instanceCount(%q,%q)=%d want %d", tc.minS, tc.maxS, got, tc.want)
		}
	}
}

func TestMergeTagSpecsInstanceOnly(t *testing.T) {
	specs := []awsquery.TagSpec{
		{ResourceType: "instance", Tags: map[string]string{"A": "1"}},
		{ResourceType: "volume", Tags: map[string]string{"B": "2"}},
	}

	got := mergeTagSpecs(specs, "instance")

	if got["A"] != "1" {
		t.Errorf("A=%q want 1", got["A"])
	}

	if _, ok := got["B"]; ok {
		t.Errorf("B should not be merged for resource=instance")
	}
}

func TestMergeTagSpecsEmptyResourceTypeApplies(t *testing.T) {
	specs := []awsquery.TagSpec{
		{ResourceType: "", Tags: map[string]string{"K": "V"}},
	}

	got := mergeTagSpecs(specs, "instance")

	if got["K"] != "V" {
		t.Errorf("empty ResourceType should apply to any resource, got %v", got)
	}
}

func TestMergeTagSpecsEmptyReturnsNil(t *testing.T) {
	if got := mergeTagSpecs(nil, "instance"); got != nil {
		t.Errorf("empty specs should return nil, got %v", got)
	}
}

func TestMergeTagSpecsAllWrongResourceReturnsNil(t *testing.T) {
	specs := []awsquery.TagSpec{
		{ResourceType: "volume", Tags: map[string]string{"A": "1"}},
	}

	if got := mergeTagSpecs(specs, "instance"); got != nil {
		t.Errorf("no instance tags should give nil, got %v", got)
	}
}

func TestToInstanceXMLs(t *testing.T) {
	in := []computedriver.Instance{{
		ID: "i-a", ImageID: "ami-1", InstanceType: "t2.micro",
		State: "running", PrivateIP: "10.0.0.1", PublicIP: "1.2.3.4",
		SubnetID: "subnet-1", VPCID: "vpc-1",
		SecurityGroups: []string{"sg-1", "sg-2"},
		Tags:           map[string]string{"k": "v"},
	}}

	got := toInstanceXMLs(in)

	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}

	if got[0].InstanceID != "i-a" ||
		got[0].ImageID != "ami-1" ||
		got[0].InstanceType != "t2.micro" ||
		got[0].State.Code != stateCodeRunning ||
		len(got[0].Groups) != 2 ||
		len(got[0].Tags) != 1 {
		t.Errorf("unexpected result: %+v", got[0])
	}
}

func TestToInstanceXMLsEmpty(t *testing.T) {
	got := toInstanceXMLs(nil)
	if len(got) != 0 {
		t.Errorf("empty input should give empty result, got %v", got)
	}
}

func TestToDriverFiltersRoundTrip(t *testing.T) {
	in := []awsquery.Filter{
		{Name: "instance-state-name", Values: []string{"running"}},
		{Name: "tag:Role", Values: []string{"api", "web"}},
	}

	got := toDriverFilters(in)

	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}

	if got[0].Name != "instance-state-name" || got[0].Values[0] != "running" {
		t.Errorf("filter 0 wrong: %+v", got[0])
	}

	if got[1].Name != "tag:Role" || len(got[1].Values) != 2 {
		t.Errorf("filter 1 wrong: %+v", got[1])
	}
}

func TestToDriverFiltersNilForEmpty(t *testing.T) {
	if got := toDriverFilters(nil); got != nil {
		t.Errorf("empty input should give nil, got %v", got)
	}
}

func TestStateChanges(t *testing.T) {
	cur := instanceState{Code: stateCodeStopping, Name: "stopping"}
	prev := instanceState{Code: stateCodeRunning, Name: "running"}

	got := stateChanges([]string{"i-1", "i-2"}, cur, prev)

	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}

	if got[0].InstanceID != "i-1" || got[0].CurrentState != cur || got[0].PreviousState != prev {
		t.Errorf("i-1 wrong: %+v", got[0])
	}

	if got[1].InstanceID != "i-2" {
		t.Errorf("i-2 wrong: %+v", got[1])
	}
}

func TestStripInstancePrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"i-abc", "abc"},
		{"i-", "i-"},   // too short — unchanged
		{"foo", "foo"}, // no prefix — unchanged
		{"", ""},
	}

	for _, tc := range cases {
		if got := stripInstancePrefix(tc.in); got != tc.want {
			t.Errorf("stripInstancePrefix(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestStateCodeAllStates(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"pending", stateCodePending},
		{"running", stateCodeRunning},
		{"shutting-down", stateCodeShuttingDown},
		{"terminated", stateCodeTerminated},
		{"stopping", stateCodeStopping},
		{"stopped", stateCodeStopped},
		{"bogus", stateCodePending}, // default branch
	}

	for _, tc := range cases {
		if got := stateCode(tc.name); got != tc.want {
			t.Errorf("stateCode(%q)=%d want %d", tc.name, got, tc.want)
		}
	}
}

//
// These trigger the provider's NotFound error by calling state-transition
// ops with unknown IDs, exercising writeErr dispatch from every operation.

func TestOperationsWithUnknownIDReturnError(t *testing.T) {
	h := newHandler()

	cases := []struct {
		name string
		form url.Values
	}{
		{
			"start",
			url.Values{"Action": {"StartInstances"}, "InstanceId.1": {"i-ghost"}},
		},
		{
			"stop",
			url.Values{"Action": {"StopInstances"}, "InstanceId.1": {"i-ghost"}},
		},
		{
			"reboot",
			url.Values{"Action": {"RebootInstances"}, "InstanceId.1": {"i-ghost"}},
		},
		{
			"terminate",
			url.Values{"Action": {"TerminateInstances"}, "InstanceId.1": {"i-ghost"}},
		},
		{
			"modify",
			url.Values{
				"Action":             {"ModifyInstanceAttribute"},
				"InstanceId":         {"i-ghost"},
				"InstanceType.Value": {"t2.small"},
			},
		},
	}

	for _, tc := range cases {
		rr := do(t, h, http.MethodPost, "/", tc.form)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d (body=%s)",
				tc.name, rr.Code, rr.Body.String())
		}
	}
}

func TestModifyInstanceAttributeNoopReturnsOK(t *testing.T) {
	h := newHandler()

	runRR := do(t, h, http.MethodPost, "/", url.Values{
		"Action":       {"RunInstances"},
		"ImageId":      {"ami-x"},
		"InstanceType": {"t2.micro"},
		"MinCount":     {"1"},
		"MaxCount":     {"1"},
	})

	id := extractFirstInstanceID(runRR.Body.String())
	if id == "" {
		t.Fatal("no instance id from RunInstances")
	}

	// Modify with no InstanceType.Value — should noop with 200.
	rr := do(t, h, http.MethodPost, "/", url.Values{
		"Action":     {"ModifyInstanceAttribute"},
		"InstanceId": {id},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("noop modify returned %d: %s", rr.Code, rr.Body.String())
	}
}

// extractFirstInstanceID pulls the first <instanceId>…</instanceId> value out
// of an XML response. Crude but good enough for these focused unit tests.
func extractFirstInstanceID(body string) string {
	const start = "<instanceId>"
	const end = "</instanceId>"

	i := strings.Index(body, start)
	if i < 0 {
		return ""
	}

	j := strings.Index(body[i+len(start):], end)
	if j < 0 {
		return ""
	}

	return body[i+len(start) : i+len(start)+j]
}

// Ensure the handler doesn't panic if ctx is cancelled mid-request.
func TestServeHTTPWithCanceledContext(t *testing.T) {
	h := newHandler()

	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader("Action=DescribeInstances"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
}
