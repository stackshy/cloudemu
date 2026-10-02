package cloudwatch_test

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// anomalyWire adds the anomaly detector ops to cwWire. The funcs return the
// error code, or "" on success.
type anomalyWire struct {
	cwWire
	putDetector func(t *testing.T, in *awscw.PutAnomalyDetectorInput) string
	describe    func(t *testing.T, in *awscw.DescribeAnomalyDetectorsInput) (*awscw.DescribeAnomalyDetectorsOutput, string)
	// del also returns the HTTP status.
	del func(t *testing.T, in *awscw.DeleteAnomalyDetectorInput) (int, string)
}

type anomalyProtocol struct {
	name  string
	build func(t *testing.T) anomalyWire
}

func anomalyProtocols() []anomalyProtocol {
	return []anomalyProtocol{{"query", newQueryAnomalyWire}, {"cbor", newCBORAnomalyWire}}
}

func newCBORAnomalyWire(t *testing.T) anomalyWire {
	t.Helper()

	w := newCBORWire(t, nil)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	c := awscw.NewFromConfig(cfg, func(o *awscw.Options) { o.BaseEndpoint = aws.String(w.url) })
	ctx := context.Background()

	return anomalyWire{
		cwWire: w,
		putDetector: func(t *testing.T, in *awscw.PutAnomalyDetectorInput) string {
			t.Helper()
			_, err := c.PutAnomalyDetector(ctx, in)

			return sdkErrCode(t, err)
		},
		describe: func(t *testing.T, in *awscw.DescribeAnomalyDetectorsInput) (*awscw.DescribeAnomalyDetectorsOutput, string) {
			t.Helper()
			out, err := c.DescribeAnomalyDetectors(ctx, in)

			return out, sdkErrCode(t, err)
		},
		del: func(t *testing.T, in *awscw.DeleteAnomalyDetectorInput) (int, string) {
			t.Helper()

			_, err := c.DeleteAnomalyDetector(ctx, in)
			if err == nil {
				return http.StatusOK, ""
			}

			var re *awshttp.ResponseError
			if !errors.As(err, &re) {
				t.Fatalf("DeleteAnomalyDetector: %v", err)
			}

			return re.HTTPStatusCode(), sdkErrCode(t, err)
		},
	}
}

func newQueryAnomalyWire(t *testing.T) anomalyWire {
	t.Helper()

	w := newQueryWire(t, nil)

	post := func(t *testing.T, form url.Values, out any) (int, string) {
		t.Helper()

		req, _ := http.NewRequest(http.MethodPost, w.url+"/", strings.NewReader(form.Encode()))
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

			return resp.StatusCode, e.Code
		}

		if out != nil {
			if err := xml.Unmarshal(body, out); err != nil {
				t.Fatalf("decode: %v %s", err, body)
			}
		}

		return http.StatusOK, ""
	}

	return anomalyWire{
		cwWire: w,
		putDetector: func(t *testing.T, in *awscw.PutAnomalyDetectorInput) string {
			t.Helper()
			_, code := post(t, detectorForm("PutAnomalyDetector", in), nil)

			return code
		},
		describe: func(t *testing.T, in *awscw.DescribeAnomalyDetectorsInput) (*awscw.DescribeAnomalyDetectorsOutput, string) {
			t.Helper()

			var x describeDetectorsXML
			_, code := post(t, describeDetectorsForm(in), &x)

			return x.toSDK(), code
		},
		del: func(t *testing.T, in *awscw.DeleteAnomalyDetectorInput) (int, string) {
			t.Helper()

			return post(t, detectorForm("DeleteAnomalyDetector", &awscw.PutAnomalyDetectorInput{
				Namespace: in.Namespace, MetricName: in.MetricName, Stat: in.Stat, Dimensions: in.Dimensions,
				SingleMetricAnomalyDetector: in.SingleMetricAnomalyDetector, MetricMathAnomalyDetector: in.MetricMathAnomalyDetector,
			}), nil)
		},
	}
}

