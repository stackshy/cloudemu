package aws

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/fxamacker/cbor/v2"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
)

// authOffGolden holds the responses the wire server gave this request set
// before IAM authorization covered the query and REST protocols. With
// EnforceAuth off, every response must stay byte-for-byte the same.
const authOffGolden = "testdata/authz_authoff_golden.json"

// fixedAuthOffTime pins the emulator clock so timestamps in responses repeat.
var fixedAuthOffTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) //nolint:gochecknoglobals // fixed test instant

// volatile matches the per-call parts of a response (generated ids, request
// ids, timestamps) that differ between runs.
var volatile = regexp.MustCompile( //nolint:gochecknoglobals // compiled once for the golden comparison
	`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|` +
		`\b(sg|i|vol|asg|lt|eni|r|ami|subnet|vpc)-[0-9a-f]{8,17}\b|"id":"[a-z0-9]{10}"|"rootResourceId":"[a-z0-9]{10}"`)

type authOffCase struct {
	Name   string `json:"name"`
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// normalizeBody masks per-call values and renders a CBOR body as JSON, whose
// map keys are sorted, so the comparison is stable.
func normalizeBody(t *testing.T, body string) string {
	t.Helper()

	if !utf8.ValidString(body) {
		dm, err := cbor.DecOptions{DefaultMapType: reflect.TypeOf(map[string]any{})}.DecMode()
		if err != nil {
			t.Fatalf("cbor mode: %v", err)
		}

		var v any
		if err := dm.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("cbor body: %v", err)
		}

		raw, _ := json.Marshal(v)
		body = "cbor:" + string(raw)
	}

	return volatile.ReplaceAllString(body, "<v>")
}

func gzipped(t *testing.T, s string) string {
	t.Helper()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()

	return buf.String()
}

