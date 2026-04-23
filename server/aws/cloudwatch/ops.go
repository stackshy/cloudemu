package cloudwatch

import (
	"net/http"
	"time"

	"github.com/fxamacker/cbor/v2"

	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

// putMetricDataInput mirrors the AWS wire shape for the operation. Field
// names are CBOR-tagged so the decoder matches the JSON-ish names the SDK
// sends (CBOR preserves string keys).
type putMetricDataInput struct {
	Namespace  string              `cbor:"Namespace"`
	MetricData []putMetricDatumCBR `cbor:"MetricData"`
}

type putMetricDatumCBR struct {
	MetricName string         `cbor:"MetricName"`
	Value      float64        `cbor:"Value"`
	Unit       string         `cbor:"Unit,omitempty"`
	Timestamp  *time.Time     `cbor:"Timestamp,omitempty"`
	Dimensions []dimensionCBR `cbor:"Dimensions,omitempty"`
}

type dimensionCBR struct {
	Name  string `cbor:"Name"`
	Value string `cbor:"Value"`
}

func (h *Handler) putMetricData(w http.ResponseWriter, r *http.Request, body []byte) {
	var in putMetricDataInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	data := make([]mondriver.MetricDatum, 0, len(in.MetricData))

	for _, d := range in.MetricData {
		ts := time.Time{}
		if d.Timestamp != nil {
			ts = *d.Timestamp
		}

		data = append(data, mondriver.MetricDatum{
			Namespace:  in.Namespace,
			MetricName: d.MetricName,
			Value:      d.Value,
			Unit:       d.Unit,
			Dimensions: toDimensionMap(d.Dimensions),
			Timestamp:  ts,
		})
	}

	if err := h.monitoring.PutMetricData(r.Context(), data); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

// getMetricStatisticsInput mirrors the SDK's GetMetricStatistics request.
type getMetricStatisticsInput struct {
	Namespace  string         `cbor:"Namespace"`
	MetricName string         `cbor:"MetricName"`
	StartTime  *time.Time     `cbor:"StartTime,omitempty"`
	EndTime    *time.Time     `cbor:"EndTime,omitempty"`
	Period     int            `cbor:"Period"`
	Statistics []string       `cbor:"Statistics,omitempty"`
	Dimensions []dimensionCBR `cbor:"Dimensions,omitempty"`
}

type datapointCBR struct {
	Timestamp   time.Time `cbor:"Timestamp"`
	SampleCount float64   `cbor:"SampleCount,omitempty"`
	Average     float64   `cbor:"Average,omitempty"`
	Sum         float64   `cbor:"Sum,omitempty"`
	Minimum     float64   `cbor:"Minimum,omitempty"`
	Maximum     float64   `cbor:"Maximum,omitempty"`
	Unit        string    `cbor:"Unit,omitempty"`
}

type getMetricStatisticsOutput struct {
	Label      string         `cbor:"Label"`
	Datapoints []datapointCBR `cbor:"Datapoints"`
}

func (h *Handler) getMetricStatistics(w http.ResponseWriter, r *http.Request, body []byte) {
	var in getMetricStatisticsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	stat := "Average"
	if len(in.Statistics) > 0 {
		stat = in.Statistics[0]
	}

	start := time.Time{}
	if in.StartTime != nil {
		start = *in.StartTime
	}

	end := time.Time{}
	if in.EndTime != nil {
		end = *in.EndTime
	}

	input := mondriver.GetMetricInput{
		Namespace:  in.Namespace,
		MetricName: in.MetricName,
		Dimensions: toDimensionMap(in.Dimensions),
		StartTime:  start,
		EndTime:    end,
		Period:     in.Period,
		Stat:       stat,
	}

	result, err := h.monitoring.GetMetricData(r.Context(), input)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, getMetricStatisticsOutput{
		Label:      in.MetricName,
		Datapoints: toDatapointsCBR(result, stat),
	})
}

type listMetricsInput struct {
	Namespace string `cbor:"Namespace,omitempty"`
}

type metricCBR struct {
	Namespace  string         `cbor:"Namespace"`
	MetricName string         `cbor:"MetricName"`
	Dimensions []dimensionCBR `cbor:"Dimensions,omitempty"`
}

type listMetricsOutput struct {
	Metrics []metricCBR `cbor:"Metrics"`
}

func (h *Handler) listMetrics(w http.ResponseWriter, r *http.Request, body []byte) {
	var in listMetricsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	names, err := h.monitoring.ListMetrics(r.Context(), in.Namespace)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	out := make([]metricCBR, 0, len(names))
	for _, name := range names {
		out = append(out, metricCBR{Namespace: in.Namespace, MetricName: name})
	}

	writeCBORResponse(w, listMetricsOutput{Metrics: out})
}

type putMetricAlarmInput struct {
	AlarmName          string         `cbor:"AlarmName"`
	Namespace          string         `cbor:"Namespace"`
	MetricName         string         `cbor:"MetricName"`
	ComparisonOperator string         `cbor:"ComparisonOperator"`
	Threshold          float64        `cbor:"Threshold"`
	Period             int            `cbor:"Period"`
	EvaluationPeriods  int            `cbor:"EvaluationPeriods"`
	Statistic          string         `cbor:"Statistic,omitempty"`
	Dimensions         []dimensionCBR `cbor:"Dimensions,omitempty"`
	AlarmActions       []string       `cbor:"AlarmActions,omitempty"`
	OKActions          []string       `cbor:"OKActions,omitempty"`
}

func (h *Handler) putMetricAlarm(w http.ResponseWriter, r *http.Request, body []byte) {
	var in putMetricAlarmInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	cfg := mondriver.AlarmConfig{
		Name:               in.AlarmName,
		Namespace:          in.Namespace,
		MetricName:         in.MetricName,
		Dimensions:         toDimensionMap(in.Dimensions),
		ComparisonOperator: in.ComparisonOperator,
		Threshold:          in.Threshold,
		Period:             in.Period,
		EvaluationPeriods:  in.EvaluationPeriods,
		Stat:               in.Statistic,
		AlarmActions:       in.AlarmActions,
		OKActions:          in.OKActions,
	}

	if err := h.monitoring.CreateAlarm(r.Context(), cfg); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

type describeAlarmsInput struct {
	AlarmNames []string `cbor:"AlarmNames,omitempty"`
}

type metricAlarmCBR struct {
	AlarmName          string  `cbor:"AlarmName"`
	Namespace          string  `cbor:"Namespace"`
	MetricName         string  `cbor:"MetricName"`
	StateValue         string  `cbor:"StateValue"`
	ComparisonOperator string  `cbor:"ComparisonOperator"`
	Threshold          float64 `cbor:"Threshold"`
}

type describeAlarmsOutput struct {
	MetricAlarms []metricAlarmCBR `cbor:"MetricAlarms"`
}

func (h *Handler) describeAlarms(w http.ResponseWriter, r *http.Request, body []byte) {
	var in describeAlarmsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	alarms, err := h.monitoring.DescribeAlarms(r.Context(), in.AlarmNames)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	out := make([]metricAlarmCBR, 0, len(alarms))
	for i := range alarms {
		out = append(out, metricAlarmCBR{
			AlarmName:          alarms[i].Name,
			Namespace:          alarms[i].Namespace,
			MetricName:         alarms[i].MetricName,
			StateValue:         alarms[i].State,
			ComparisonOperator: alarms[i].ComparisonOperator,
			Threshold:          alarms[i].Threshold,
		})
	}

	writeCBORResponse(w, describeAlarmsOutput{MetricAlarms: out})
}

type deleteAlarmsInput struct {
	AlarmNames []string `cbor:"AlarmNames"`
}

func (h *Handler) deleteAlarms(w http.ResponseWriter, r *http.Request, body []byte) {
	var in deleteAlarmsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	for _, name := range in.AlarmNames {
		if err := h.monitoring.DeleteAlarm(r.Context(), name); err != nil {
			writeDriverErr(w, err)
			return
		}
	}

	writeCBORResponse(w, struct{}{})
}

func toDimensionMap(dims []dimensionCBR) map[string]string {
	if len(dims) == 0 {
		return nil
	}

	out := make(map[string]string, len(dims))

	for _, d := range dims {
		if d.Name != "" {
			out[d.Name] = d.Value
		}
	}

	return out
}

func toDatapointsCBR(res *mondriver.MetricDataResult, stat string) []datapointCBR {
	if res == nil {
		return nil
	}

	out := make([]datapointCBR, 0, len(res.Timestamps))

	for i := range res.Timestamps {
		dp := datapointCBR{
			Timestamp: res.Timestamps[i].UTC(),
			Unit:      "Count",
		}

		v := res.Values[i]

		switch stat {
		case "Sum":
			dp.Sum = v
		case "Minimum":
			dp.Minimum = v
		case "Maximum":
			dp.Maximum = v
		case "SampleCount":
			dp.SampleCount = v
		default:
			dp.Average = v
		}

		out = append(out, dp)
	}

	return out
}