func detectorForm(action string, in *awscw.PutAnomalyDetectorInput) url.Values {
	form := url.Values{"Action": {action}}
	setIfSet(form, "Namespace", in.Namespace)
	setIfSet(form, "MetricName", in.MetricName)
	setIfSet(form, "Stat", in.Stat)
	addDimensionsForm(form, in.Dimensions)

	if s := in.SingleMetricAnomalyDetector; s != nil {
		p := "SingleMetricAnomalyDetector."
		setIfSet(form, p+"AccountId", s.AccountId)
		setIfSet(form, p+"Namespace", s.Namespace)
		setIfSet(form, p+"MetricName", s.MetricName)
		setIfSet(form, p+"Stat", s.Stat)

		for i, d := range s.Dimensions {
			dp := p + "Dimensions.member." + strconv.Itoa(i+1) + "."
			form.Set(dp+"Name", aws.ToString(d.Name))
			form.Set(dp+"Value", aws.ToString(d.Value))
		}
	}

	if m := in.MetricMathAnomalyDetector; m != nil {
		addQueriesForm(form, "MetricMathAnomalyDetector.MetricDataQueries", m.MetricDataQueries)
	}

	if c := in.Configuration; c != nil {
		setIfSet(form, "Configuration.MetricTimezone", c.MetricTimezone)

		if len(c.ExcludedTimeRanges) == 0 {
			form.Set("Configuration.ExcludedTimeRanges", "")
		}

		for i, r := range c.ExcludedTimeRanges {
			p := "Configuration.ExcludedTimeRanges.member." + strconv.Itoa(i+1) + "."
			form.Set(p+"StartTime", aws.ToTime(r.StartTime).UTC().Format(time.RFC3339))
			form.Set(p+"EndTime", aws.ToTime(r.EndTime).UTC().Format(time.RFC3339))
		}
	}

	if mc := in.MetricCharacteristics; mc != nil && mc.PeriodicSpikes != nil {
		form.Set("MetricCharacteristics.PeriodicSpikes", strconv.FormatBool(*mc.PeriodicSpikes))
	}

	return form
}

func describeDetectorsForm(in *awscw.DescribeAnomalyDetectorsInput) url.Values {
	form := url.Values{"Action": {"DescribeAnomalyDetectors"}}
	setIfSet(form, "Namespace", in.Namespace)
	setIfSet(form, "MetricName", in.MetricName)
	setIfSet(form, "NextToken", in.NextToken)
	addDimensionsForm(form, in.Dimensions)

	if in.MaxResults != nil {
		form.Set("MaxResults", strconv.Itoa(int(*in.MaxResults)))
	}

	for i, typ := range in.AnomalyDetectorTypes {
		form.Set("AnomalyDetectorTypes.member."+strconv.Itoa(i+1), string(typ))
	}

	return form
}

type describeDetectorsXML struct {
	Detectors []struct {
		Namespace  string   `xml:"Namespace"`
		MetricName string   `xml:"MetricName"`
		Stat       string   `xml:"Stat"`
		Dimensions []dimXML `xml:"Dimensions>member"`
		Single     *struct {
			Namespace  string   `xml:"Namespace"`
			MetricName string   `xml:"MetricName"`
			Stat       string   `xml:"Stat"`
			Dimensions []dimXML `xml:"Dimensions>member"`
		} `xml:"SingleMetricAnomalyDetector"`
		Math *struct {
			Queries []struct {
				ID         string `xml:"Id"`
				Expression string `xml:"Expression"`
				ReturnData bool   `xml:"ReturnData"`
			} `xml:"MetricDataQueries>member"`
		} `xml:"MetricMathAnomalyDetector"`
		Config *struct {
			Ranges []struct {
				Start string `xml:"StartTime"`
				End   string `xml:"EndTime"`
			} `xml:"ExcludedTimeRanges>member"`
			Timezone string `xml:"MetricTimezone"`
		} `xml:"Configuration"`
		Characteristics *struct {
			PeriodicSpikes bool `xml:"PeriodicSpikes"`
		} `xml:"MetricCharacteristics"`
		StateValue string `xml:"StateValue"`
	} `xml:"DescribeAnomalyDetectorsResult>AnomalyDetectors>member"`
	NextToken string `xml:"DescribeAnomalyDetectorsResult>NextToken"`
}

func sdkDims(in []dimXML) []cwtypes.Dimension {
	var out []cwtypes.Dimension
	for _, d := range in {
		out = append(out, cwtypes.Dimension{Name: aws.String(d.Name), Value: aws.String(d.Value)})
	}

	return out
}

