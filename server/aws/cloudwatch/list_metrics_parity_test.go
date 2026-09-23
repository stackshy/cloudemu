package cloudwatch_test

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/stackshy/cloudemu/v2/config"
	cwprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	cwserver "github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// cwWire drives one CloudWatch protocol. Requests are written as SDK input
// types so one table can run on both the query and the CBOR protocol.
type cwWire struct {
	provider    *cwprovider.Mock
	put         func(t *testing.T, in *awscw.PutMetricDataInput)
	listMetrics func(t *testing.T, in *awscw.ListMetricsInput) *awscw.ListMetricsOutput
	getStats    func(t *testing.T, in *awscw.GetMetricStatisticsInput) *awscw.GetMetricStatisticsOutput
	putAlarm    func(t *testing.T, in *awscw.PutMetricAlarmInput)

	// These return the error code, or "" on success.
	getMetricData   func(t *testing.T, in *awscw.GetMetricDataInput) (*awscw.GetMetricDataOutput, string)
	alarmsForMetric func(t *testing.T, in *awscw.DescribeAlarmsForMetricInput) (*awscw.DescribeAlarmsForMetricOutput, string)
	listMetricsCode func(t *testing.T, in *awscw.ListMetricsInput) string
	alarmsCode      func(t *testing.T, in *awscw.DescribeAlarmsInput) string

	// tokenCode calls a paged list op with only a NextToken.
	tokenCode func(t *testing.T, op, token string) string
}

type cwProtocol struct {
	name  string
	build func(t *testing.T, ipam netdriver.IPAMMetrics) cwWire
}

func cwProtocols() []cwProtocol {
	return []cwProtocol{{name: "query", build: newQueryWire}, {name: "cbor", build: newCBORWire}}
}

// fakeIPAM is a fixed AWS/IPAM metrics source.
type fakeIPAM []netdriver.IpamMetric

func (f fakeIPAM) IpamMetrics(context.Context) []netdriver.IpamMetric { return f }

func newWireServer(t *testing.T, ipam netdriver.IPAMMetrics) (*cwprovider.Mock, *httptest.Server) {
	t.Helper()

	p := cwprovider.New(config.NewOptions())
	h := cwserver.New(p)
	h.SetIPAMMetrics(ipam)

	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	return p, ts
}

