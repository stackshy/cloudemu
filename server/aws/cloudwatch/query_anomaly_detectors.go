package cloudwatch

// This file is the query-protocol codec of the anomaly detector operations.
// The logic is in core_anomaly.go.

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
	"time"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Form prefixes of the nested anomaly detector structures.
const (
	formSingleDetector = "SingleMetricAnomalyDetector."
	formMathDetector   = "MetricMathAnomalyDetector."
	formConfiguration  = "Configuration."
)

func (h *Handler) queryPutAnomalyDetector(w http.ResponseWriter, r *http.Request) {
	in := queryAnomalyDetectorInput(r)

	if err := h.putAnomalyDetectorCore(r.Context(), &in); err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "PutAnomalyDetectorResponse", emptyQueryResult("PutAnomalyDetectorResult"))
}

func (h *Handler) queryDeleteAnomalyDetector(w http.ResponseWriter, r *http.Request) {
	in := queryAnomalyDetectorInput(r)

	if err := h.deleteAnomalyDetectorCore(r.Context(), &in); err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "DeleteAnomalyDetectorResponse", emptyQueryResult("DeleteAnomalyDetectorResult"))
}

func (h *Handler) queryDescribeAnomalyDetectors(w http.ResponseWriter, r *http.Request) {
	in := describeAnomalyDetectorsInput{
		Namespace:            r.Form.Get("Namespace"),
		MetricName:           r.Form.Get("MetricName"),
		Dimensions:           dimsToCBR(queryDimensions(r, "Dimensions.member.")),
		AnomalyDetectorTypes: queryStringList(r, "AnomalyDetectorTypes.member."),
		NextToken:            r.Form.Get("NextToken"),
	}

	if r.Form.Has("MaxResults") {
		n, _ := strconv.Atoi(r.Form.Get("MaxResults"))
		in.MaxResults = &n
	}

	res, err := h.describeAnomalyDetectorsCore(r.Context(), &in)
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	out := describeAnomalyDetectorsResultXML{AnomalyDetectors: make([]anomalyDetectorXML, 0, len(res.Detectors)), NextToken: res.NextToken}
	for i := range res.Detectors {
		out.AnomalyDetectors = append(out.AnomalyDetectors, toAnomalyDetectorXML(&res.Detectors[i]))
	}

	writeQueryResponse(w, "DescribeAnomalyDetectorsResponse", out)
}

// queryAnomalyDetectorInput decodes the PutAnomalyDetector and
// DeleteAnomalyDetector form. A nested structure is set when any of its
// fields is in the form, so an empty one still counts for the form rules.
func queryAnomalyDetectorInput(r *http.Request) anomalyDetectorInput {
	in := anomalyDetectorInput{
		Namespace:  r.Form.Get("Namespace"),
		MetricName: r.Form.Get("MetricName"),
		Stat:       r.Form.Get("Stat"),
		Dimensions: dimsToCBR(queryDimensions(r, "Dimensions.member.")),
	}

	if formHasPrefix(r, formSingleDetector) {
		in.SingleMetricAnomalyDetector = &singleMetricAnomalyDetectorCBR{
			AccountID:  r.Form.Get(formSingleDetector + "AccountId"),
			Namespace:  r.Form.Get(formSingleDetector + "Namespace"),
			MetricName: r.Form.Get(formSingleDetector + "MetricName"),
			Stat:       r.Form.Get(formSingleDetector + "Stat"),
			Dimensions: dimsToCBR(queryDimensions(r, formSingleDetector+"Dimensions.member.")),
		}
	}

	if formHasPrefix(r, formMathDetector) {
		in.MetricMathAnomalyDetector = &metricMathAnomalyDetectorCBR{
			MetricDataQueries: queryMetricDataQueries(r, formMathDetector+"MetricDataQueries"),
		}
	}

	if formHasPrefix(r, formConfiguration) {
		in.Configuration = &anomalyDetectorConfigurationCBR{
			ExcludedTimeRanges: queryTimeRanges(r, formConfiguration+"ExcludedTimeRanges.member."),
			MetricTimezone:     r.Form.Get(formConfiguration + "MetricTimezone"),
		}
	}

	if spikes := queryOptBool(r, "MetricCharacteristics.PeriodicSpikes"); spikes != nil {
		in.MetricCharacteristics = &metricCharacteristicsCBR{PeriodicSpikes: spikes}
	}

	return in
}

func formHasPrefix(r *http.Request, prefix string) bool {
	for k := range r.Form {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}

	return false
}

