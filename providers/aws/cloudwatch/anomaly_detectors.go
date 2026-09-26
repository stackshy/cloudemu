package cloudwatch

import (
	"context"
	"encoding/json"
	"maps"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
)

// Anomaly detectors are an AWS-local capability. A detector is stored under a
// key built from the metric it models. An anomaly alarm that is evaluated
// creates its detector when it is missing, as AWS does.

// Detector training states.
const (
	detectorPendingTraining     = "PENDING_TRAINING"
	detectorTrainedInsufficient = "TRAINED_INSUFFICIENT_DATA"
	detectorTrained             = "TRAINED"
	detectorStatePeriodSeconds  = 60
	detectorSingleKeyPrefix     = "S|"
	detectorMathKeyPrefix       = "M|"
	detectorKeySeparator        = "\x00"
	anomalyTrainingSettle       = 2 * time.Second
)

// anomalyDetectorData is one stored detector. Values are never changed in
// place. A change stores a new value.
type anomalyDetectorData struct {
	Detector driver.AnomalyDetector
	// CreatedAt starts the PENDING_TRAINING settle window.
	CreatedAt time.Time
	// TrainedAt is when the detector was first seen TRAINED.
	TrainedAt time.Time
}

// detectorKey identifies the metric a detector models. A math detector is
// keyed by its query list without labels, and by the entry it trains on.
func detectorKey(d *driver.AnomalyDetector) string {
	if len(d.Metrics) == 0 {
		return detectorSingleKeyPrefix + d.Namespace + detectorKeySeparator + d.MetricName +
			detectorKeySeparator + canonicalDims(d.Dimensions) + detectorKeySeparator + d.Stat
	}

	type keyQuery struct {
		ID, Expression, AccountID string
		Period                    int
		MetricStat                *driver.MetricStat
	}

	qs := make([]keyQuery, 0, len(d.Metrics))

	for i := range d.Metrics {
		q := &d.Metrics[i]
		qs = append(qs, keyQuery{ID: q.ID, Expression: q.Expression, AccountID: q.AccountID, Period: q.Period, MetricStat: q.MetricStat})
	}

	// Marshaling plain structs and string maps cannot fail.
	b, _ := json.Marshal(qs)

	watched := ""
	if w := metricmath.Watched(d.Metrics, ""); len(w) == 1 {
		watched = w[0].ID
	}

	return detectorMathKeyPrefix + watched + detectorKeySeparator + string(b)
}

// cloneDetector returns a deep copy, so a stored detector shares nothing
// with its caller.
func cloneDetector(d *driver.AnomalyDetector) driver.AnomalyDetector {
	out := *d
	out.Dimensions = maps.Clone(d.Dimensions)
	out.Metrics = metricmath.Clone(d.Metrics)
	out.ExcludedTimeRanges = append([]driver.TimeRange(nil), d.ExcludedTimeRanges...)

	if d.PeriodicSpikes != nil {
		v := *d.PeriodicSpikes
		out.PeriodicSpikes = &v
	}

	return out
}

// PutAnomalyDetector creates a detector or replaces the configuration of the
// detector on the same metric.
//
//nolint:gocritic // hugeParam: the value form matches the other store methods.
func (m *Mock) PutAnomalyDetector(_ context.Context, d driver.AnomalyDetector) error {
	if len(d.Metrics) == 0 && (d.Namespace == "" || d.MetricName == "" || d.Stat == "") {
		return errors.Newf(errors.InvalidArgument, "a detector needs a namespace, metric name and stat, or metrics")
	}

	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	m.putDetectorLocked(&d, time.Time{})

	return nil
}

