package cloudwatch_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cwprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	cwserver "github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
)

var arnResultPattern = regexp.MustCompile(`<Arn>([^<]+)</Arn>`)

// TestQueryMetricStreamLifecycle drives the classic AWS query protocol
// (form-encoded POST, Action=..., XML response) — what aws-cli and
// terraform-provider-aws actually speak for CloudWatch — through the full
// metric-stream lifecycle: PutMetricStream, GetMetricStream, ListMetricStreams,
// Stop/StartMetricStreams, Tag/ListTags/UntagResource, and DeleteMetricStream.
//
// It specifically guards the PR's two query-protocol wire fixes:
//  1. empty-result operations (Delete/Start/Stop/Tag/Untag) must emit an
//     explicit <XxxResult/> element (via emptyQueryResult) so botocore /
//     aws-sdk-go can deserialize the response instead of erroring on a missing
//     result node.
//  2. GetMetricStream on an unknown name must return ResourceNotFoundException
//     (not the shorter ResourceNotFound the older alarm operations use), which
//     is what terraform's delete-waiter matches on to treat the resource as gone.
func TestQueryMetricStreamLifecycle(t *testing.T) {
	h := cwserver.New(cwprovider.New(config.NewOptions()))
	ts := httptest.NewServer(h)

	t.Cleanup(ts.Close)

	post := func(form url.Values) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", monitoringAuth)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()

		b, _ := io.ReadAll(resp.Body)

		return resp.StatusCode, string(b)
	}

	firehoseARN := "arn:aws:firehose:us-east-1:123456789098:deliverystream/MyFirehose"
	roleARN := "arn:aws:iam::123456789098:role/MyFirehoseWriteAccessRole"

	// PutMetricStream → 200 + PutMetricStreamResponse carrying an Arn.
	code, body := post(url.Values{
		"Action":                            {"PutMetricStream"},
		"Name":                              {"my-stream"},
		"FirehoseArn":                       {firehoseARN},
		"RoleArn":                           {roleARN},
		"OutputFormat":                      {"json"},
		"IncludeFilters.member.1.Namespace": {"AWS/EC2"},
		"IncludeFilters.member.1.MetricNames.member.1": {"CPUUtilization"},
		"Tags.member.1.Key":                            {"env"},
		"Tags.member.1.Value":                          {"prod"},
	})
	if code != http.StatusOK || !strings.Contains(body, "PutMetricStreamResponse") {
		t.Fatalf("PutMetricStream: code=%d body=%s", code, body)
	}

	m := arnResultPattern.FindStringSubmatch(body)
	if len(m) != 2 || m[1] == "" {
		t.Fatalf("PutMetricStream: no Arn in response: %s", body)
	}

	streamARN := m[1]

	// GetMetricStream → correct XML shape, State=running for a newly-created stream.
	code, body = post(url.Values{"Action": {"GetMetricStream"}, "Name": {"my-stream"}})
	if code != http.StatusOK {
		t.Fatalf("GetMetricStream: code=%d body=%s", code, body)
	}

	for _, want := range []string{
		"<Name>my-stream</Name>",
		"<FirehoseArn>" + firehoseARN + "</FirehoseArn>",
		"<RoleArn>" + roleARN + "</RoleArn>",
		"<State>running</State>",
		"<Namespace>AWS/EC2</Namespace>",
		"<GetMetricStreamResult>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GetMetricStream body missing %q: %s", want, body)
		}
	}

	// A second stream so ListMetricStreams has more than one entry.
	if code, body := post(url.Values{
		"Action":       {"PutMetricStream"},
		"Name":         {"other-stream"},
		"FirehoseArn":  {firehoseARN},
		"RoleArn":      {roleARN},
		"OutputFormat": {"json"},
	}); code != http.StatusOK {
		t.Fatalf("PutMetricStream other: code=%d body=%s", code, body)
	}

	code, body = post(url.Values{"Action": {"ListMetricStreams"}})
	if code != http.StatusOK || !strings.Contains(body, "ListMetricStreamsResult") {
		t.Fatalf("ListMetricStreams: code=%d body=%s", code, body)
	}
	if strings.Count(body, "<member>") != 2 {
		t.Fatalf("ListMetricStreams: want 2 entries, body=%s", body)
	}
	if !strings.Contains(body, "<Name>my-stream</Name>") || !strings.Contains(body, "<Name>other-stream</Name>") {
		t.Fatalf("ListMetricStreams: missing an entry, body=%s", body)
	}

	// StopMetricStreams — the empty-result response must deserialize: assert
	// the explicit <StopMetricStreamsResult> element (wire fix #1), not just a
	// bare ResponseMetadata envelope.
	code, body = post(url.Values{"Action": {"StopMetricStreams"}, "Names.member.1": {"my-stream"}})
	if code != http.StatusOK {
		t.Fatalf("StopMetricStreams: code=%d body=%s", code, body)
	}
	if !strings.Contains(body, "<StopMetricStreamsResult></StopMetricStreamsResult>") {
		t.Fatalf("StopMetricStreams: missing explicit empty result element (emptyQueryResult regression), body=%s", body)
	}

	code, body = post(url.Values{"Action": {"GetMetricStream"}, "Name": {"my-stream"}})
	if code != http.StatusOK || !strings.Contains(body, "<State>stopped</State>") {
		t.Fatalf("GetMetricStream after stop: code=%d body=%s", code, body)
	}

	// StartMetricStreams — same empty-result assertion.
	code, body = post(url.Values{"Action": {"StartMetricStreams"}, "Names.member.1": {"my-stream"}})
	if code != http.StatusOK {
		t.Fatalf("StartMetricStreams: code=%d body=%s", code, body)
	}
	if !strings.Contains(body, "<StartMetricStreamsResult></StartMetricStreamsResult>") {
		t.Fatalf("StartMetricStreams: missing explicit empty result element (emptyQueryResult regression), body=%s", body)
	}

	code, body = post(url.Values{"Action": {"GetMetricStream"}, "Name": {"my-stream"}})
	if code != http.StatusOK || !strings.Contains(body, "<State>running</State>") {
		t.Fatalf("GetMetricStream after start: code=%d body=%s", code, body)
	}

	// TagResource / ListTagsForResource / UntagResource on the metric-stream ARN.
	code, body = post(url.Values{
		"Action":              {"TagResource"},
		"ResourceARN":         {streamARN},
		"Tags.member.1.Key":   {"team"},
		"Tags.member.1.Value": {"sre"},
	})
	if code != http.StatusOK {
		t.Fatalf("TagResource: code=%d body=%s", code, body)
	}
	if !strings.Contains(body, "<TagResourceResult></TagResourceResult>") {
		t.Fatalf("TagResource: missing explicit empty result element, body=%s", body)
	}

	code, body = post(url.Values{"Action": {"ListTagsForResource"}, "ResourceARN": {streamARN}})
	if code != http.StatusOK || !strings.Contains(body, "ListTagsForResourceResult") {
		t.Fatalf("ListTagsForResource: code=%d body=%s", code, body)
	}
	if !strings.Contains(body, "<Key>env</Key>") || !strings.Contains(body, "<Key>team</Key>") {
		t.Fatalf("ListTagsForResource: want env and team tags, body=%s", body)
	}

	code, body = post(url.Values{"Action": {"UntagResource"}, "ResourceARN": {streamARN}, "TagKeys.member.1": {"env"}})
	if code != http.StatusOK {
		t.Fatalf("UntagResource: code=%d body=%s", code, body)
	}
	if !strings.Contains(body, "<UntagResourceResult></UntagResourceResult>") {
		t.Fatalf("UntagResource: missing explicit empty result element, body=%s", body)
	}

	code, body = post(url.Values{"Action": {"ListTagsForResource"}, "ResourceARN": {streamARN}})
	if code != http.StatusOK || strings.Contains(body, "<Key>env</Key>") || !strings.Contains(body, "<Key>team</Key>") {
		t.Fatalf("ListTagsForResource after untag: want only team tag, body=%s", body)
	}

	// DeleteMetricStream — empty-result assertion again.
	code, body = post(url.Values{"Action": {"DeleteMetricStream"}, "Name": {"my-stream"}})
	if code != http.StatusOK {
		t.Fatalf("DeleteMetricStream: code=%d body=%s", code, body)
	}
	if !strings.Contains(body, "<DeleteMetricStreamResult></DeleteMetricStreamResult>") {
		t.Fatalf("DeleteMetricStream: missing explicit empty result element (emptyQueryResult regression), body=%s", body)
	}

	// GetMetricStream on the now-deleted name — the exact wire fix: the error
	// code must be the full "ResourceNotFoundException", not the shorter
	// "ResourceNotFound" the older alarm operations return.
	code, body = post(url.Values{"Action": {"GetMetricStream"}, "Name": {"my-stream"}})
	if code != http.StatusNotFound {
		t.Fatalf("GetMetricStream after delete: code=%d, want 404, body=%s", code, body)
	}
	if !strings.Contains(body, "<Code>ResourceNotFoundException</Code>") {
		t.Fatalf("GetMetricStream after delete: want <Code>ResourceNotFoundException</Code>, body=%s", body)
	}
	if strings.Contains(body, "<Code>ResourceNotFound</Code>") {
		t.Fatalf("GetMetricStream after delete: got the shorter ResourceNotFound code, body=%s", body)
	}
}