// queryTimeRanges reads a Range list. A time that does not parse is left nil,
// which the core rejects.
func queryTimeRanges(r *http.Request, prefix string) []timeRangeCBR {
	var out []timeRangeCBR

	for i := 1; ; i++ {
		p := prefix + strconv.Itoa(i) + "."
		if !r.Form.Has(p+"StartTime") && !r.Form.Has(p+"EndTime") {
			return out
		}

		out = append(out, timeRangeCBR{StartTime: queryTime(r.Form.Get(p + "StartTime")), EndTime: queryTime(r.Form.Get(p + "EndTime"))})
	}
}

func queryTime(raw string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil
	}

	return &t
}

type singleMetricAnomalyDetectorXML struct {
	AccountID  string         `xml:"AccountId,omitempty"`
	Namespace  string         `xml:"Namespace"`
	MetricName string         `xml:"MetricName"`
	Dimensions []dimensionXML `xml:"Dimensions>member,omitempty"`
	Stat       string         `xml:"Stat"`
}

type metricMathAnomalyDetectorXML struct {
	MetricDataQueries []metricDataQueryXML `xml:"MetricDataQueries>member"`
}

type timeRangeXML struct {
	StartTime string `xml:"StartTime"`
	EndTime   string `xml:"EndTime"`
}

type timeRangesXML struct {
	Members []timeRangeXML `xml:"member"`
}

type anomalyConfigurationXML struct {
	ExcludedTimeRanges timeRangesXML `xml:"ExcludedTimeRanges"`
	MetricTimezone     string        `xml:"MetricTimezone,omitempty"`
}

type metricCharacteristicsXML struct {
	PeriodicSpikes bool `xml:"PeriodicSpikes"`
}

type anomalyDetectorXML struct {
	Namespace                   string                          `xml:"Namespace,omitempty"`
	MetricName                  string                          `xml:"MetricName,omitempty"`
	Dimensions                  []dimensionXML                  `xml:"Dimensions>member,omitempty"`
	Stat                        string                          `xml:"Stat,omitempty"`
	SingleMetricAnomalyDetector *singleMetricAnomalyDetectorXML `xml:"SingleMetricAnomalyDetector,omitempty"`
	MetricMathAnomalyDetector   *metricMathAnomalyDetectorXML   `xml:"MetricMathAnomalyDetector,omitempty"`
	Configuration               anomalyConfigurationXML         `xml:"Configuration"`
	MetricCharacteristics       *metricCharacteristicsXML       `xml:"MetricCharacteristics,omitempty"`
	StateValue                  string                          `xml:"StateValue,omitempty"`
}

type describeAnomalyDetectorsResultXML struct {
	XMLName          xml.Name             `xml:"DescribeAnomalyDetectorsResult"`
	AnomalyDetectors []anomalyDetectorXML `xml:"AnomalyDetectors>member"`
	NextToken        string               `xml:"NextToken,omitempty"`
}

// toAnomalyDetectorXML mirrors toAnomalyDetectorCBR.
func toAnomalyDetectorXML(d *mondriver.AnomalyDetector) anomalyDetectorXML {
	out := anomalyDetectorXML{StateValue: d.StateValue, Configuration: anomalyConfigurationXML{MetricTimezone: d.MetricTimezone}}

	for _, r := range d.ExcludedTimeRanges {
		out.Configuration.ExcludedTimeRanges.Members = append(out.Configuration.ExcludedTimeRanges.Members, timeRangeXML{
			StartTime: r.StartTime.UTC().Format(time.RFC3339), EndTime: r.EndTime.UTC().Format(time.RFC3339),
		})
	}

	if d.PeriodicSpikes != nil {
		out.MetricCharacteristics = &metricCharacteristicsXML{PeriodicSpikes: *d.PeriodicSpikes}
	}

	if len(d.Metrics) > 0 {
		out.MetricMathAnomalyDetector = &metricMathAnomalyDetectorXML{MetricDataQueries: toQueriesXML(d.Metrics)}

		return out
	}

	dims := dimsToXML(d.Dimensions)
	out.Namespace, out.MetricName, out.Dimensions, out.Stat = d.Namespace, d.MetricName, dims, d.Stat
	out.SingleMetricAnomalyDetector = &singleMetricAnomalyDetectorXML{
		AccountID: d.AccountID, Namespace: d.Namespace, MetricName: d.MetricName, Dimensions: dims, Stat: d.Stat,
	}

	return out
}