func newCBORWire(t *testing.T, ipam netdriver.IPAMMetrics) cwWire {
	t.Helper()

	p, ts := newWireServer(t, ipam)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	c := awscw.NewFromConfig(cfg, func(o *awscw.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	return cwWire{
		provider: p,
		put: func(t *testing.T, in *awscw.PutMetricDataInput) {
			t.Helper()

			if _, err := c.PutMetricData(ctx, in); err != nil {
				t.Fatalf("PutMetricData: %v", err)
			}
		},
		listMetrics: func(t *testing.T, in *awscw.ListMetricsInput) *awscw.ListMetricsOutput {
			t.Helper()

			out, err := c.ListMetrics(ctx, in)
			if err != nil {
				t.Fatalf("ListMetrics: %v", err)
			}

			return out
		},
		getStats: func(t *testing.T, in *awscw.GetMetricStatisticsInput) *awscw.GetMetricStatisticsOutput {
			t.Helper()

			out, err := c.GetMetricStatistics(ctx, in)
			if err != nil {
				t.Fatalf("GetMetricStatistics: %v", err)
			}

			return out
		},
		putAlarm: func(t *testing.T, in *awscw.PutMetricAlarmInput) {
			t.Helper()

			if _, err := c.PutMetricAlarm(ctx, in); err != nil {
				t.Fatalf("PutMetricAlarm: %v", err)
			}
		},
		getMetricData: func(t *testing.T, in *awscw.GetMetricDataInput) (*awscw.GetMetricDataOutput, string) {
			t.Helper()

			out, err := c.GetMetricData(ctx, in)

			return out, sdkErrCode(t, err)
		},
		alarmsForMetric: func(t *testing.T, in *awscw.DescribeAlarmsForMetricInput) (*awscw.DescribeAlarmsForMetricOutput, string) {
			t.Helper()

			out, err := c.DescribeAlarmsForMetric(ctx, in)

			return out, sdkErrCode(t, err)
		},
		listMetricsCode: func(t *testing.T, in *awscw.ListMetricsInput) string {
			t.Helper()

			_, err := c.ListMetrics(ctx, in)

			return sdkErrCode(t, err)
		},
		alarmsCode: func(t *testing.T, in *awscw.DescribeAlarmsInput) string {
			t.Helper()

			_, err := c.DescribeAlarms(ctx, in)

			return sdkErrCode(t, err)
		},
		tokenCode: func(t *testing.T, op, token string) string {
			t.Helper()

			var err error

			switch op {
			case "DescribeAlarmHistory":
				_, err = c.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{NextToken: aws.String(token)})
			case "ListMetricStreams":
				_, err = c.ListMetricStreams(ctx, &awscw.ListMetricStreamsInput{NextToken: aws.String(token)})
			case "ListDashboards":
				_, err = c.ListDashboards(ctx, &awscw.ListDashboardsInput{NextToken: aws.String(token)})
			default:
				t.Fatalf("tokenCode: unknown op %s", op)
			}

			return sdkErrCode(t, err)
		},
	}
}

func newQueryWire(t *testing.T, ipam netdriver.IPAMMetrics) cwWire {
	t.Helper()

	p, ts := newWireServer(t, ipam)

	// postCode returns the error code, or "" after decoding a 200 into out.
	postCode := func(t *testing.T, form url.Values, out any) string {
		t.Helper()

		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", monitoringAuth)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			var e struct {
				Code string `xml:"Error>Code"`
			}

			_ = xml.Unmarshal(body, &e)
			if e.Code == "" {
				t.Fatalf("%s: status %d body %s", form.Get("Action"), resp.StatusCode, body)
			}

			return e.Code
		}

		if out != nil {
			if err := xml.Unmarshal(body, out); err != nil {
				t.Fatalf("%s: decode %v body %s", form.Get("Action"), err, body)
			}
		}

		return ""
	}

	post := func(t *testing.T, form url.Values, out any) {
		t.Helper()

		if code := postCode(t, form, out); code != "" {
			t.Fatalf("%s: error %s", form.Get("Action"), code)
		}
	}

	return cwWire{
		provider: p,
		put: func(t *testing.T, in *awscw.PutMetricDataInput) {
			t.Helper()
			post(t, putMetricDataForm(in), nil)
		},
		listMetrics: func(t *testing.T, in *awscw.ListMetricsInput) *awscw.ListMetricsOutput {
			t.Helper()

			var x listMetricsXML
			post(t, listMetricsForm(in), &x)

			return x.toSDK()
		},
		getStats: func(t *testing.T, in *awscw.GetMetricStatisticsInput) *awscw.GetMetricStatisticsOutput {
			t.Helper()

			var x getStatsXML
			post(t, getStatsForm(in), &x)

			return x.toSDK()
		},
		putAlarm: func(t *testing.T, in *awscw.PutMetricAlarmInput) {
			t.Helper()
			post(t, putAlarmForm(in), nil)
		},
		getMetricData: func(t *testing.T, in *awscw.GetMetricDataInput) (*awscw.GetMetricDataOutput, string) {
			t.Helper()

			var x getMetricDataXML
			code := postCode(t, getMetricDataForm(in), &x)

			return x.toSDK(), code
		},
		alarmsForMetric: func(t *testing.T, in *awscw.DescribeAlarmsForMetricInput) (*awscw.DescribeAlarmsForMetricOutput, string) {
			t.Helper()

			var x alarmsForMetricXML
			code := postCode(t, alarmsForMetricForm(in), &x)

			return x.toSDK(), code
		},
		listMetricsCode: func(t *testing.T, in *awscw.ListMetricsInput) string {
			t.Helper()

			return postCode(t, listMetricsForm(in), nil)
		},
		alarmsCode: func(t *testing.T, in *awscw.DescribeAlarmsInput) string {
			t.Helper()

			form := url.Values{"Action": {"DescribeAlarms"}}
			if in.NextToken != nil {
				form.Set("NextToken", *in.NextToken)
			}

			return postCode(t, form, nil)
		},
		tokenCode: func(t *testing.T, op, token string) string {
			t.Helper()

			return postCode(t, url.Values{"Action": {op}, "NextToken": {token}}, nil)
		},
	}
}

func addDimensionsForm(form url.Values, dims []cwtypes.Dimension) {
	for i, d := range dims {
		p := "Dimensions.member." + strconv.Itoa(i+1) + "."
		form.Set(p+"Name", aws.ToString(d.Name))
		form.Set(p+"Value", aws.ToString(d.Value))
	}
}

func putMetricDataForm(in *awscw.PutMetricDataInput) url.Values {
	form := url.Values{"Action": {"PutMetricData"}, "Namespace": {aws.ToString(in.Namespace)}}

	for i, d := range in.MetricData {
		p := "MetricData.member." + strconv.Itoa(i+1) + "."
		form.Set(p+"MetricName", aws.ToString(d.MetricName))
		form.Set(p+"Value", strconv.FormatFloat(aws.ToFloat64(d.Value), 'g', -1, 64))

		if d.Timestamp != nil {
			form.Set(p+"Timestamp", d.Timestamp.UTC().Format(time.RFC3339))
		}

		for j, dim := range d.Dimensions {
			dp := p + "Dimensions.member." + strconv.Itoa(j+1) + "."
			form.Set(dp+"Name", aws.ToString(dim.Name))
			form.Set(dp+"Value", aws.ToString(dim.Value))
		}
	}

	return form
}