// TestQueryPutMetricStreamValidation confirms the query-protocol path rejects
// IncludeFilters and ExcludeFilters supplied together with the real
// InvalidParameterValueException error name (not the shorter
// InvalidParameterValue the older alarm operations return).
func TestQueryPutMetricStreamValidation(t *testing.T) {
	h := cwserver.New(cwprovider.New(config.NewOptions()))
	ts := httptest.NewServer(h)

	t.Cleanup(ts.Close)

	post := func(form url.Values) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", monitoringAuth)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()

		b, _ := io.ReadAll(resp.Body)

		return resp.StatusCode, string(b)
	}

	code, body := post(url.Values{
		"Action":                            {"PutMetricStream"},
		"Name":                              {"both-filters"},
		"FirehoseArn":                       {"arn:aws:firehose:us-east-1:123456789098:deliverystream/MyFirehose"},
		"RoleArn":                           {"arn:aws:iam::123456789098:role/MyFirehoseWriteAccessRole"},
		"OutputFormat":                      {"json"},
		"IncludeFilters.member.1.Namespace": {"AWS/EC2"},
		"ExcludeFilters.member.1.Namespace": {"AWS/ELB"},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("PutMetricStream with both filters: code=%d, want 400, body=%s", code, body)
	}
	if !strings.Contains(body, "<Code>InvalidParameterValueException</Code>") {
		t.Fatalf("PutMetricStream with both filters: want <Code>InvalidParameterValueException</Code>, body=%s", body)
	}
}