func (x describeDetectorsXML) toSDK() *awscw.DescribeAnomalyDetectorsOutput {
	out := &awscw.DescribeAnomalyDetectorsOutput{NextToken: optString(x.NextToken)}

	for _, d := range x.Detectors {
		ad := cwtypes.AnomalyDetector{
			Namespace: optString(d.Namespace), MetricName: optString(d.MetricName), Stat: optString(d.Stat),
			Dimensions: sdkDims(d.Dimensions), StateValue: cwtypes.AnomalyDetectorStateValue(d.StateValue),
		}

		if s := d.Single; s != nil {
			ad.SingleMetricAnomalyDetector = &cwtypes.SingleMetricAnomalyDetector{
				Namespace: aws.String(s.Namespace), MetricName: aws.String(s.MetricName), Stat: aws.String(s.Stat), Dimensions: sdkDims(s.Dimensions),
			}
		}

		if m := d.Math; m != nil {
			ad.MetricMathAnomalyDetector = &cwtypes.MetricMathAnomalyDetector{}
			for _, q := range m.Queries {
				ad.MetricMathAnomalyDetector.MetricDataQueries = append(ad.MetricMathAnomalyDetector.MetricDataQueries, cwtypes.MetricDataQuery{
					Id: aws.String(q.ID), Expression: optString(q.Expression), ReturnData: aws.Bool(q.ReturnData),
				})
			}
		}

		if c := d.Config; c != nil {
			ad.Configuration = &cwtypes.AnomalyDetectorConfiguration{MetricTimezone: optString(c.Timezone), ExcludedTimeRanges: []cwtypes.Range{}}
			for _, r := range c.Ranges {
				start, _ := time.Parse(time.RFC3339, r.Start)
				end, _ := time.Parse(time.RFC3339, r.End)
				ad.Configuration.ExcludedTimeRanges = append(ad.Configuration.ExcludedTimeRanges, cwtypes.Range{StartTime: &start, EndTime: &end})
			}
		}

		if c := d.Characteristics; c != nil {
			ad.MetricCharacteristics = &cwtypes.MetricCharacteristics{PeriodicSpikes: aws.Bool(c.PeriodicSpikes)}
		}

		out.AnomalyDetectors = append(out.AnomalyDetectors, ad)
	}

	return out
}

const anomNS = "A/App"

func singleDetector(name, stat string, dims ...string) *cwtypes.SingleMetricAnomalyDetector {
	d := &cwtypes.SingleMetricAnomalyDetector{Namespace: aws.String(anomNS), MetricName: aws.String(name), Stat: aws.String(stat)}
	for i := 0; i+1 < len(dims); i += 2 {
		d.Dimensions = append(d.Dimensions, cwtypes.Dimension{Name: aws.String(dims[i]), Value: aws.String(dims[i+1])})
	}

	return d
}

func mathDetectorInput() *cwtypes.MetricMathAnomalyDetector {
	m1 := mathStat("m1", "Errors")

	return &cwtypes.MetricMathAnomalyDetector{MetricDataQueries: []cwtypes.MetricDataQuery{
		m1, {Id: aws.String("e1"), Expression: aws.String("m1*2"), ReturnData: aws.Bool(true)},
	}}
}

func describeAll(t *testing.T, w anomalyWire, in *awscw.DescribeAnomalyDetectorsInput) []cwtypes.AnomalyDetector {
	t.Helper()

	out, code := w.describe(t, in)
	if code != "" {
		t.Fatalf("DescribeAnomalyDetectors: %s", code)
	}

	return out.AnomalyDetectors
}