func listMetricsForm(in *awscw.ListMetricsInput) url.Values {
	form := url.Values{"Action": {"ListMetrics"}}

	if in.Namespace != nil {
		form.Set("Namespace", *in.Namespace)
	}

	if in.MetricName != nil {
		form.Set("MetricName", *in.MetricName)
	}

	if in.NextToken != nil {
		form.Set("NextToken", *in.NextToken)
	}

	for i, f := range in.Dimensions {
		p := "Dimensions.member." + strconv.Itoa(i+1) + "."
		form.Set(p+"Name", aws.ToString(f.Name))

		if f.Value != nil {
			form.Set(p+"Value", *f.Value)
		}
	}

	return form
}

func getStatsForm(in *awscw.GetMetricStatisticsInput) url.Values {
	form := url.Values{
		"Action":     {"GetMetricStatistics"},
		"Namespace":  {aws.ToString(in.Namespace)},
		"MetricName": {aws.ToString(in.MetricName)},
		"Period":     {strconv.Itoa(int(aws.ToInt32(in.Period)))},
		"StartTime":  {aws.ToTime(in.StartTime).UTC().Format(time.RFC3339)},
		"EndTime":    {aws.ToTime(in.EndTime).UTC().Format(time.RFC3339)},
	}

	for i, s := range in.Statistics {
		form.Set("Statistics.member."+strconv.Itoa(i+1), string(s))
	}

	addDimensionsForm(form, in.Dimensions)

	return form
}

type dimXML struct {
	Name  string `xml:"Name"`
	Value string `xml:"Value"`
}

type listMetricsXML struct {
	Metrics []struct {
		Namespace  string   `xml:"Namespace"`
		MetricName string   `xml:"MetricName"`
		Dimensions []dimXML `xml:"Dimensions>member"`
	} `xml:"ListMetricsResult>Metrics>member"`
	NextToken string `xml:"ListMetricsResult>NextToken"`
}

func (x listMetricsXML) toSDK() *awscw.ListMetricsOutput {
	out := &awscw.ListMetricsOutput{}
	if x.NextToken != "" {
		out.NextToken = aws.String(x.NextToken)
	}

	for _, m := range x.Metrics {
		mt := cwtypes.Metric{Namespace: aws.String(m.Namespace), MetricName: aws.String(m.MetricName)}
		for _, d := range m.Dimensions {
			mt.Dimensions = append(mt.Dimensions, cwtypes.Dimension{Name: aws.String(d.Name), Value: aws.String(d.Value)})
		}

		out.Metrics = append(out.Metrics, mt)
	}

	return out
}

// getStatsXML keeps pointers so an absent statistic stays nil, like the SDK.
type getStatsXML struct {
	Datapoints []struct {
		SampleCount *float64 `xml:"SampleCount"`
		Average     *float64 `xml:"Average"`
		Sum         *float64 `xml:"Sum"`
		Minimum     *float64 `xml:"Minimum"`
		Maximum     *float64 `xml:"Maximum"`
		Unit        string   `xml:"Unit"`
	} `xml:"GetMetricStatisticsResult>Datapoints>member"`
}

func (x getStatsXML) toSDK() *awscw.GetMetricStatisticsOutput {
	out := &awscw.GetMetricStatisticsOutput{}

	for _, d := range x.Datapoints {
		out.Datapoints = append(out.Datapoints, cwtypes.Datapoint{
			SampleCount: d.SampleCount, Average: d.Average, Sum: d.Sum,
			Minimum: d.Minimum, Maximum: d.Maximum, Unit: cwtypes.StandardUnit(d.Unit),
		})
	}

	return out
}

func dims(kv ...string) []cwtypes.Dimension {
	out := make([]cwtypes.Dimension, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, cwtypes.Dimension{Name: aws.String(kv[i]), Value: aws.String(kv[i+1])})
	}

	return out
}

// metricKeys renders rows as sorted "ns|name|k=v,..." strings for comparison.
func metricKeys(ms []cwtypes.Metric) []string {
	out := make([]string, 0, len(ms))

	for _, m := range ms {
		parts := make([]string, 0, len(m.Dimensions))
		for _, d := range m.Dimensions {
			parts = append(parts, aws.ToString(d.Name)+"="+aws.ToString(d.Value))
		}

		sort.Strings(parts)
		out = append(out, aws.ToString(m.Namespace)+"|"+aws.ToString(m.MetricName)+"|"+strings.Join(parts, ","))
	}

	sort.Strings(out)

	return out
}

