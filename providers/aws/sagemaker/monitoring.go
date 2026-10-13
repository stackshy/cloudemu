package sagemaker

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

const (
	maxMonitoringNameLen = 63
	maxAnalysisWindow    = 24 * time.Hour
	execKeyLayout        = "20060102T150405.000000000"
	maxAlertBound        = 100
	hoursPerDay          = 24
)

var (
	monitoringNamePattern = regexp.MustCompile(`^[a-zA-Z0-9](-*[a-zA-Z0-9]){0,62}$`)
	scheduleExprPattern   = regexp.MustCompile(`^cron\(0 (\*|(\d{1,2})(?:/(\d{1,2}))?) \? \* \* \*\)$`)
	isoOffsetPattern      = regexp.MustCompile(`^(-?)P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)
)

func (m *Mock) monitoringARN(kind, name string) string {
	switch kind {
	case "schedule":
		return m.arn("monitoring-schedule/" + name)
	case driver.MonitoringDataQuality:
		return m.arn("data-quality-job-definition/" + name)
	case driver.MonitoringModelQuality:
		return m.arn("model-quality-job-definition/" + name)
	case driver.MonitoringModelBias:
		return m.arn("model-bias-job-definition/" + name)
	default:
		return m.arn("model-explainability-job-definition/" + name)
	}
}

func defKey(kind, name string) string { return kind + "\x00" + name }

func validMonitoringKind(kind string) bool {
	for _, k := range driver.MonitoringKinds {
		if k == kind {
			return true
		}
	}

	return false
}

func validMonitoringName(field, name string) error {
	if name == "" || len(name) > maxMonitoringNameLen || !monitoringNamePattern.MatchString(name) {
		return errors.Newf(errors.InvalidArgument, "%s %q must be 1-%d letters, digits and hyphens", field, name, maxMonitoringNameLen)
	}

	return nil
}

// --- Job definitions ---

func cloneDef(in *driver.MonitoringJobDefinition) *driver.MonitoringJobDefinition {
	out := *in
	out.Echo = cloneEcho(in.Echo)

	return &out
}

// CreateMonitoringJobDefinition creates a data quality, model quality, model
// bias or model explainability monitoring job definition.
//
//nolint:gocritic,lll // large value params mirror the driver interface; SDK-shaped literal
func (m *Mock) CreateMonitoringJobDefinition(_ context.Context, spec driver.MonitoringJobDefinitionSpec) (*driver.MonitoringJobDefinition, error) {
	if !validMonitoringKind(spec.Kind) {
		return nil, errors.Newf(errors.InvalidArgument, "unknown monitoring job definition kind %q", spec.Kind)
	}

	if err := validMonitoringName("jobDefinitionName", spec.Name); err != nil {
		return nil, err
	}

	if err := validRole(spec.RoleARN); err != nil {
		return nil, err
	}

	if err := validateTags(spec.Tags); err != nil {
		return nil, err
	}

	arn := m.monitoringARN(spec.Kind, spec.Name)
	d := &driver.MonitoringJobDefinition{
		Kind: spec.Kind, Name: spec.Name, ARN: arn, RoleARN: spec.RoleARN, EndpointName: spec.EndpointName,
		Echo: cloneEcho(spec.Echo), CreationTime: m.now(),
	}

	if !m.monDefs.SetIfAbsent(defKey(spec.Kind, spec.Name), d) {
		return nil, driver.InUse("%s job definition %q already exists", spec.Kind, spec.Name)
	}

	m.setTags(arn, spec.Tags)

	return cloneDef(d), nil
}

func (m *Mock) DescribeMonitoringJobDefinition(_ context.Context, kind, name string) (*driver.MonitoringJobDefinition, error) {
	d, ok := m.monDefs.Get(defKey(kind, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "%s job definition %q not found", kind, name)
	}

	return cloneDef(d), nil
}

//nolint:gocritic,lll // large value params mirror the driver interface; SDK-shaped literal
func (m *Mock) ListMonitoringJobDefinitions(_ context.Context, kind string, opts driver.ListOptions) (driver.ListPage[driver.MonitoringJobDefinition], error) {
	return listFrom(m.monDefs, listSpec[driver.MonitoringJobDefinition]{
		name:    func(t *driver.MonitoringJobDefinition) string { return t.Name },
		created: func(t *driver.MonitoringJobDefinition) string { return t.CreationTime },
		extra: func(t *driver.MonitoringJobDefinition, f map[string]string) bool {
			return t.Kind == kind && (f["EndpointName"] == "" || t.EndpointName == f["EndpointName"])
		},
		clone: func(t *driver.MonitoringJobDefinition) driver.MonitoringJobDefinition { return *cloneDef(t) },
	}, opts)
}

// DeleteMonitoringJobDefinition deletes a definition. A schedule that names it
// keeps its stored reference (the real API documents no in-use guard).
func (m *Mock) DeleteMonitoringJobDefinition(_ context.Context, kind, name string) error {
	d, ok := m.monDefs.Get(defKey(kind, name))
	if !ok {
		return errors.Newf(errors.NotFound, "%s job definition %q not found", kind, name)
	}

	m.monDefs.Delete(defKey(kind, name))
	m.dropTags(d.ARN)

	return nil
}

// --- Schedule expressions ---

// scheduleSpec is a parsed ScheduleExpression.
type scheduleSpec struct {
	now        bool
	hourly     bool
	hour, step int // daily at hour; with step, every step hours from hour
}

// parseSchedule accepts the documented forms: cron(0 * ? * * *) hourly,
// cron(0 H ? * * *) daily, cron(0 H/N ? * * *) every N hours from H, and NOW.
func parseSchedule(expr string) (scheduleSpec, error) {
	if expr == "NOW" {
		return scheduleSpec{now: true}, nil
	}

	g := scheduleExprPattern.FindStringSubmatch(expr)
	if g == nil {
		return scheduleSpec{}, errors.Newf(errors.InvalidArgument,
			"scheduleExpression %q must be cron(0 * ? * * *), cron(0 [00-23] ? * * *), cron(0 [00-23]/[01-24] ? * * *) or NOW", expr)
	}

	if g[1] == "*" {
		return scheduleSpec{hourly: true}, nil
	}

	hour, _ := strconv.Atoi(g[2])
	step := 0

	if g[3] != "" {
		step, _ = strconv.Atoi(g[3])
		if step < 1 || step > hoursPerDay {
			return scheduleSpec{}, errors.Newf(errors.InvalidArgument, "scheduleExpression interval must be between 1 and %d hours", hoursPerDay)
		}
	}

	if hour > hoursPerDay-1 {
		return scheduleSpec{}, errors.New(errors.InvalidArgument, "scheduleExpression hour must be between 00 and 23")
	}

	return scheduleSpec{hour: hour, step: step}, nil
}

// fires returns the fire times t with from < t <= to (for NOW: exactly from,
// once), oldest first. When more than limit have fired it keeps the newest
// limit, so a long-running schedule never stops producing current executions.
func (s scheduleSpec) fires(from, to time.Time, limit int) []time.Time {
	if s.now {
		if from.After(to) {
			return nil
		}

		return []time.Time{from}
	}

	first := from.UTC().Truncate(time.Hour).Add(time.Hour)
	out := make([]time.Time, 0, min(limit, hoursPerDay))

	// Walk back from the latest possible fire so the newest limit are kept.
	for t := to.UTC().Truncate(time.Hour); !t.Before(first) && len(out) < limit; t = t.Add(-time.Hour) {
		if s.matches(t.Hour()) {
			out = append(out, t)
		}
	}

	slices.Reverse(out)

	return out
}

func (s scheduleSpec) matches(hour int) bool {
	switch {
	case s.hourly:
		return true
	case s.step == 0:
		return hour == s.hour
	default:
		return hour >= s.hour && (hour-s.hour)%s.step == 0
	}
}

// parseOffset parses an ISO-8601 duration offset such as -PT1H.
func parseOffset(v string) (time.Duration, error) {
	g := isoOffsetPattern.FindStringSubmatch(v)
	if g == nil || v == "P" || strings.HasSuffix(v, "T") {
		return 0, errors.Newf(errors.InvalidArgument, "%q is not a valid ISO-8601 duration offset", v)
	}

	num := func(s string) time.Duration {
		n, _ := strconv.Atoi(s)

		return time.Duration(n)
	}

	d := num(g[2])*hoursPerDay*time.Hour + num(g[3])*time.Hour + num(g[4])*time.Minute + num(g[5])*time.Second
	if g[1] == "-" {
		d = -d
	}

	return d, nil
}

// validateScheduleConfig checks the schedule expression and analysis window.
//
//nolint:gocyclo // validation branches mirror the service's rules
func validateScheduleConfig(spec *driver.MonitoringScheduleSpec) error {
	if spec.ScheduleExpression == "" {
		return nil // the service picks a daily schedule
	}

	sched, err := parseSchedule(spec.ScheduleExpression)
	if err != nil {
		return err
	}

	if sched.now && (spec.DataAnalysisStartTime == "" || spec.DataAnalysisEndTime == "") {
		return errors.New(errors.InvalidArgument, "dataAnalysisStartTime and dataAnalysisEndTime are required when scheduleExpression is NOW")
	}

	if spec.DataAnalysisStartTime == "" && spec.DataAnalysisEndTime == "" {
		return nil
	}

	start, err := parseOffset(spec.DataAnalysisStartTime)
	if err != nil {
		return err
	}

	end, err := parseOffset(spec.DataAnalysisEndTime)
	if err != nil {
		return err
	}

	if d := end - start; d < 0 || d > maxAnalysisWindow {
		return errors.New(errors.InvalidArgument, "the data analysis window must end after it starts and span at most 24 hours")
	}

	return nil
}

// --- Schedules ---

func cloneSchedule(in *driver.MonitoringSchedule) *driver.MonitoringSchedule {
	out := *in
	out.Config = append(json.RawMessage(nil), in.Config...)

	if in.LastExecution != nil {
		e := *in.LastExecution
		out.LastExecution = &e
	}

	return &out
}

func (m *Mock) validateScheduleSpec(spec *driver.MonitoringScheduleSpec) error {
	if err := validMonitoringName("monitoringScheduleName", spec.Name); err != nil {
		return err
	}

	if len(spec.Config) == 0 {
		return errors.New(errors.InvalidArgument, "monitoringScheduleConfig is required")
	}

	if err := validateScheduleConfig(spec); err != nil {
		return err
	}

	if spec.JobDefinitionName != "" {
		if !validMonitoringKind(spec.Type) {
			return errors.New(errors.InvalidArgument, "monitoringType is required with monitoringJobDefinitionName")
		}

		def, ok := m.monDefs.Get(defKey(spec.Type, spec.JobDefinitionName))
		if !ok {
			return errors.Newf(errors.InvalidArgument, "Could not find %s job definition %q", spec.Type, spec.JobDefinitionName)
		}

		if spec.EndpointName == "" {
			spec.EndpointName = def.EndpointName
		}
	}

	if spec.EndpointName != "" && m.endpointStatus(context.Background(), spec.EndpointName) == "" {
		return errors.Newf(errors.InvalidArgument, "Could not find endpoint %q", spec.EndpointName)
	}

	return nil
}

// CreateMonitoringSchedule creates a schedule that starts Scheduled.
//
//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) CreateMonitoringSchedule(_ context.Context, spec driver.MonitoringScheduleSpec) (*driver.MonitoringSchedule, error) {
	if err := m.validateScheduleSpec(&spec); err != nil {
		return nil, err
	}

	if err := validateTags(spec.Tags); err != nil {
		return nil, err
	}

	m.monMu.Lock()
	defer m.monMu.Unlock()

	now := m.now()
	arn := m.monitoringARN("schedule", spec.Name)
	s := &driver.MonitoringSchedule{
		Name: spec.Name, ARN: arn, Status: driver.ScheduleScheduled, Type: spec.Type, JobDefinitionName: spec.JobDefinitionName,
		EndpointName: spec.EndpointName, ScheduleExpression: spec.ScheduleExpression, DataAnalysisStartTime: spec.DataAnalysisStartTime,
		DataAnalysisEndTime: spec.DataAnalysisEndTime, Config: append(json.RawMessage(nil), spec.Config...),
		CreationTime: now, LastModifiedTime: now, ActiveFrom: now,
	}

	if !m.monSchedules.SetIfAbsent(spec.Name, s) {
		return nil, driver.InUse("monitoring schedule %q already exists", spec.Name)
	}

	m.setTags(arn, spec.Tags)
	m.monSettle.Begin(spec.Name, driver.SchedulePending, m.opts.Clock.Now(), m.opts.SettleDuration(settle.DefaultScheduleSettle))

	return m.observeSchedule(s), nil
}

// observeSchedule overlays the --async-settle Pending window and the latest
// execution on a copy of a stored schedule. The caller holds monMu.
func (m *Mock) observeSchedule(s *driver.MonitoringSchedule) *driver.MonitoringSchedule {
	out := cloneSchedule(s)
	out.Status = m.monSettle.State(s.Name, m.opts.Clock.Now(), s.Status)

	if execs := m.executionsOf(s); len(execs) > 0 {
		last := execs[len(execs)-1]
		out.LastExecution = &last
	}

	return out
}

func (m *Mock) DescribeMonitoringSchedule(_ context.Context, name string) (*driver.MonitoringSchedule, error) {
	m.monMu.Lock()
	defer m.monMu.Unlock()

	s, ok := m.monSchedules.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "monitoring schedule %q not found", name)
	}

	return m.observeSchedule(s), nil
}

//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) ListMonitoringSchedules(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.MonitoringSchedule], error) {
	m.monMu.Lock()
	stored := m.monSchedules.SortedValues()
	observed := make([]*driver.MonitoringSchedule, 0, len(stored))

	for _, s := range stored {
		observed = append(observed, m.observeSchedule(s))
	}
	m.monMu.Unlock()

	return listItems(observed, listSpec[driver.MonitoringSchedule]{
		name:     func(t *driver.MonitoringSchedule) string { return t.Name },
		status:   func(t *driver.MonitoringSchedule) string { return t.Status },
		created:  func(t *driver.MonitoringSchedule) string { return t.CreationTime },
		modified: func(t *driver.MonitoringSchedule) string { return t.LastModifiedTime },
		extra: func(t *driver.MonitoringSchedule, f map[string]string) bool {
			return (f["EndpointName"] == "" || t.EndpointName == f["EndpointName"]) &&
				(f["MonitoringJobDefinitionName"] == "" || t.JobDefinitionName == f["MonitoringJobDefinitionName"]) &&
				(f["MonitoringTypeEquals"] == "" || t.Type == f["MonitoringTypeEquals"])
		},
	}, opts)
}

// UpdateMonitoringSchedule replaces the schedule's configuration. Executions
// already due are kept; the new cadence applies from now.
//
//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) UpdateMonitoringSchedule(_ context.Context, spec driver.MonitoringScheduleSpec) (*driver.MonitoringSchedule, error) {
	if err := m.validateScheduleSpec(&spec); err != nil {
		return nil, err
	}

	m.monMu.Lock()
	defer m.monMu.Unlock()

	cur, ok := m.monSchedules.Get(spec.Name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "monitoring schedule %q not found", spec.Name)
	}

	m.materializeExecutions(cur)

	updated := cloneSchedule(cur)
	updated.Type, updated.JobDefinitionName, updated.EndpointName = spec.Type, spec.JobDefinitionName, spec.EndpointName
	updated.ScheduleExpression, updated.DataAnalysisStartTime, updated.DataAnalysisEndTime = spec.ScheduleExpression, spec.DataAnalysisStartTime, spec.DataAnalysisEndTime //nolint:lll // SDK-shaped literal
	updated.Config = append(json.RawMessage(nil), spec.Config...)
	updated.LastModifiedTime = m.now()
	updated.ActiveFrom = updated.LastModifiedTime
	m.monSchedules.Set(spec.Name, updated)

	return m.observeSchedule(updated), nil
}

// DeleteMonitoringSchedule deletes a schedule (stopping it) but keeps its
// execution history.
func (m *Mock) DeleteMonitoringSchedule(_ context.Context, name string) error {
	m.monMu.Lock()
	defer m.monMu.Unlock()

	s, ok := m.monSchedules.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "monitoring schedule %q not found", name)
	}

	m.materializeExecutions(s)
	m.monSchedules.Delete(name)
	m.monSettle.Clear(name)
	m.dropTags(s.ARN)

	for key, a := range m.monAlerts.All() {
		if a.ScheduleName == name {
			m.monAlerts.Delete(key)
		}
	}

	return nil
}

// StartMonitoringSchedule resumes a stopped schedule; starting a running one is a no-op.
func (m *Mock) StartMonitoringSchedule(_ context.Context, name string) error {
	return m.setScheduleStatus(name, driver.ScheduleScheduled)
}

// StopMonitoringSchedule stops a schedule; stopping a stopped one is a no-op.
func (m *Mock) StopMonitoringSchedule(_ context.Context, name string) error {
	return m.setScheduleStatus(name, driver.ScheduleStopped)
}

func (m *Mock) setScheduleStatus(name, status string) error {
	m.monMu.Lock()
	defer m.monMu.Unlock()

	s, ok := m.monSchedules.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "monitoring schedule %q not found", name)
	}

	if s.Status == status {
		return nil
	}

	m.materializeExecutions(s)

	updated := cloneSchedule(s)
	updated.Status = status
	updated.LastModifiedTime = m.now()

	if status == driver.ScheduleScheduled {
		updated.ActiveFrom = updated.LastModifiedTime
	}

	m.monSchedules.Set(name, updated)

	return nil
}

// --- Executions ---

func execKey(schedule string, fire time.Time) string {
	return schedule + "\x00" + fire.UTC().Format(execKeyLayout)
}

// virtualExecutions computes the executions a Scheduled schedule has produced
// since ActiveFrom (none while Stopped). The caller holds monMu.
func (m *Mock) virtualExecutions(s *driver.MonitoringSchedule) []driver.MonitoringExecution {
	if s.Status != driver.ScheduleScheduled {
		return nil
	}

	expr := orDefault(s.ScheduleExpression, defaultScheduleExpression)

	sched, err := parseSchedule(expr)
	if err != nil {
		return nil
	}

	now := m.opts.Clock.Now()
	fires := sched.fires(parseTime(s.ActiveFrom), now, driver.MaxMonitoringExecutionsPerSchedule)
	out := make([]driver.MonitoringExecution, 0, len(fires))

	for _, f := range fires {
		status := driver.MonitorExecCompleted
		if d := m.opts.SettleDuration(settle.DefaultExecutionSettle); d > 0 && now.Before(f.Add(d)) {
			status = driver.MonitorExecInProgress
		}

		out = append(out, driver.MonitoringExecution{
			ScheduleName: s.Name, EndpointName: s.EndpointName, JobDefinitionName: s.JobDefinitionName, Type: s.Type, Status: status,
			ProcessingJobARN: m.arn("processing-job/model-monitoring-" + f.UTC().Format("200601021504") + "-" + s.Name),
			ScheduledTime:    fmtTime(f), CreationTime: fmtTime(f), LastModifiedTime: fmtTime(f),
		})
	}

	return out
}

// materializeExecutions persists the executions a schedule has produced so far
// (idempotently), so they outlive a Stop, an Update or a Delete. The caller
// holds monMu.
func (m *Mock) materializeExecutions(s *driver.MonitoringSchedule) {
	for _, e := range m.virtualExecutions(s) { //nolint:gocritic // large value params mirror the driver interface
		// A run still in flight when the schedule stops, changes or is deleted finishes.
		if e.Status == driver.MonitorExecInProgress {
			e.Status = driver.MonitorExecCompleted
		}

		m.monExecs.SetIfAbsent(execKey(e.ScheduleName, parseTime(e.ScheduledTime)), &e)
	}
}

// executionsOf returns a schedule's executions (stored rows win over computed
// ones), oldest first. The caller holds monMu.
func (m *Mock) executionsOf(s *driver.MonitoringSchedule) []driver.MonitoringExecution {
	byKey := map[string]driver.MonitoringExecution{}

	for _, e := range m.virtualExecutions(s) { //nolint:gocritic // large value params mirror the driver interface
		byKey[execKey(e.ScheduleName, parseTime(e.ScheduledTime))] = e
	}

	for key, e := range m.monExecs.All() {
		if e.ScheduleName == s.Name {
			byKey[key] = *e
		}
	}

	return sortedExecutions(byKey)
}

func sortedExecutions(byKey map[string]driver.MonitoringExecution) []driver.MonitoringExecution {
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]driver.MonitoringExecution, 0, len(keys))
	for _, k := range keys {
		out = append(out, byKey[k])
	}

	return out
}

// allExecutions returns every execution: those of live schedules (computed plus
// stored) and the stored history of deleted ones. The caller holds monMu.
func (m *Mock) allExecutions() []driver.MonitoringExecution {
	byKey := map[string]driver.MonitoringExecution{}

	for _, s := range m.monSchedules.All() {
		for _, e := range m.virtualExecutions(s) { //nolint:gocritic // large value params mirror the driver interface
			byKey[execKey(e.ScheduleName, parseTime(e.ScheduledTime))] = e
		}
	}

	for key, e := range m.monExecs.All() {
		byKey[key] = *e
	}

	return sortedExecutions(byKey)
}

// ListMonitoringExecutions lists executions. An unknown schedule name yields an
// empty list: it is a filter, not a lookup.
//
//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) ListMonitoringExecutions(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.MonitoringExecution], error) {
	m.monMu.Lock()
	all := m.allExecutions()
	m.monMu.Unlock()

	items := make([]*driver.MonitoringExecution, 0, len(all))
	for i := range all {
		items = append(items, &all[i])
	}

	return listItems(items, listSpec[driver.MonitoringExecution]{
		name:         func(t *driver.MonitoringExecution) string { return t.ScheduleName + "\x00" + t.ScheduledTime },
		status:       func(t *driver.MonitoringExecution) string { return t.Status },
		created:      func(t *driver.MonitoringExecution) string { return t.CreationTime },
		modified:     func(t *driver.MonitoringExecution) string { return t.LastModifiedTime },
		defaultOrder: driver.SortDescending,
		keys:         map[string]func(*driver.MonitoringExecution) string{"ScheduledTime": func(t *driver.MonitoringExecution) string { return t.ScheduledTime }}, //nolint:lll // SDK-shaped literal
		extra:        executionFilter,
	}, opts)
}

func executionFilter(e *driver.MonitoringExecution, f map[string]string) bool {
	for key, got := range map[string]string{
		"EndpointName": e.EndpointName, "MonitoringJobDefinitionName": e.JobDefinitionName,
		"MonitoringScheduleName": e.ScheduleName, "MonitoringTypeEquals": e.Type,
	} {
		if want := f[key]; want != "" && want != got {
			return false
		}
	}

	return inTimeWindow(e.ScheduledTime, f["ScheduledTimeAfter"], f["ScheduledTimeBefore"])
}

// --- Alerts ---

func validateAlertBounds(datapoints, period int) error {
	if datapoints < 1 || datapoints > maxAlertBound || period < 1 || period > maxAlertBound {
		return errors.Newf(errors.InvalidArgument, "datapointsToAlert and evaluationPeriod must be between 1 and %d", maxAlertBound)
	}

	return nil
}

func violating(status string) bool {
	return status == driver.MonitorExecFailed || status == driver.MonitorExecCompletedViolations
}

// alertTimeline replays a schedule's executions through an alert's sliding
// window: it returns the current status and one event per status change.
func alertTimeline(a *driver.MonitoringAlert, execs []driver.MonitoringExecution) (string, []driver.MonitoringAlertEvent) {
	status := driver.AlertOK

	var events []driver.MonitoringAlertEvent

	for i := range execs {
		from := i + 1 - a.EvaluationPeriod
		if from < 0 {
			from = 0
		}

		count := 0

		for _, e := range execs[from : i+1] { //nolint:gocritic // large value params mirror the driver interface
			if violating(e.Status) {
				count++
			}
		}

		next := driver.AlertOK
		if count >= a.DatapointsToAlert {
			next = driver.AlertInAlert
		}

		if next != status {
			status = next
			events = append(events, driver.MonitoringAlertEvent{
				ScheduleName: a.ScheduleName, AlertName: a.Name, Status: status, CreationTime: execs[i].ScheduledTime,
			})
		}
	}

	return status, events
}

// scheduleExecutions returns the executions of one schedule, live or deleted.
// The caller holds monMu.
func (m *Mock) scheduleExecutions(name string) []driver.MonitoringExecution {
	var out []driver.MonitoringExecution

	for _, e := range m.allExecutions() { //nolint:gocritic // large value params mirror the driver interface
		if e.ScheduleName == name {
			out = append(out, e)
		}
	}

	return out
}

//nolint:gocritic,lll // large value params mirror the driver interface; SDK-shaped literal
func (m *Mock) ListMonitoringAlerts(_ context.Context, scheduleName string, opts driver.ListOptions) (driver.ListPage[driver.MonitoringAlert], error) {
	m.monMu.Lock()

	if !m.monSchedules.Has(scheduleName) {
		m.monMu.Unlock()

		return driver.ListPage[driver.MonitoringAlert]{}, errors.Newf(errors.NotFound, "monitoring schedule %q not found", scheduleName)
	}

	execs := m.scheduleExecutions(scheduleName)
	items := make([]*driver.MonitoringAlert, 0)

	for _, a := range m.monAlerts.SortedValues() {
		if a.ScheduleName == scheduleName {
			c := *a
			c.Status, _ = alertTimeline(&c, execs)
			items = append(items, &c)
		}
	}
	m.monMu.Unlock()

	return listItems(items, listSpec[driver.MonitoringAlert]{
		name:    func(t *driver.MonitoringAlert) string { return t.Name },
		created: func(t *driver.MonitoringAlert) string { return t.CreationTime },
	}, opts)
}

//nolint:gocritic,lll // large value params mirror the driver interface; SDK-shaped literal
func (m *Mock) ListMonitoringAlertHistory(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.MonitoringAlertEvent], error) {
	schedule, alertName := opts.Filters["MonitoringScheduleName"], opts.Filters["MonitoringAlertName"]

	m.monMu.Lock()

	if schedule != "" && !m.monSchedules.Has(schedule) {
		m.monMu.Unlock()

		return driver.ListPage[driver.MonitoringAlertEvent]{}, errors.Newf(errors.NotFound, "monitoring schedule %q not found", schedule)
	}

	var events []*driver.MonitoringAlertEvent

	for _, a := range m.monAlerts.SortedValues() {
		if (schedule != "" && a.ScheduleName != schedule) || (alertName != "" && a.Name != alertName) {
			continue
		}

		_, evs := alertTimeline(a, m.scheduleExecutions(a.ScheduleName))
		for i := range evs {
			events = append(events, &evs[i])
		}
	}
	m.monMu.Unlock()

	return listItems(events, listSpec[driver.MonitoringAlertEvent]{
		name: func(t *driver.MonitoringAlertEvent) string {
			return t.ScheduleName + "\x00" + t.AlertName + "\x00" + t.CreationTime
		},
		status:       func(t *driver.MonitoringAlertEvent) string { return t.Status },
		created:      func(t *driver.MonitoringAlertEvent) string { return t.CreationTime },
		defaultOrder: driver.SortDescending,
	}, opts)
}

// UpdateMonitoringAlert changes an alert's thresholds.
//
//nolint:lll // SDK-shaped literal
func (m *Mock) UpdateMonitoringAlert(_ context.Context, scheduleName, alertName string, datapoints, period int) (*driver.MonitoringSchedule, error) {
	if err := validateAlertBounds(datapoints, period); err != nil {
		return nil, err
	}

	m.monMu.Lock()
	defer m.monMu.Unlock()

	s, ok := m.monSchedules.Get(scheduleName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "monitoring schedule %q not found", scheduleName)
	}

	if !m.monAlerts.Update(scheduleName+"\x00"+alertName, func(a *driver.MonitoringAlert) *driver.MonitoringAlert {
		updated := *a
		updated.DatapointsToAlert, updated.EvaluationPeriod, updated.LastModifiedTime = datapoints, period, m.now()

		return &updated
	}) {
		return nil, errors.Newf(errors.NotFound, "monitoring alert %q not found on schedule %q", alertName, scheduleName)
	}

	return m.observeSchedule(s), nil
}

// defaultScheduleExpression is the cadence of a schedule created without one: daily at midnight.
const defaultScheduleExpression = "cron(0 0 ? * * *)"