// Put, describe, update and delete the three detector forms. Before CW-7a
// every call failed with InvalidAction or UnknownOperationException.
func TestAnomalyDetectorLifecycle(t *testing.T) {
	for _, proto := range anomalyProtocols() {
		t.Run(proto.name, func(t *testing.T) {
			w := proto.build(t)

			start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
			ranges := []cwtypes.Range{
				{StartTime: aws.Time(start), EndTime: aws.Time(start.Add(time.Hour))},
				{StartTime: aws.Time(start.Add(24 * time.Hour)), EndTime: aws.Time(start.Add(25 * time.Hour))},
			}

			put := &awscw.PutAnomalyDetectorInput{
				SingleMetricAnomalyDetector: singleDetector("Lat", "Average", "Host", "a"),
				Configuration:               &cwtypes.AnomalyDetectorConfiguration{ExcludedTimeRanges: ranges, MetricTimezone: aws.String("Europe/Berlin")},
				MetricCharacteristics:       &cwtypes.MetricCharacteristics{PeriodicSpikes: aws.Bool(true)},
			}
			if code := w.putDetector(t, put); code != "" {
				t.Fatalf("put single: %s", code)
			}

			legacy := &awscw.PutAnomalyDetectorInput{Namespace: aws.String(anomNS), MetricName: aws.String("Cpu"), Stat: aws.String("p90")}
			if code := w.putDetector(t, legacy); code != "" {
				t.Fatalf("put legacy: %s", code)
			}

			if code := w.putDetector(t, &awscw.PutAnomalyDetectorInput{MetricMathAnomalyDetector: mathDetectorInput()}); code != "" {
				t.Fatalf("put math: %s", code)
			}

			singles := describeAll(t, w, &awscw.DescribeAnomalyDetectorsInput{})
			if len(singles) != 2 {
				t.Fatalf("default describe = %d detectors, want the 2 single-metric ones", len(singles))
			}

			cpu, lat := singles[0], singles[1]
			if aws.ToString(cpu.MetricName) != "Cpu" || aws.ToString(cpu.SingleMetricAnomalyDetector.Stat) != "p90" {
				t.Fatalf("legacy detector = %+v", cpu)
			}

			if cpu.Configuration == nil || len(cpu.Configuration.ExcludedTimeRanges) != 0 {
				t.Fatalf("legacy detector Configuration = %+v", cpu.Configuration)
			}

			if aws.ToString(lat.Namespace) != anomNS || aws.ToString(lat.Stat) != "Average" || len(lat.Dimensions) != 1 ||
				aws.ToString(lat.SingleMetricAnomalyDetector.MetricName) != "Lat" || len(lat.SingleMetricAnomalyDetector.Dimensions) != 1 {
				t.Fatalf("single detector = %+v", lat)
			}

			if len(lat.Configuration.ExcludedTimeRanges) != 2 || aws.ToString(lat.Configuration.MetricTimezone) != "Europe/Berlin" ||
				!aws.ToTime(lat.Configuration.ExcludedTimeRanges[1].StartTime).Equal(start.Add(24*time.Hour)) ||
				!aws.ToBool(lat.MetricCharacteristics.PeriodicSpikes) || lat.StateValue != cwtypes.AnomalyDetectorStateValuePendingTraining {
				t.Fatalf("single detector configuration = %+v %+v %s", lat.Configuration, lat.MetricCharacteristics, lat.StateValue)
			}

			maths := describeAll(t, w, &awscw.DescribeAnomalyDetectorsInput{AnomalyDetectorTypes: []cwtypes.AnomalyDetectorType{"METRIC_MATH"}})
			if len(maths) != 1 || len(maths[0].MetricMathAnomalyDetector.MetricDataQueries) != 2 || maths[0].Namespace != nil {
				t.Fatalf("math describe = %+v", maths)
			}

			both := describeAll(t, w, &awscw.DescribeAnomalyDetectorsInput{AnomalyDetectorTypes: []cwtypes.AnomalyDetectorType{"METRIC_MATH", "SINGLE_METRIC"}})
			if len(both) != 3 {
				t.Fatalf("both types = %d detectors", len(both))
			}

			// The same metric again replaces the configuration.
			put.Configuration = &cwtypes.AnomalyDetectorConfiguration{ExcludedTimeRanges: ranges[:1]}
			if code := w.putDetector(t, put); code != "" {
				t.Fatalf("update: %s", code)
			}

			got := describeAll(t, w, &awscw.DescribeAnomalyDetectorsInput{MetricName: aws.String("Lat")})
			if len(got) != 1 || len(got[0].Configuration.ExcludedTimeRanges) != 1 || got[0].Configuration.MetricTimezone != nil {
				t.Fatalf("after update = %+v", got)
			}

			del := &awscw.DeleteAnomalyDetectorInput{SingleMetricAnomalyDetector: singleDetector("Lat", "Average", "Host", "a")}
			if status, code := w.del(t, del); code != "" {
				t.Fatalf("delete: %d %s", status, code)
			}

			if status, code := w.del(t, del); status != http.StatusNotFound || code != "ResourceNotFoundException" {
				t.Fatalf("delete again = %d %s, want 404 ResourceNotFoundException", status, code)
			}

			if status, code := w.del(t, &awscw.DeleteAnomalyDetectorInput{MetricMathAnomalyDetector: mathDetectorInput()}); code != "" {
				t.Fatalf("delete math: %d %s", status, code)
			}

			if left := describeAll(t, w, &awscw.DescribeAnomalyDetectorsInput{
				AnomalyDetectorTypes: []cwtypes.AnomalyDetectorType{"METRIC_MATH", "SINGLE_METRIC"},
			}); len(left) != 1 || aws.ToString(left[0].MetricName) != "Cpu" {
				t.Fatalf("after deletes = %+v", left)
			}
		})
	}
}