// putDetectorLocked is the one write path of the detector store. A new
// detector gets CreatedAt from the clock. An existing one keeps CreatedAt and
// TrainedAt, and trainedAt is recorded when none is set yet. The caller holds
// alarmMu.
func (m *Mock) putDetectorLocked(d *driver.AnomalyDetector, trainedAt time.Time) {
	key := detectorKey(d)
	rec := &anomalyDetectorData{Detector: cloneDetector(d), CreatedAt: m.opts.Clock.Now()}
	rec.Detector.StateValue = ""

	if old, ok := m.anomalyDetectors.Get(key); ok {
		rec.CreatedAt, rec.TrainedAt = old.CreatedAt, old.TrainedAt
	}

	if rec.TrainedAt.IsZero() {
		rec.TrainedAt = trainedAt
	}

	m.anomalyDetectors.Set(key, rec)
}

// ensureDetectorLocked creates d when no detector models its metric. The
// caller holds alarmMu.
func (m *Mock) ensureDetectorLocked(d *driver.AnomalyDetector) {
	if !m.anomalyDetectors.Has(detectorKey(d)) {
		m.putDetectorLocked(d, time.Time{})
	}
}

// ensureAlarmDetectorLocked creates the detector a band alarm reads from when
// it is missing. It does nothing for other alarms. The caller holds alarmMu.
func (m *Mock) ensureAlarmDetectorLocked(a *alarmData) {
	_, inputID, isBand := bandThreshold(a)
	if !isBand {
		return
	}

	if d, ok := detectorFor(a.Metrics, inputID); ok {
		m.ensureDetectorLocked(&d)
	}
}

// markTrainedLocked records the first time a detector is seen TRAINED. It does
// nothing when the detector changed or went away since old was read. The
// caller holds alarmMu.
func (m *Mock) markTrainedLocked(key string, old *anomalyDetectorData, at time.Time) {
	if cur, ok := m.anomalyDetectors.Get(key); ok && cur == old {
		m.putDetectorLocked(&cur.Detector, at)
	}
}

// DeleteAnomalyDetector removes the detector on d's metric.
//
//nolint:gocritic // hugeParam: the value form matches the other store methods.
func (m *Mock) DeleteAnomalyDetector(_ context.Context, d driver.AnomalyDetector) error {
	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	if !m.anomalyDetectors.Delete(detectorKey(&d)) {
		return errors.Newf(errors.NotFound, "the anomaly detector does not exist")
	}

	return nil
}

