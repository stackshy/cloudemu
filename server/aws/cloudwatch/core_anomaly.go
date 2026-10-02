package cloudwatch

import (
	"context"
	"regexp"
	"strconv"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
)

// The cores in this file hold the PutAnomalyDetector, DescribeAnomalyDetectors
// and DeleteAnomalyDetector logic. The query and the CBOR codecs both call them.

// anomalyDetectorStore is the AWS-local capability behind the anomaly
// detector operations.
type anomalyDetectorStore interface {
	PutAnomalyDetector(ctx context.Context, d mondriver.AnomalyDetector) error
	DescribeAnomalyDetectors(ctx context.Context) ([]mondriver.AnomalyDetector, error)
	DeleteAnomalyDetector(ctx context.Context, d mondriver.AnomalyDetector) error
}

// Anomaly detector types for DescribeAnomalyDetectors.
const (
	detectorTypeSingle = "SINGLE_METRIC"
	detectorTypeMath   = "METRIC_MATH"
)

// Limits from the API reference.
const (
	maxDetectorDimensions     = 30
	maxExcludedTimeRanges     = 10
	maxDetectorTypes          = 2
	maxDetectorPageSize       = 100
	maxMetricTimezoneLength   = 50
	errUnknownOperation       = "UnknownOperationException"
	errMsgDetectorDoesntExist = "The anomaly detector does not exist."
)

// detectorStatPattern is the part of the Stat pattern of the API reference
// that covers the standard statistics, percentiles and trimmed statistics.
var detectorStatPattern = regexp.MustCompile(
	`^((SampleCount|Average|Sum|Minimum|Maximum|IQM|(p|tc|tm|ts|wm)(\d{1,2}(\.\d{0,10})?|100))(_E|_L|_H)?|(TM|TC|TS|WM|PR)\(.+\))$`)

type singleMetricAnomalyDetectorCBR struct {
	AccountID  string         `cbor:"AccountId,omitempty"`
	Namespace  string         `cbor:"Namespace,omitempty"`
	MetricName string         `cbor:"MetricName,omitempty"`
	Dimensions []dimensionCBR `cbor:"Dimensions,omitempty"`
	Stat       string         `cbor:"Stat,omitempty"`
}

type metricMathAnomalyDetectorCBR struct {
	MetricDataQueries []metricDataQueryCBR `cbor:"MetricDataQueries,omitempty"`
}

type timeRangeCBR struct {
	StartTime *time.Time `cbor:"StartTime,omitempty"`
	EndTime   *time.Time `cbor:"EndTime,omitempty"`
}

type anomalyDetectorConfigurationCBR struct {
	ExcludedTimeRanges []timeRangeCBR `cbor:"ExcludedTimeRanges"`
	MetricTimezone     string         `cbor:"MetricTimezone,omitempty"`
}

type metricCharacteristicsCBR struct {
	PeriodicSpikes *bool `cbor:"PeriodicSpikes,omitempty"`
}

// anomalyDetectorInput is the shared PutAnomalyDetector and
// DeleteAnomalyDetector request. Delete ignores Configuration and
// MetricCharacteristics.
type anomalyDetectorInput struct {
	Namespace                   string                           `cbor:"Namespace,omitempty"`
	MetricName                  string                           `cbor:"MetricName,omitempty"`
	Dimensions                  []dimensionCBR                   `cbor:"Dimensions,omitempty"`
	Stat                        string                           `cbor:"Stat,omitempty"`
	SingleMetricAnomalyDetector *singleMetricAnomalyDetectorCBR  `cbor:"SingleMetricAnomalyDetector,omitempty"`
	MetricMathAnomalyDetector   *metricMathAnomalyDetectorCBR    `cbor:"MetricMathAnomalyDetector,omitempty"`
	Configuration               *anomalyDetectorConfigurationCBR `cbor:"Configuration,omitempty"`
	MetricCharacteristics       *metricCharacteristicsCBR        `cbor:"MetricCharacteristics,omitempty"`
}

// describeAnomalyDetectorsInput is the shared DescribeAnomalyDetectors request.
type describeAnomalyDetectorsInput struct {
	Namespace            string         `cbor:"Namespace,omitempty"`
	MetricName           string         `cbor:"MetricName,omitempty"`
	Dimensions           []dimensionCBR `cbor:"Dimensions,omitempty"`
	AnomalyDetectorTypes []string       `cbor:"AnomalyDetectorTypes,omitempty"`
	MaxResults           *int           `cbor:"MaxResults,omitempty"`
	NextToken            string         `cbor:"NextToken,omitempty"`
}

type describeAnomalyDetectorsResult struct {
	Detectors []mondriver.AnomalyDetector
	NextToken string
}

func (h *Handler) anomalyStore() (anomalyDetectorStore, error) {
	store, ok := h.monitoring.(anomalyDetectorStore)
	if !ok {
		return nil, newWireError(errUnknownOperation, "anomaly detectors are not supported")
	}

	return store, nil
}

