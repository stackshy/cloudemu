package cloudwatch_test

import (
	"errors"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	smithy "github.com/aws/smithy-go"
)

// sdkErrCode returns the API error code of err, or "" when err is nil.
func sdkErrCode(t *testing.T, err error) string {
	t.Helper()

	if err == nil {
		return ""
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("not an API error: %v", err)
	}

	return apiErr.ErrorCode()
}

func setIfSet(form url.Values, key string, v *string) {
	if v != nil {
		form.Set(key, *v)
	}
}

func getMetricDataForm(in *awscw.GetMetricDataInput) url.Values {
	form := url.Values{
		"Action":    {"GetMetricData"},
		"StartTime": {aws.ToTime(in.StartTime).UTC().Format(time.RFC3339)},
		"EndTime":   {aws.ToTime(in.EndTime).UTC().Format(time.RFC3339)},
	}

	setIfSet(form, "NextToken", in.NextToken)

	if in.ScanBy != "" {
		form.Set("ScanBy", string(in.ScanBy))
	}

	if in.MaxDatapoints != nil {
		form.Set("MaxDatapoints", strconv.Itoa(int(*in.MaxDatapoints)))
	}

	for i, q := range in.MetricDataQueries {
		p := "MetricDataQueries.member." + strconv.Itoa(i+1) + "."
		form.Set(p+"Id", aws.ToString(q.Id))
		setIfSet(form, p+"Label", q.Label)
		setIfSet(form, p+"Expression", q.Expression)

		if q.ReturnData != nil {
			form.Set(p+"ReturnData", strconv.FormatBool(*q.ReturnData))
		}

		if ms := q.MetricStat; ms != nil {
			form.Set(p+"MetricStat.Metric.Namespace", aws.ToString(ms.Metric.Namespace))
			form.Set(p+"MetricStat.Metric.MetricName", aws.ToString(ms.Metric.MetricName))
			form.Set(p+"MetricStat.Period", strconv.Itoa(int(aws.ToInt32(ms.Period))))
			form.Set(p+"MetricStat.Stat", aws.ToString(ms.Stat))

			if ms.Unit != "" {
				form.Set(p+"MetricStat.Unit", string(ms.Unit))
			}

			for j, d := range ms.Metric.Dimensions {
				dp := p + "MetricStat.Metric.Dimensions.member." + strconv.Itoa(j+1) + "."
				form.Set(dp+"Name", aws.ToString(d.Name))
				form.Set(dp+"Value", aws.ToString(d.Value))
			}
		}
	}

	return form
}

type getMetricDataXML struct {
	Results []struct {
		ID         string    `xml:"Id"`
		Label      string    `xml:"Label"`
		StatusCode string    `xml:"StatusCode"`
		Timestamps []string  `xml:"Timestamps>member"`
		Values     []float64 `xml:"Values>member"`
	} `xml:"GetMetricDataResult>MetricDataResults>member"`
	NextToken string `xml:"GetMetricDataResult>NextToken"`
}

func (x getMetricDataXML) toSDK() *awscw.GetMetricDataOutput {
	out := &awscw.GetMetricDataOutput{}
	if x.NextToken != "" {
		out.NextToken = aws.String(x.NextToken)
	}

	for _, r := range x.Results {
		res := cwtypes.MetricDataResult{
			Id: aws.String(r.ID), Label: aws.String(r.Label),
			StatusCode: cwtypes.StatusCode(r.StatusCode), Values: r.Values,
		}

		for _, raw := range r.Timestamps {
			ts, _ := time.Parse(time.RFC3339, raw)
			res.Timestamps = append(res.Timestamps, ts)
		}

		out.MetricDataResults = append(out.MetricDataResults, res)
	}

	return out
}

func putAlarmForm(in *awscw.PutMetricAlarmInput) url.Values {
	form := url.Values{
		"Action":             {"PutMetricAlarm"},
		"AlarmName":          {aws.ToString(in.AlarmName)},
		"Namespace":          {aws.ToString(in.Namespace)},
		"MetricName":         {aws.ToString(in.MetricName)},
		"ComparisonOperator": {string(in.ComparisonOperator)},
		"Threshold":          {strconv.FormatFloat(aws.ToFloat64(in.Threshold), 'g', -1, 64)},
		"Period":             {strconv.Itoa(int(aws.ToInt32(in.Period)))},
		"EvaluationPeriods":  {strconv.Itoa(int(aws.ToInt32(in.EvaluationPeriods)))},
	}

	if in.Statistic != "" {
		form.Set("Statistic", string(in.Statistic))
	}

	setIfSet(form, "ExtendedStatistic", in.ExtendedStatistic)

	if in.Unit != "" {
		form.Set("Unit", string(in.Unit))
	}

	addDimensionsForm(form, in.Dimensions)

	return form
}