func TestDescribeAnomalyDetectorsFilters(t *testing.T) {
	for _, proto := range anomalyProtocols() {
		t.Run(proto.name, func(t *testing.T) {
			w := proto.build(t)

			for _, d := range []*cwtypes.SingleMetricAnomalyDetector{
				singleDetector("Lat", "Average", "Host", "a", "Zone", "z1"),
				singleDetector("Lat", "Average", "Host", "b"),
				singleDetector("Cpu", "Maximum", "Host", "a"),
			} {
				if code := w.putDetector(t, &awscw.PutAnomalyDetectorInput{SingleMetricAnomalyDetector: d}); code != "" {
					t.Fatalf("put: %s", code)
				}
			}

			other := &cwtypes.SingleMetricAnomalyDetector{Namespace: aws.String("B/Other"), MetricName: aws.String("Lat"), Stat: aws.String("Sum")}
			if code := w.putDetector(t, &awscw.PutAnomalyDetectorInput{SingleMetricAnomalyDetector: other}); code != "" {
				t.Fatalf("put other: %s", code)
			}

			hostA := []cwtypes.Dimension{{Name: aws.String("Host"), Value: aws.String("a")}}

			counts := []struct {
				name string
				in   *awscw.DescribeAnomalyDetectorsInput
				want int
			}{
				{"all", &awscw.DescribeAnomalyDetectorsInput{}, 4},
				{"namespace", &awscw.DescribeAnomalyDetectorsInput{Namespace: aws.String(anomNS)}, 3},
				{"metric name across namespaces", &awscw.DescribeAnomalyDetectorsInput{MetricName: aws.String("Lat")}, 3},
				{"dimension subset", &awscw.DescribeAnomalyDetectorsInput{Dimensions: hostA}, 2},
				{"name and dimension", &awscw.DescribeAnomalyDetectorsInput{MetricName: aws.String("Lat"), Dimensions: hostA}, 1},
				{"math only", &awscw.DescribeAnomalyDetectorsInput{AnomalyDetectorTypes: []cwtypes.AnomalyDetectorType{"METRIC_MATH"}}, 0},
			}

			for _, c := range counts {
				if got := describeAll(t, w, c.in); len(got) != c.want {
					t.Errorf("%s: %d detectors, want %d", c.name, len(got), c.want)
				}
			}

			// Paging one at a time returns every detector once.
			seen := map[string]bool{}
			var token *string

			for page := 0; page < 10; page++ {
				out, code := w.describe(t, &awscw.DescribeAnomalyDetectorsInput{MaxResults: aws.Int32(1), NextToken: token})
				if code != "" || len(out.AnomalyDetectors) != 1 {
					t.Fatalf("page %d: %s %d", page, code, len(out.AnomalyDetectors))
				}

				d := out.AnomalyDetectors[0]
				seen[aws.ToString(d.Namespace)+aws.ToString(d.MetricName)+strconv.Itoa(len(d.Dimensions))+aws.ToString(d.Stat)] = true

				if token = out.NextToken; token == nil {
					break
				}
			}

			if len(seen) != 4 {
				t.Fatalf("paging saw %d detectors, want 4", len(seen))
			}

			errs := []struct {
				name string
				in   *awscw.DescribeAnomalyDetectorsInput
				code string
			}{
				{"MaxResults 0", &awscw.DescribeAnomalyDetectorsInput{MaxResults: aws.Int32(0)}, "InvalidParameterValue"},
				{"MaxResults 101", &awscw.DescribeAnomalyDetectorsInput{MaxResults: aws.Int32(101)}, "InvalidParameterValue"},
				{"bad type", &awscw.DescribeAnomalyDetectorsInput{AnomalyDetectorTypes: []cwtypes.AnomalyDetectorType{"NOPE"}}, "InvalidParameterValue"},
				{"bad token", &awscw.DescribeAnomalyDetectorsInput{NextToken: aws.String("!!")}, "InvalidNextToken"},
			}

			for _, e := range errs {
				if _, code := w.describe(t, e.in); code != e.code {
					t.Errorf("%s: code %q, want %q", e.name, code, e.code)
				}
			}
		})
	}
}