// TestAuthOffResponsesUnchanged replays a request set touching every handler
// this change edits (the CloudWatch op selection, the EC2 autoscaling table,
// the SageMaker runtime split) and the gate's protocol edges, with
// EnforceAuth off, and compares each response with the recorded one.
func TestAuthOffResponsesUnchanged(t *testing.T) {
	clock := config.NewFakeClock(fixedAuthOffTime)
	cloud := cloudemu.NewAWS(config.WithClock(clock))
	d := DriversFrom(cloud)
	d.Clock = clock

	ts := httptest.NewServer(New(d))
	defer ts.Close()

	creds := aws.Credentials{AccessKeyID: "AKIAANY", SecretAccessKey: "any"}
	cborBody, _ := cbor.Marshal(map[string]any{"Namespace": "Off", "MetricData": []any{map[string]any{"MetricName": "M", "Value": 1.0}}})
	cwQuery := "Action=PutMetricData&Version=2010-08-01&Namespace=Off&MetricData.member.1.MetricName=Q&MetricData.member.1.Value=2"
	smithy := map[string]string{"Smithy-Protocol": "rpc-v2-cbor"}

	reqs := []struct {
		name string
		rq   sreq
	}{
		{"cw cbor PutMetricData", sreq{path: "/service/GraniteServiceVersion20100801/operation/PutMetricData", ctype: "application/cbor",
			body: string(cborBody), service: "monitoring", header: smithy}},
		{"cw cbor with query Action", sreq{path: "/service/GraniteServiceVersion20100801/operation/ListMetrics?Action=DescribeAlarms",
			ctype: "application/cbor", body: string(cborBody), service: "monitoring", header: smithy}},
		{"cw cbor unknown op", sreq{path: "/service/GraniteServiceVersion20100801/operation/Nope", ctype: "application/cbor",
			service: "monitoring", header: smithy}},
		{"cw cbor no op", sreq{path: "/service/GraniteServiceVersion20100801/operation/", ctype: "application/cbor",
			service: "monitoring", header: smithy}},
		{"cw json DescribeAlarms", sreq{path: "/", ctype: "application/x-amz-json-1.0", body: `{}`, service: "monitoring",
			header: map[string]string{"X-Amz-Target": "GraniteServiceVersion20100801.DescribeAlarms"}}},
		{"cw json unknown op", sreq{path: "/", ctype: "application/x-amz-json-1.0", body: `{}`, service: "monitoring",
			header: map[string]string{"X-Amz-Target": "GraniteServiceVersion20100801.Nope"}}},
		{"cw query PutMetricData", form("monitoring", cwQuery)},
		{"cw query gzip PutMetricData", sreq{path: "/", ctype: formCT, body: gzipped(t, cwQuery), service: "monitoring",
			header: map[string]string{"Content-Encoding": "gzip"}}},
		{"cw query ListMetrics", form("monitoring", "Action=ListMetrics&Version=2010-08-01&Namespace=Off")},
		{"cw query unknown", form("monitoring", "Action=Nope&Version=2010-08-01")},
		{"cw query GET DescribeAlarms", sreq{method: http.MethodGet, path: "/?Action=DescribeAlarms&Version=2010-08-01", service: "monitoring"}},
		{"ec2 DescribeInstances", form("ec2", "Action=DescribeInstances&Version=2016-11-15")},
		{"ec2 unknown", form("ec2", "Action=Nope&Version=2016-11-15")},
		{"ec2 bad body", form("ec2", "Action=DescribeInstances&x=%zz")},
		{"as CreateAutoScalingGroup", form("autoscaling",
			"Action=CreateAutoScalingGroup&AutoScalingGroupName=g&MinSize=0&MaxSize=1&DesiredCapacity=1&LaunchConfigurationName=lc&AvailabilityZones.member.1=us-east-1a")},
		{"as DescribeAutoScalingGroups", form("autoscaling", "Action=DescribeAutoScalingGroups")},
		{"as UpdateAutoScalingGroup", form("autoscaling", "Action=UpdateAutoScalingGroup&AutoScalingGroupName=g&MaxSize=2")},
		{"as SetDesiredCapacity", form("autoscaling", "Action=SetDesiredCapacity&AutoScalingGroupName=g&DesiredCapacity=1")},
		{"as PutScalingPolicy", form("autoscaling",
			"Action=PutScalingPolicy&AutoScalingGroupName=g&PolicyName=p&ScalingAdjustment=1&AdjustmentType=ChangeInCapacity")},
		{"as ExecutePolicy", form("autoscaling", "Action=ExecutePolicy&AutoScalingGroupName=g&PolicyName=p")},
		{"as DeletePolicy (served by IAM)", form("autoscaling", "Action=DeletePolicy&AutoScalingGroupName=g&PolicyName=p")},
		{"as DeleteAutoScalingGroup", form("autoscaling", "Action=DeleteAutoScalingGroup&AutoScalingGroupName=g")},
		{"iam ListUsers", form("iam", "Action=ListUsers&Version=2010-05-08")},
		{"sts GetCallerIdentity", form("sts", "Action=GetCallerIdentity&Version=2011-06-15")},
		{"sns ListTopics", form("sns", "Action=ListTopics")},
		{"sagemaker runtime GET", sreq{method: http.MethodGet, path: "/endpoints/e/invocations", service: "sagemaker"}},
		{"sagemaker runtime missing endpoint", sreq{path: "/endpoints/e/invocations", ctype: "application/json", body: `{}`,
			service: "sagemaker"}},
		{"sagemaker feature store DELETE", sreq{method: http.MethodDelete, path: "/FeatureGroup/g?RecordIdentifierValueAsString=1",
			service: "sagemaker"}},
		{"sagemaker feature store POST", sreq{path: "/FeatureGroup/g", service: "sagemaker"}},
		{"sagemaker ListModels", sreq{path: "/", ctype: amzJSON11, body: `{}`, service: "sagemaker",
			header: map[string]string{"X-Amz-Target": "SageMaker.ListModels"}}},
		{"sagemaker unknown op", sreq{path: "/", ctype: amzJSON11, body: `{}`, service: "sagemaker",
			header: map[string]string{"X-Amz-Target": "SageMaker.Nope"}}},
		{"s3 mb", sreq{method: http.MethodPut, path: "/off-bucket", service: "s3"}},
		{"s3 ListBuckets", sreq{method: http.MethodGet, path: "/", service: "s3"}},
		{"lambda ListFunctions", sreq{method: http.MethodGet, path: lambdaPath, service: "lambda"}},
		{"route53 ListHostedZones", sreq{method: http.MethodGet, path: r53Path, service: r53Signed}},
		{"route53 GetHostedZone missing", sreq{method: http.MethodGet, path: r53Path + "/ZNONE", service: r53Signed}},
		{"route53 unknown sub-resource", sreq{method: http.MethodGet, path: r53Path + "/ZNONE/other", service: r53Signed}},
		{"route53 wrong method", sreq{method: http.MethodPut, path: r53Path + "/ZNONE/rrset", service: r53Signed}},
		{"route53 bad rrset body", sreq{path: r53Path + "/ZNONE/rrset", ctype: r53XMLCT, body: "<x", service: r53Signed}},
		{"route53 GetHealthCheck missing", sreq{method: http.MethodGet, path: "/2013-04-01/healthcheck/none", service: r53Signed}},
		{"route53 tags without id", sreq{method: http.MethodGet, path: "/2013-04-01/tags/hostedzone", service: r53Signed}},
		{"route53 GetHostedZoneCount", sreq{method: http.MethodGet, path: "/2013-04-01/hostedzonecount", service: r53Signed}},
		{"cloudfront ListDistributions", sreq{method: http.MethodGet, path: cfPath, service: cfSigned}},
		{"cloudfront GetDistribution missing", sreq{method: http.MethodGet, path: cfPath + "/ENONE", service: cfSigned}},
		{"cloudfront unknown path", sreq{method: http.MethodGet, path: cfPath + "/ENONE/other", service: cfSigned}},
		{"cloudfront wrong method", sreq{method: http.MethodPost, path: cfPath + "/ENONE/config", service: cfSigned}},
		{"cloudfront bad tagging op", sreq{path: "/2020-05-31/tagging?Operation=X", service: cfSigned}},
		{"unknown target", sreq{path: "/", ctype: amzJSON11, body: `{}`, service: "x",
			header: map[string]string{"X-Amz-Target": "NoSuchService_2020.Op"}}},
	}

	got := make([]authOffCase, 0, len(reqs))

	for _, r := range reqs {
		status, body := doSigned(t, ts, creds, r.rq)
		got = append(got, authOffCase{Name: r.name, Status: status, Body: normalizeBody(t, body)})
	}

	if os.Getenv("CLOUDEMU_UPDATE_AUTHOFF_GOLDEN") != "" {
		raw, _ := json.MarshalIndent(got, "", "  ")
		_ = os.MkdirAll(filepath.Dir(authOffGolden), 0o755)

		if err := os.WriteFile(authOffGolden, append(raw, '\n'), 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}

		return
	}

	raw, err := os.ReadFile(authOffGolden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	var want []authOffCase
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("golden: %v", err)
	}

	if len(want) != len(got) {
		t.Fatalf("golden has %d cases, test sends %d", len(want), len(got))
	}

	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s:\n got %d %q\nwant %d %q", got[i].Name, got[i].Status, got[i].Body, want[i].Status, want[i].Body)
		}
	}
}