func alarmsForMetricForm(in *awscw.DescribeAlarmsForMetricInput) url.Values {
	form := url.Values{"Action": {"DescribeAlarmsForMetric"}}
	setIfSet(form, "Namespace", in.Namespace)
	setIfSet(form, "MetricName", in.MetricName)
	setIfSet(form, "ExtendedStatistic", in.ExtendedStatistic)

	if in.Statistic != "" {
		form.Set("Statistic", string(in.Statistic))
	}

	if in.Unit != "" {
		form.Set("Unit", string(in.Unit))
	}

	if in.Period != nil {
		form.Set("Period", strconv.Itoa(int(*in.Period)))
	}

	addDimensionsForm(form, in.Dimensions)

	return form
}

type alarmsForMetricXML struct {
	Alarms []struct {
		AlarmName string `xml:"AlarmName"`
	} `xml:"DescribeAlarmsForMetricResult>MetricAlarms>member"`
}

func (x alarmsForMetricXML) toSDK() *awscw.DescribeAlarmsForMetricOutput {
	out := &awscw.DescribeAlarmsForMetricOutput{}
	for _, a := range x.Alarms {
		out.MetricAlarms = append(out.MetricAlarms, cwtypes.MetricAlarm{AlarmName: aws.String(a.AlarmName)})
	}

	return out
}

// seedSeries puts values 1, 2, 3 one minute apart on T/App A (Env=prod,
// Svc=x) and returns the time range that covers them.
func seedSeries(t *testing.T, w cwWire) (start, end time.Time) {
	t.Helper()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	data := make([]cwtypes.MetricDatum, 0, 3)

	for i := range 3 {
		data = append(data, cwtypes.MetricDatum{
			MetricName: aws.String("A"), Value: aws.Float64(float64(i + 1)),
			Timestamp: aws.Time(base.Add(time.Duration(i) * time.Minute)), Dimensions: dims("Env", "prod", "Svc", "x"),
		})
	}

	w.put(t, &awscw.PutMetricDataInput{Namespace: aws.String("T/App"), MetricData: data})

	return base.Add(-time.Minute), base.Add(5 * time.Minute)
}

func sumQuery(id string, returnData *bool) cwtypes.MetricDataQuery {
	return cwtypes.MetricDataQuery{
		Id:         aws.String(id),
		ReturnData: returnData,
		MetricStat: &cwtypes.MetricStat{
			Metric: &cwtypes.Metric{Namespace: aws.String("T/App"), MetricName: aws.String("A"), Dimensions: dims("Env", "prod", "Svc", "x")},
			Period: aws.Int32(60),
			Stat:   aws.String("Sum"),
		},
	}
}

// TestGetMetricDataMathExpression: a math row over a hidden input row. The
// query protocol returned InvalidAction before this fix.
func TestGetMetricDataMathExpression(t *testing.T) {
	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, nil)
			start, end := seedSeries(t, w)

			out, code := w.getMetricData(t, &awscw.GetMetricDataInput{
				StartTime: aws.Time(start), EndTime: aws.Time(end),
				MetricDataQueries: []cwtypes.MetricDataQuery{
					sumQuery("m1", aws.Bool(false)),
					{Id: aws.String("e1"), Expression: aws.String("m1*2"), Label: aws.String("double")},
				},
			})
			if code != "" {
				t.Fatalf("GetMetricData error %s", code)
			}

			if len(out.MetricDataResults) != 1 {
				t.Fatalf("rows = %d, want only e1", len(out.MetricDataResults))
			}

			row := out.MetricDataResults[0]
			if aws.ToString(row.Id) != "e1" || aws.ToString(row.Label) != "double" {
				t.Fatalf("row = %s/%s, want e1/double", aws.ToString(row.Id), aws.ToString(row.Label))
			}

			want := []float64{6, 4, 2}
			if !floatsEqual(row.Values, want) {
				t.Fatalf("values = %v, want %v (doubled, newest first)", row.Values, want)
			}
		})
	}
}

func floatsEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