func TestPutAnomalyDetectorValidation(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	eleven := make([]cwtypes.Range, 11)
	for i := range eleven {
		eleven[i] = cwtypes.Range{StartTime: aws.Time(start.Add(time.Duration(2*i) * time.Hour)), EndTime: aws.Time(start.Add(time.Duration(2*i+1) * time.Hour))}
	}

	tests := []struct {
		name string
		in   *awscw.PutAnomalyDetectorInput
		code string
	}{
		{"single and math", &awscw.PutAnomalyDetectorInput{
			SingleMetricAnomalyDetector: singleDetector("Lat", "Average"), MetricMathAnomalyDetector: mathDetectorInput(),
		}, "InvalidParameterCombination"},
		{"legacy and single", &awscw.PutAnomalyDetectorInput{
			Namespace: aws.String(anomNS), SingleMetricAnomalyDetector: singleDetector("Lat", "Average"),
		}, "InvalidParameterCombination"},
		{"nothing", &awscw.PutAnomalyDetectorInput{}, "MissingParameter"},
		{"no stat", &awscw.PutAnomalyDetectorInput{SingleMetricAnomalyDetector: singleDetector("Lat", "")}, "MissingParameter"},
		{"bad stat", &awscw.PutAnomalyDetectorInput{SingleMetricAnomalyDetector: singleDetector("Lat", "Median")}, "InvalidParameterValue"},
		{"eleven ranges", &awscw.PutAnomalyDetectorInput{
			SingleMetricAnomalyDetector: singleDetector("Lat", "Average"),
			Configuration:               &cwtypes.AnomalyDetectorConfiguration{ExcludedTimeRanges: eleven},
		}, "LimitExceededException"},
		{"range backwards", &awscw.PutAnomalyDetectorInput{
			SingleMetricAnomalyDetector: singleDetector("Lat", "Average"),
			Configuration: &cwtypes.AnomalyDetectorConfiguration{ExcludedTimeRanges: []cwtypes.Range{
				{StartTime: aws.Time(start.Add(time.Hour)), EndTime: aws.Time(start)},
			}},
		}, "InvalidParameterValue"},
		{"math two returned", &awscw.PutAnomalyDetectorInput{MetricMathAnomalyDetector: &cwtypes.MetricMathAnomalyDetector{
			MetricDataQueries: []cwtypes.MetricDataQuery{
				{Id: aws.String("a"), Expression: aws.String("1")}, {Id: aws.String("b"), Expression: aws.String("2")},
			},
		}}, "ValidationError"},
	}

	for _, proto := range anomalyProtocols() {
		t.Run(proto.name, func(t *testing.T) {
			w := proto.build(t)

			for _, tc := range tests {
				if code := w.putDetector(t, tc.in); code != tc.code {
					t.Errorf("%s: code %q, want %q", tc.name, code, tc.code)
				}
			}

			if got := describeAll(t, w, &awscw.DescribeAnomalyDetectorsInput{
				AnomalyDetectorTypes: []cwtypes.AnomalyDetectorType{"METRIC_MATH", "SINGLE_METRIC"},
			}); len(got) != 0 {
				t.Fatalf("a rejected put stored %d detectors", len(got))
			}
		})
	}
}