func (h *Handler) putAnomalyDetectorCore(ctx context.Context, in *anomalyDetectorInput) error {
	store, err := h.anomalyStore()
	if err != nil {
		return err
	}

	d, err := resolveDetector(in)
	if err != nil {
		return err
	}

	if err := applyDetectorConfiguration(&d, in); err != nil {
		return err
	}

	return store.PutAnomalyDetector(ctx, d)
}

func (h *Handler) deleteAnomalyDetectorCore(ctx context.Context, in *anomalyDetectorInput) error {
	store, err := h.anomalyStore()
	if err != nil {
		return err
	}

	d, err := resolveDetector(in)
	if err != nil {
		return err
	}

	if err := store.DeleteAnomalyDetector(ctx, d); err != nil {
		if cerrors.IsNotFound(err) {
			return newNotFoundError(errResourceNotFoundException, errMsgDetectorDoesntExist)
		}

		return err
	}

	return nil
}

func (h *Handler) describeAnomalyDetectorsCore(
	ctx context.Context, in *describeAnomalyDetectorsInput,
) (describeAnomalyDetectorsResult, error) {
	store, err := h.anomalyStore()
	if err != nil {
		return describeAnomalyDetectorsResult{}, err
	}

	types, size, offset, err := describeDetectorParams(in)
	if err != nil {
		return describeAnomalyDetectorsResult{}, err
	}

	all, err := store.DescribeAnomalyDetectors(ctx)
	if err != nil {
		return describeAnomalyDetectorsResult{}, err
	}

	wantDims := toDimensionMap(in.Dimensions)
	matched := make([]mondriver.AnomalyDetector, 0, len(all))

	for i := range all {
		if detectorMatches(&all[i], in, wantDims, types) {
			matched = append(matched, all[i])
		}
	}

	from, to, next := pageWindow(len(matched), offset, size)
	res := describeAnomalyDetectorsResult{Detectors: matched[from:to]}

	if next > 0 {
		res.NextToken = encodeOffsetToken(next)
	}

	return res, nil
}

// describeDetectorParams validates the type list and the paging inputs.
func describeDetectorParams(in *describeAnomalyDetectorsInput) (types map[string]bool, size, offset int, err error) {
	if len(in.AnomalyDetectorTypes) > maxDetectorTypes {
		return nil, 0, 0, newWireError(errInvalidParameterValue, "AnomalyDetectorTypes can have at most 2 members.")
	}

	types = map[string]bool{}

	for _, t := range in.AnomalyDetectorTypes {
		if t != detectorTypeSingle && t != detectorTypeMath {
			return nil, 0, 0, newWireError(errInvalidParameterValue, "The value "+t+
				" for parameter AnomalyDetectorTypes is not valid. Valid values are SINGLE_METRIC and METRIC_MATH.")
		}

		types[t] = true
	}

	if len(types) == 0 {
		types[detectorTypeSingle] = true
	}

	size = maxDetectorPageSize

	if in.MaxResults != nil {
		size = *in.MaxResults
		if size < 1 || size > maxDetectorPageSize {
			return nil, 0, 0, newWireError(errInvalidParameterValue, "The value "+strconv.Itoa(size)+
				" for parameter MaxResults is not valid. It must be between 1 and 100.")
		}
	}

	offset, err = offsetFromToken(in.NextToken, errInvalidNextToken)

	return types, size, offset, err
}

// detectorMatches applies the filters. Namespace, MetricName and Dimensions
// only narrow single-metric detectors. A detector matches a dimension filter
// when it has every filter dimension, maybe among others: the API returns
// all the models "of multiple metrics that have these dimensions".
func detectorMatches(
	d *mondriver.AnomalyDetector, in *describeAnomalyDetectorsInput, wantDims map[string]string, types map[string]bool,
) bool {
	if len(d.Metrics) > 0 {
		return types[detectorTypeMath]
	}

	if !types[detectorTypeSingle] {
		return false
	}

	if !optionalMatch(in.Namespace, d.Namespace) || !optionalMatch(in.MetricName, d.MetricName) {
		return false
	}

	for k, v := range wantDims {
		if have, ok := d.Dimensions[k]; !ok || have != v {
			return false
		}
	}

	return true
}