// DescribeAnomalyDetectors returns every detector with its training state,
// sorted by key. The state reads metric data, which is done outside alarmMu
// so a long read never blocks alarm evaluation.
func (m *Mock) DescribeAnomalyDetectors(_ context.Context) ([]driver.AnomalyDetector, error) {
	m.alarmMu.Lock()
	recs := m.anomalyDetectors.All()
	m.alarmMu.Unlock()

	keys := make([]string, 0, len(recs))
	for k := range recs {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	now := m.opts.Clock.Now()
	out := make([]driver.AnomalyDetector, 0, len(keys))

	for _, k := range keys {
		rec := recs[k]
		state, first := m.detectorState(rec, now)

		if first {
			m.alarmMu.Lock()
			m.markTrainedLocked(k, rec, now)
			m.alarmMu.Unlock()
		}

		d := cloneDetector(&rec.Detector)
		d.StateValue = state
		out = append(out, d)
	}

	return out, nil
}

// detectorState is the training state at now. first is true when the
// detector is TRAINED for the first time.
func (m *Mock) detectorState(rec *anomalyDetectorData, now time.Time) (state string, first bool) {
	window := settle.Pending(detectorPendingTraining, rec.CreatedAt, m.opts.SettleDuration(anomalyTrainingSettle))
	if !window.Settled(now) {
		return detectorPendingTraining, false
	}

	if metricmath.Trained(m.detectorPoints(&rec.Detector, now)) {
		return detectorTrained, rec.TrainedAt.IsZero()
	}

	if !rec.TrainedAt.IsZero() {
		return detectorTrainedInsufficient, false
	}

	return detectorPendingTraining, false
}

// detectorPoints counts the non-empty periods of the detector's series over
// the last training window. A single-metric detector has no period, so it is
// read at one minute.
func (m *Mock) detectorPoints(d *driver.AnomalyDetector, now time.Time) int {
	start, end := now.Add(-metricmath.TrainingWindow), now.Add(time.Nanosecond)

	if len(d.Metrics) == 0 {
		res := m.readMetric(&driver.GetMetricInput{
			Namespace: d.Namespace, MetricName: d.MetricName, Dimensions: d.Dimensions,
			StartTime: start, EndTime: end, Period: detectorStatePeriodSeconds, Stat: d.Stat,
		})

		return len(res.Values)
	}

	watched := metricmath.Watched(d.Metrics, "")
	if len(watched) != 1 {
		return 0
	}

	s, err := metricmath.New(d.Metrics, m.rangeFetcher(start, end)).Resolve(watched[0].ID)
	if err != nil {
		return 0
	}

	n := 0

	for _, ts := range s.Timestamps {
		if !ts.IsZero() {
			n++
		}
	}

	return n
}

// BandExclusions returns the excluded training ranges of the detector that
// models entry inputID of queries. It is nil when there is no such detector.
func (m *Mock) BandExclusions(queries []driver.MetricDataQuery, inputID string) []driver.TimeRange {
	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	return m.bandExclusionsLocked(queries, inputID)
}

// bandExclusionsLocked is BandExclusions for a caller that holds alarmMu.
func (m *Mock) bandExclusionsLocked(queries []driver.MetricDataQuery, inputID string) []driver.TimeRange {
	d, ok := detectorFor(queries, inputID)
	if !ok {
		return nil
	}

	rec, ok := m.anomalyDetectors.Get(detectorKey(&d))
	if !ok {
		return nil
	}

	return append([]driver.TimeRange(nil), rec.Detector.ExcludedTimeRanges...)
}

// detectorFor returns the detector that models entry inputID of queries. A
// metric entry maps to a single-metric detector. An expression maps to a
// math detector over the list without its band entries, trained on inputID.
func detectorFor(queries []driver.MetricDataQuery, inputID string) (driver.AnomalyDetector, bool) {
	var input *driver.MetricDataQuery

	for i := range queries {
		if queries[i].ID == inputID {
			input = &queries[i]
		}
	}

	switch {
	case input == nil:
		return driver.AnomalyDetector{}, false
	case input.MetricStat != nil:
		ms := input.MetricStat

		return driver.AnomalyDetector{
			AccountID: input.AccountID, Namespace: ms.Namespace, MetricName: ms.MetricName,
			Dimensions: maps.Clone(ms.Dimensions), Stat: ms.Stat,
		}, true
	default:
		return driver.AnomalyDetector{Metrics: detectorQueries(queries, inputID)}, true
	}
}

// detectorQueries copies queries without band entries. Only inputID returns
// data.
func detectorQueries(queries []driver.MetricDataQuery, inputID string) []driver.MetricDataQuery {
	out := make([]driver.MetricDataQuery, 0, len(queries))

	for _, q := range metricmath.Clone(queries) {
		if _, isBand := metricmath.BandInput(q.Expression); isBand {
			continue
		}

		ret := q.ID == inputID
		q.ReturnData = &ret
		out = append(out, q)
	}

	return out
}

// bandThreshold returns the band entry an anomaly alarm compares against and
// the entry the band reads. ok is false for any other alarm.
func bandThreshold(a *alarmData) (bandID, inputID string, ok bool) {
	if a.ThresholdMetricID == "" {
		return "", "", false
	}

	for i := range a.Metrics {
		if a.Metrics[i].ID != a.ThresholdMetricID {
			continue
		}

		inputID, ok = metricmath.BandInput(a.Metrics[i].Expression)

		return a.ThresholdMetricID, inputID, ok
	}

	return "", "", false
}