// bandAlarmInput watches A/App Lat against ANOMALY_DETECTION_BAND(m1, 2).
func bandAlarmInput(name string) *awscw.PutMetricAlarmInput {
	return &awscw.PutMetricAlarmInput{
		AlarmName: aws.String(name), EvaluationPeriods: aws.Int32(1), ThresholdMetricId: aws.String("ad1"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanUpperThreshold,
		Metrics: []cwtypes.MetricDataQuery{
			{Id: aws.String("m1"), ReturnData: aws.Bool(true), MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{Namespace: aws.String(anomNS), MetricName: aws.String("Lat")}, Period: aws.Int32(60), Stat: aws.String("Average"),
			}},
			{Id: aws.String("ad1"), Expression: aws.String("ANOMALY_DETECTION_BAND(m1, 2)"), ReturnData: aws.Bool(true)},
		},
	}
}

// PutMetricAlarm checks how the band operators and ThresholdMetricId go
// together, and DescribeAlarms has no Threshold for an anomaly alarm.
func TestAnomalyAlarmValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *awscw.PutMetricAlarmInput)
	}{
		{"band operator without ThresholdMetricId", func(in *awscw.PutMetricAlarmInput) { in.ThresholdMetricId = nil }},
		{"ThresholdMetricId with a static operator", func(in *awscw.PutMetricAlarmInput) {
			in.ComparisonOperator = cwtypes.ComparisonOperatorGreaterThanThreshold
		}},
		{"Threshold with ThresholdMetricId", func(in *awscw.PutMetricAlarmInput) { in.Threshold = aws.Float64(5) }},
		{"ThresholdMetricId names a metric", func(in *awscw.PutMetricAlarmInput) {
			in.ThresholdMetricId = aws.String("m1")
			in.Metrics[1].ReturnData = aws.Bool(false)
		}},
		{"single metric band alarm", func(in *awscw.PutMetricAlarmInput) {
			in.Metrics, in.ThresholdMetricId = nil, nil
			in.Namespace, in.MetricName, in.Period, in.Statistic = aws.String(anomNS), aws.String("Lat"), aws.Int32(60), cwtypes.StatisticAverage
		}},
	}

	for _, proto := range anomalyProtocols() {
		t.Run(proto.name, func(t *testing.T) {
			w := proto.build(t)

			for _, tc := range tests {
				in := bandAlarmInput("bad")
				tc.mutate(in)

				if code := w.putAlarmCode(t, in); code != "ValidationError" {
					t.Errorf("%s: code %q, want ValidationError", tc.name, code)
				}
			}

			if code := w.putAlarmCode(t, bandAlarmInput("band")); code != "" {
				t.Fatalf("valid band alarm: %s", code)
			}

			a := w.describeAlarms(t, "band")[0]
			if a.Threshold != nil || aws.ToString(a.ThresholdMetricId) != "ad1" {
				t.Fatalf("band alarm Threshold = %v, ThresholdMetricId = %v", a.Threshold, a.ThresholdMetricId)
			}

			if code := w.putAlarmCode(t, rateAlarmInput("static")); code != "" {
				t.Fatalf("static alarm: %s", code)
			}

			if s := w.describeAlarms(t, "static")[0]; aws.ToFloat64(s.Threshold) != 20 {
				t.Fatalf("static alarm Threshold = %v", s.Threshold)
			}
		})
	}
}

// putLat puts one A/App Lat value at ts.
func putLat(t *testing.T, w anomalyWire, ts time.Time, v float64) {
	t.Helper()

	w.put(t, &awscw.PutMetricDataInput{Namespace: aws.String(anomNS), MetricData: []cwtypes.MetricDatum{
		{MetricName: aws.String("Lat"), Value: aws.Float64(v), Timestamp: aws.Time(ts)},
	}})
}