func seedAppMetrics(t *testing.T, w cwWire) {
	t.Helper()

	now := time.Now().UTC()
	w.put(t, &awscw.PutMetricDataInput{
		Namespace: aws.String("T/App"),
		MetricData: []cwtypes.MetricDatum{
			{MetricName: aws.String("A"), Value: aws.Float64(1), Timestamp: aws.Time(now), Dimensions: dims("Env", "prod", "Svc", "x")},
			{MetricName: aws.String("A"), Value: aws.Float64(2), Timestamp: aws.Time(now), Dimensions: dims("Env", "dev")},
			{MetricName: aws.String("B"), Value: aws.Float64(3), Timestamp: aws.Time(now), Dimensions: dims("Env", "dev")},
		},
	})
}

// TestListMetricsParity checks the ListMetrics filters and the Dimensions on
// each row. The query path used to list names only and ignore every filter.
func TestListMetricsParity(t *testing.T) {
	const (
		aProd = "T/App|A|Env=prod,Svc=x"
		aDev  = "T/App|A|Env=dev"
		bDev  = "T/App|B|Env=dev"
	)

	cases := []struct {
		name string
		in   *awscw.ListMetricsInput
		want []string
	}{
		{"namespace", &awscw.ListMetricsInput{Namespace: aws.String("T/App")}, []string{aDev, aProd, bDev}},
		{"metric name", &awscw.ListMetricsInput{Namespace: aws.String("T/App"), MetricName: aws.String("A")}, []string{aDev, aProd}},
		{"dimension value no match", &awscw.ListMetricsInput{
			Namespace: aws.String("T/App"), Dimensions: []cwtypes.DimensionFilter{{Name: aws.String("Env"), Value: aws.String("staging")}},
		}, []string{}},
		{"dimension name only", &awscw.ListMetricsInput{
			Namespace: aws.String("T/App"), Dimensions: []cwtypes.DimensionFilter{{Name: aws.String("Env")}},
		}, []string{aDev, aProd, bDev}},
		{"dimension subset", &awscw.ListMetricsInput{
			Namespace: aws.String("T/App"), Dimensions: []cwtypes.DimensionFilter{{Name: aws.String("Svc"), Value: aws.String("x")}},
		}, []string{aProd}},
	}

	for _, p := range cwProtocols() {
		for _, tc := range cases {
			t.Run(p.name+"/"+tc.name, func(t *testing.T) {
				w := p.build(t, nil)
				seedAppMetrics(t, w)

				got := metricKeys(w.listMetrics(t, tc.in).Metrics)
				if strings.Join(got, ";") != strings.Join(tc.want, ";") {
					t.Fatalf("metrics = %v, want %v", got, tc.want)
				}
			})
		}
	}
}

// TestListMetricsIPAMParity checks that the derived AWS/IPAM metrics are listed
// on both protocols. The query path used to return none.
func TestListMetricsIPAMParity(t *testing.T) {
	ipam := fakeIPAM{{
		Namespace: netdriver.IpamMetricNamespace, MetricName: "VpcIPUsage", Value: 3, Unit: "Percent",
		Dimensions: map[string]string{"IpamId": "ipam-1"},
	}}

	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, ipam)
			seedAppMetrics(t, w)

			got := metricKeys(w.listMetrics(t, &awscw.ListMetricsInput{Namespace: aws.String("AWS/IPAM")}).Metrics)
			if want := "AWS/IPAM|VpcIPUsage|IpamId=ipam-1"; len(got) != 1 || got[0] != want {
				t.Fatalf("metrics = %v, want [%s]", got, want)
			}
		})
	}
}

// TestListMetricsPagingParity checks 500 rows per page, counted per series.
func TestListMetricsPagingParity(t *testing.T) {
	const series = 501

	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, nil)

			now := time.Now().UTC()
			data := make([]mondriver.MetricDatum, 0, series)

			for i := range series {
				data = append(data, mondriver.MetricDatum{
					Namespace: "P/App", MetricName: "S", Value: 1, Timestamp: now,
					Dimensions: map[string]string{"Idx": strconv.Itoa(i)},
				})
			}

			if err := w.provider.PutMetricData(context.Background(), data); err != nil {
				t.Fatalf("seed: %v", err)
			}

			first := w.listMetrics(t, &awscw.ListMetricsInput{Namespace: aws.String("P/App")})
			if len(first.Metrics) != 500 || first.NextToken == nil {
				t.Fatalf("first page: %d rows, token %v; want 500 rows and a token", len(first.Metrics), first.NextToken)
			}

			second := w.listMetrics(t, &awscw.ListMetricsInput{Namespace: aws.String("P/App"), NextToken: first.NextToken})
			if len(second.Metrics) != 1 || second.NextToken != nil {
				t.Fatalf("second page: %d rows, token %v; want 1 row and no token", len(second.Metrics), second.NextToken)
			}
		})
	}
}
