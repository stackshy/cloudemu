package cloudwatch

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"time"
)

// Query-protocol codecs for GetMetricData and DescribeAlarmsForMetric. The
// aws CLI and botocore speak this protocol. The logic lives in the cores.

func (h *Handler) queryGetMetricData(w http.ResponseWriter, r *http.Request) {
	in := getMetricDataInput{
		MetricDataQueries: queryMetricDataQueries(r, "MetricDataQueries"),
		StartTime:         queryOptTime(r, "StartTime"),
		EndTime:           queryOptTime(r, "EndTime"),
		NextToken:         r.Form.Get("NextToken"),
		ScanBy:            r.Form.Get("ScanBy"),
	}
	in.MaxDatapoints, _ = strconv.Atoi(r.Form.Get("MaxDatapoints"))

	res, err := h.getMetricDataCore(r.Context(), &in)
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	rows := make([]metricDataResultXML, 0, len(res.Rows))

	for i := range res.Rows {
		row := &res.Rows[i]

		x := metricDataResultXML{ID: row.ID, Label: row.Label, StatusCode: row.StatusCode, Values: row.Values}
		for _, ts := range row.Timestamps {
			x.Timestamps = append(x.Timestamps, ts.Format(time.RFC3339))
		}

		rows = append(rows, x)
	}

	writeQueryResponse(w, "GetMetricDataResponse", getMetricDataResultXML{MetricDataResults: rows, NextToken: res.NextToken})
}

// queryMetricDataQueries reads <prefix>.member.N MetricDataQuery entries. The
// list ends at the first entry with no Id.
func queryMetricDataQueries(r *http.Request, prefix string) []metricDataQueryCBR {
	var out []metricDataQueryCBR

	for i := 1; ; i++ {
		p := prefix + ".member." + strconv.Itoa(i) + "."

		id := r.Form.Get(p + "Id")
		if id == "" {
			return out
		}

		q := metricDataQueryCBR{
			ID:         id,
			Label:      r.Form.Get(p + "Label"),
			Expression: r.Form.Get(p + "Expression"),
			ReturnData: queryOptBool(r, p+"ReturnData"),
		}

		if name := r.Form.Get(p + "MetricStat.Metric.MetricName"); name != "" {
			period, _ := strconv.Atoi(r.Form.Get(p + "MetricStat.Period"))
			q.MetricStat = &metricStatCBR{
				Metric: metricCBRRef{
					Namespace:  r.Form.Get(p + "MetricStat.Metric.Namespace"),
					MetricName: name,
					Dimensions: dimsToCBR(queryDimensions(r, p+"MetricStat.Metric.Dimensions.member.")),
				},
				Period: period,
				Stat:   r.Form.Get(p + "MetricStat.Stat"),
				Unit:   r.Form.Get(p + "MetricStat.Unit"),
			}
		}

		out = append(out, q)
	}
}

func (h *Handler) queryDescribeAlarmsForMetric(w http.ResponseWriter, r *http.Request) {
	in := describeAlarmsForMetricInput{
		Namespace:         r.Form.Get("Namespace"),
		MetricName:        r.Form.Get("MetricName"),
		Dimensions:        dimsToCBR(queryDimensions(r, "Dimensions.member.")),
		Statistic:         r.Form.Get("Statistic"),
		ExtendedStatistic: r.Form.Get("ExtendedStatistic"),
		Unit:              r.Form.Get("Unit"),
	}
	in.Period, _ = strconv.Atoi(r.Form.Get("Period"))

	alarms, err := h.describeAlarmsForMetricCore(r.Context(), &in)
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	members := make([]alarmMemberXML, 0, len(alarms))
	for i := range alarms {
		members = append(members, toAlarmMemberXML(&alarms[i]))
	}

	writeQueryResponse(w, "DescribeAlarmsForMetricResponse", describeAlarmsForMetricResultXML{MetricAlarms: members})
}

type metricDataResultXML struct {
	ID         string    `xml:"Id"`
	Label      string    `xml:"Label,omitempty"`
	StatusCode string    `xml:"StatusCode"`
	Timestamps []string  `xml:"Timestamps>member"`
	Values     []float64 `xml:"Values>member"`
}

type getMetricDataResultXML struct {
	XMLName           xml.Name              `xml:"GetMetricDataResult"`
	MetricDataResults []metricDataResultXML `xml:"MetricDataResults>member"`
	NextToken         string                `xml:"NextToken,omitempty"`
	Messages          struct{}              `xml:"Messages"`
}

type describeAlarmsForMetricResultXML struct {
	XMLName      xml.Name         `xml:"DescribeAlarmsForMetricResult"`
	MetricAlarms []alarmMemberXML `xml:"MetricAlarms>member"`
}
