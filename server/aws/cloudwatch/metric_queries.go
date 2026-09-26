package cloudwatch

import (
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
)

// This file converts MetricDataQuery lists between the wire shapes and the
// driver type. GetMetricData and PutMetricAlarm read them. DescribeAlarms
// writes them back for a metric-math alarm, always with ReturnData set.

func toDriverQueries(in []metricDataQueryCBR) []mondriver.MetricDataQuery {
	if len(in) == 0 {
		return nil
	}

	out := make([]mondriver.MetricDataQuery, 0, len(in))

	for i := range in {
		q := &in[i]
		dq := mondriver.MetricDataQuery{
			ID: q.ID, Expression: q.Expression, Label: q.Label, Period: q.Period, AccountID: q.AccountID,
		}

		if q.ReturnData != nil {
			v := *q.ReturnData
			dq.ReturnData = &v
		}

		if ms := q.MetricStat; ms != nil {
			dq.MetricStat = &mondriver.MetricStat{
				Namespace:  ms.Metric.Namespace,
				MetricName: ms.Metric.MetricName,
				Dimensions: toDimensionMap(ms.Metric.Dimensions),
				Period:     ms.Period,
				Stat:       ms.Stat,
				Unit:       ms.Unit,
			}
		}

		out = append(out, dq)
	}

	return out
}

func toQueriesCBR(in []mondriver.MetricDataQuery) []metricDataQueryCBR {
	if len(in) == 0 {
		return nil
	}

	out := make([]metricDataQueryCBR, 0, len(in))

	for i := range in {
		q := &in[i]
		ret := metricmath.ReturnsData(q)
		c := metricDataQueryCBR{
			ID: q.ID, Expression: q.Expression, Label: q.Label, Period: q.Period, AccountID: q.AccountID, ReturnData: &ret,
		}

		if ms := q.MetricStat; ms != nil {
			c.MetricStat = &metricStatCBR{
				Metric: metricCBRRef{Namespace: ms.Namespace, MetricName: ms.MetricName, Dimensions: dimsToCBR(ms.Dimensions)},
				Period: ms.Period, Stat: ms.Stat, Unit: ms.Unit,
			}
		}

		out = append(out, c)
	}

	return out
}

type metricStatXML struct {
	Metric metricMemberXML `xml:"Metric"`
	Period int             `xml:"Period"`
	Stat   string          `xml:"Stat"`
	Unit   string          `xml:"Unit,omitempty"`
}

type metricDataQueryXML struct {
	ID         string         `xml:"Id"`
	MetricStat *metricStatXML `xml:"MetricStat,omitempty"`
	Expression string         `xml:"Expression,omitempty"`
	Label      string         `xml:"Label,omitempty"`
	ReturnData bool           `xml:"ReturnData"`
	Period     int            `xml:"Period,omitempty"`
	AccountID  string         `xml:"AccountId,omitempty"`
}

func toQueriesXML(in []mondriver.MetricDataQuery) []metricDataQueryXML {
	if len(in) == 0 {
		return nil
	}

	out := make([]metricDataQueryXML, 0, len(in))

	for i := range in {
		q := &in[i]
		x := metricDataQueryXML{
			ID: q.ID, Expression: q.Expression, Label: q.Label, ReturnData: metricmath.ReturnsData(q), Period: q.Period, AccountID: q.AccountID,
		}

		if ms := q.MetricStat; ms != nil {
			x.MetricStat = &metricStatXML{
				Metric: metricMemberXML{Namespace: ms.Namespace, MetricName: ms.MetricName, Dimensions: dimsToXML(ms.Dimensions)},
				Period: ms.Period, Stat: ms.Stat, Unit: ms.Unit,
			}
		}

		out = append(out, x)
	}

	return out
}