// resolveDetector reads the detector identity from the one form the request
// uses: the legacy top-level fields, SingleMetricAnomalyDetector or
// MetricMathAnomalyDetector.
func resolveDetector(in *anomalyDetectorInput) (mondriver.AnomalyDetector, error) {
	legacy := in.Namespace != "" || in.MetricName != "" || in.Stat != "" || len(in.Dimensions) > 0

	forms := 0

	for _, set := range []bool{legacy, in.SingleMetricAnomalyDetector != nil, in.MetricMathAnomalyDetector != nil} {
		if set {
			forms++
		}
	}

	switch {
	case forms > 1:
		return mondriver.AnomalyDetector{}, newWireError(errInvalidParameterCombo,
			"Use only one of SingleMetricAnomalyDetector, MetricMathAnomalyDetector, or the Namespace, MetricName, Dimensions and Stat parameters.")
	case forms == 0:
		return mondriver.AnomalyDetector{}, newWireError(errMissingParameter,
			"You must specify either SingleMetricAnomalyDetector or MetricMathAnomalyDetector.")
	case in.MetricMathAnomalyDetector != nil:
		return mathDetector(in.MetricMathAnomalyDetector.MetricDataQueries)
	}

	single := in.SingleMetricAnomalyDetector
	if single == nil {
		single = &singleMetricAnomalyDetectorCBR{Namespace: in.Namespace, MetricName: in.MetricName, Dimensions: in.Dimensions, Stat: in.Stat}
	}

	return singleDetector(single)
}

func singleDetector(s *singleMetricAnomalyDetectorCBR) (mondriver.AnomalyDetector, error) {
	for _, f := range []struct{ name, v string }{{"Namespace", s.Namespace}, {"MetricName", s.MetricName}, {"Stat", s.Stat}} {
		if f.v == "" {
			return mondriver.AnomalyDetector{}, newWireError(errMissingParameter, "The parameter "+f.name+" is required.")
		}
	}

	if !detectorStatPattern.MatchString(s.Stat) {
		return mondriver.AnomalyDetector{}, newWireError(errInvalidParameterValue, "The value "+s.Stat+" for parameter Stat is not valid.")
	}

	if len(s.Dimensions) > maxDetectorDimensions {
		return mondriver.AnomalyDetector{}, newWireError(errInvalidParameterValue, "A detector can have at most 30 dimensions.")
	}

	return mondriver.AnomalyDetector{
		AccountID: s.AccountID, Namespace: s.Namespace, MetricName: s.MetricName,
		Dimensions: toDimensionMap(s.Dimensions), Stat: s.Stat,
	}, nil
}

// mathDetector checks a metric-math detector's query list the way
// PutMetricAlarm checks an alarm's.
func mathDetector(in []metricDataQueryCBR) (mondriver.AnomalyDetector, error) {
	queries := toDriverQueries(in)
	if len(queries) == 0 {
		return mondriver.AnomalyDetector{}, newWireError(errMissingParameter, "The parameter MetricDataQueries is required.")
	}

	if err := validateQueryCounts(queries); err != nil {
		return mondriver.AnomalyDetector{}, err
	}

	ids, err := metricQueryIDs(queries)
	if err != nil {
		return mondriver.AnomalyDetector{}, err
	}

	if len(metricmath.Watched(queries, "")) != 1 {
		return mondriver.AnomalyDetector{}, newWireError(errValidation, "Exactly one element of the metrics list should return data.")
	}

	for i := range queries {
		if _, isBand := metricmath.BandInput(queries[i].Expression); isBand {
			return mondriver.AnomalyDetector{}, newWireError(errValidation,
				"Error in expression '"+queries[i].ID+"': ANOMALY_DETECTION_BAND cannot be used in an anomaly detector.")
		}
	}

	if err := expressionRefsKnown(queries, ids); err != nil {
		return mondriver.AnomalyDetector{}, err
	}

	if id, ok := metricmath.Cycle(queries); ok {
		return mondriver.AnomalyDetector{}, newWireError(errValidation, "Error in expression '"+id+"': Circular dependency in the metrics list.")
	}

	return mondriver.AnomalyDetector{Metrics: queries}, nil
}

// applyDetectorConfiguration checks and copies Configuration and
// MetricCharacteristics onto d.
func applyDetectorConfiguration(d *mondriver.AnomalyDetector, in *anomalyDetectorInput) error {
	if mc := in.MetricCharacteristics; mc != nil && mc.PeriodicSpikes != nil {
		v := *mc.PeriodicSpikes
		d.PeriodicSpikes = &v
	}

	cfg := in.Configuration
	if cfg == nil {
		return nil
	}

	if len(cfg.ExcludedTimeRanges) > maxExcludedTimeRanges {
		return newWireError(errLimitExceeded, "You can specify as many as 10 excluded time ranges.")
	}

	if len(cfg.MetricTimezone) > maxMetricTimezoneLength {
		return newWireError(errInvalidParameterValue, "The value for parameter MetricTimezone is too long.")
	}

	d.MetricTimezone = cfg.MetricTimezone

	for _, r := range cfg.ExcludedTimeRanges {
		if r.StartTime == nil || r.EndTime == nil || !r.StartTime.Before(*r.EndTime) {
			return newWireError(errInvalidParameterValue, "Each excluded time range needs a StartTime before its EndTime.")
		}

		d.ExcludedTimeRanges = append(d.ExcludedTimeRanges, mondriver.TimeRange{StartTime: r.StartTime.UTC(), EndTime: r.EndTime.UTC()})
	}

	return nil
}
