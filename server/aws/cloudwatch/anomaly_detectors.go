package cloudwatch

// This file is the rpc-v2-cbor codec of the anomaly detector operations. The
// logic is in core_anomaly.go.

import (
	"net/http"

	"github.com/fxamacker/cbor/v2"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

type anomalyDetectorCBR struct {
	Namespace                   string                           `cbor:"Namespace,omitempty"`
	MetricName                  string                           `cbor:"MetricName,omitempty"`
	Dimensions                  []dimensionCBR                   `cbor:"Dimensions,omitempty"`
	Stat                        string                           `cbor:"Stat,omitempty"`
	SingleMetricAnomalyDetector *singleMetricAnomalyDetectorCBR  `cbor:"SingleMetricAnomalyDetector,omitempty"`
	MetricMathAnomalyDetector   *metricMathAnomalyDetectorCBR    `cbor:"MetricMathAnomalyDetector,omitempty"`
	Configuration               *anomalyDetectorConfigurationCBR `cbor:"Configuration,omitempty"`
	MetricCharacteristics       *metricCharacteristicsCBR        `cbor:"MetricCharacteristics,omitempty"`
	StateValue                  string                           `cbor:"StateValue,omitempty"`
}

type describeAnomalyDetectorsOutput struct {
	AnomalyDetectors []anomalyDetectorCBR `cbor:"AnomalyDetectors"`
	NextToken        string               `cbor:"NextToken,omitempty"`
}

func (h *Handler) putAnomalyDetector(w http.ResponseWriter, r *http.Request, body []byte) {
	var in anomalyDetectorInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	if err := h.putAnomalyDetectorCore(r.Context(), &in); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

func (h *Handler) deleteAnomalyDetector(w http.ResponseWriter, r *http.Request, body []byte) {
	var in anomalyDetectorInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	if err := h.deleteAnomalyDetectorCore(r.Context(), &in); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

func (h *Handler) describeAnomalyDetectors(w http.ResponseWriter, r *http.Request, body []byte) {
	var in describeAnomalyDetectorsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	res, err := h.describeAnomalyDetectorsCore(r.Context(), &in)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	out := describeAnomalyDetectorsOutput{AnomalyDetectors: make([]anomalyDetectorCBR, 0, len(res.Detectors)), NextToken: res.NextToken}
	for i := range res.Detectors {
		out.AnomalyDetectors = append(out.AnomalyDetectors, toAnomalyDetectorCBR(&res.Detectors[i]))
	}

	writeCBORResponse(w, out)
}

// toAnomalyDetectorCBR renders a detector. A single-metric detector fills
// both the legacy top-level fields and SingleMetricAnomalyDetector.
// Configuration is always present, as on AWS.
func toAnomalyDetectorCBR(d *mondriver.AnomalyDetector) anomalyDetectorCBR {
	out := anomalyDetectorCBR{
		StateValue: d.StateValue,
		Configuration: &anomalyDetectorConfigurationCBR{
			ExcludedTimeRanges: toTimeRangesCBR(d.ExcludedTimeRanges), MetricTimezone: d.MetricTimezone,
		},
	}

	if d.PeriodicSpikes != nil {
		v := *d.PeriodicSpikes
		out.MetricCharacteristics = &metricCharacteristicsCBR{PeriodicSpikes: &v}
	}

	if len(d.Metrics) > 0 {
		out.MetricMathAnomalyDetector = &metricMathAnomalyDetectorCBR{MetricDataQueries: toQueriesCBR(d.Metrics)}

		return out
	}

	dims := dimsToCBR(d.Dimensions)
	out.Namespace, out.MetricName, out.Dimensions, out.Stat = d.Namespace, d.MetricName, dims, d.Stat
	out.SingleMetricAnomalyDetector = &singleMetricAnomalyDetectorCBR{
		AccountID: d.AccountID, Namespace: d.Namespace, MetricName: d.MetricName, Dimensions: dims, Stat: d.Stat,
	}

	return out
}

func toTimeRangesCBR(in []mondriver.TimeRange) []timeRangeCBR {
	out := make([]timeRangeCBR, 0, len(in))

	for _, r := range in {
		start, end := r.StartTime.UTC(), r.EndTime.UTC()
		out = append(out, timeRangeCBR{StartTime: &start, EndTime: &end})
	}

	return out
}