// GetMetricData returns a band as two rows with its Id, the lower edge and
// then the upper edge. A detector's excluded range changes the band. Before
// CW-7a the band had no rows with data.
func TestGetMetricDataAnomalyBand(t *testing.T) {
	for _, proto := range anomalyProtocols() {
		t.Run(proto.name, func(t *testing.T) {
			w := proto.build(t)
			base := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)

			// Minutes 0-9 alternate 8 and 12, minutes 20-29 are a flat 10.
			for i := range 10 {
				putLat(t, w, base.Add(time.Duration(i)*time.Minute+time.Second), float64(8+4*(i%2)))
				putLat(t, w, base.Add(time.Duration(20+i)*time.Minute+time.Second), 10)
			}

			in := &awscw.GetMetricDataInput{
				StartTime: aws.Time(base.Add(20 * time.Minute)), EndTime: aws.Time(base.Add(30 * time.Minute)),
				ScanBy: cwtypes.ScanByTimestampAscending,
				MetricDataQueries: []cwtypes.MetricDataQuery{
					{Id: aws.String("m1"), ReturnData: aws.Bool(false), MetricStat: &cwtypes.MetricStat{
						Metric: &cwtypes.Metric{Namespace: aws.String(anomNS), MetricName: aws.String("Lat")}, Period: aws.Int32(60), Stat: aws.String("Average"),
					}},
					{Id: aws.String("ad1"), Expression: aws.String("ANOMALY_DETECTION_BAND(m1, 2)")},
				},
			}

			width := func(points int) float64 {
				out, code := w.getMetricData(t, in)
				if code != "" || len(out.MetricDataResults) != 2 {
					t.Fatalf("GetMetricData = %s %+v", code, out)
				}

				lower, upper := out.MetricDataResults[0], out.MetricDataResults[1]
				if aws.ToString(lower.Id) != "ad1" || aws.ToString(upper.Id) != "ad1" || len(lower.Values) != points || len(upper.Values) != points {
					t.Fatalf("band rows = %+v", out.MetricDataResults)
				}

				for i := range lower.Values {
					if lower.Values[i] > 10 || upper.Values[i] < 10 {
						t.Fatalf("point %d: band [%v, %v] leaves out 10", i, lower.Values[i], upper.Values[i])
					}
				}

				return upper.Values[points-1] - lower.Values[points-1]
			}

			if got := width(10); got < 5 {
				t.Fatalf("band width with noisy history = %v, want about 6", got)
			}

			// Excluding the noisy minutes leaves a flat history and a near-zero band.
			if code := w.putDetector(t, &awscw.PutAnomalyDetectorInput{
				SingleMetricAnomalyDetector: singleDetector("Lat", "Average"),
				Configuration: &cwtypes.AnomalyDetectorConfiguration{ExcludedTimeRanges: []cwtypes.Range{
					{StartTime: aws.Time(base.Add(-time.Minute)), EndTime: aws.Time(base.Add(11 * time.Minute))},
				}},
			}); code != "" {
				t.Fatalf("put detector: %s", code)
			}

			// The first three flat minutes now have too little history for a band.
			if got := width(7); got > 1e-6 {
				t.Fatalf("band width with the noise excluded = %v, want about 0", got)
			}
		})
	}
}

// An anomaly alarm over the wire: a steady series is OK, a spike above the
// band is ALARM, and the alarm's detector exists.
func TestAnomalyAlarmOverWire(t *testing.T) {
	for _, proto := range anomalyProtocols() {
		t.Run(proto.name, func(t *testing.T) {
			w := proto.build(t)
			now := time.Now().UTC()

			for i := 10; i >= 1; i-- {
				putLat(t, w, now.Add(-time.Duration(i)*time.Minute), float64(10+i%2))
			}

			if code := w.putAlarmCode(t, bandAlarmInput("band")); code != "" {
				t.Fatalf("PutMetricAlarm: %s", code)
			}

			putLat(t, w, time.Now().UTC(), 10)

			if s := w.describeAlarms(t, "band")[0].StateValue; s != cwtypes.StateValueOk {
				t.Fatalf("steady state = %s, want OK", s)
			}

			putLat(t, w, time.Now().UTC(), 500)

			if s := w.describeAlarms(t, "band")[0].StateValue; s != cwtypes.StateValueAlarm {
				t.Fatalf("spike state = %s, want ALARM", s)
			}

			got := describeAll(t, w, &awscw.DescribeAnomalyDetectorsInput{Namespace: aws.String(anomNS)})
			if len(got) != 1 || aws.ToString(got[0].MetricName) != "Lat" || aws.ToString(got[0].Stat) != "Average" {
				t.Fatalf("auto-created detector = %+v", got)
			}
		})
	}
}
