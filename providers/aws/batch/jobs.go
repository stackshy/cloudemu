package batch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// SubmitJob / CancelJob / TerminateJob / DescribeJobs bounds from the Batch API
// reference.
const (
	minArraySize          = 2
	maxArraySize          = 10000
	maxDependencies       = 20
	maxSchedulingPriority = 9999
	minAttemptDuration    = 60
	maxReasonLen          = 1024
	maxDescribeJobs       = 100

	jobSeqKey = "seq"
)

// jobNameRE is the SubmitJob jobName rule: up to 128 characters, the first
// alphanumeric, then letters, numbers, hyphens and underscores.
var jobNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// SubmitJob validates and records a job. The job then advances through its
// lifecycle on the configured clock (see natural). Jobs run no workload: a job
// whose dependencies succeed always succeeds.
//
//nolint:gocritic // in is the driver SubmitJobInput, taken by value to match the interface
func (m *Mock) SubmitJob(ctx context.Context, in driver.SubmitJobInput) (*driver.Job, error) {
	if err := validateSubmitShape(&in); err != nil {
		return nil, err
	}

	q, err := m.submitQueue(in.JobQueue)
	if err != nil {
		return nil, err
	}

	if shareErr := validateShare(&q, &in); shareErr != nil {
		return nil, shareErr
	}

	jd, err := m.resolveJobDefinition(in.JobDefinition)
	if err != nil {
		return nil, err
	}

	if platformErr := m.validateJobPlatform(&q, &jd, &in); platformErr != nil {
		return nil, platformErr
	}

	job, err := m.buildJob(&in, &q, &jd)
	if err != nil {
		return nil, err
	}

	m.jobsMu.Lock()

	if err := m.validateDependencies(in.DependsOn); err != nil {
		m.jobsMu.Unlock()

		return nil, err
	}

	seq, _ := m.jobSeq.Get(jobSeqKey)
	seq++
	m.jobSeq.Set(jobSeqKey, seq)

	now := m.opts.Clock.Now()
	job.CreatedAt = now.UnixMilli()

	rec := jobRecord{
		Job:         job,
		Seq:         seq,
		SubmittedAt: now,
		Phase:       m.opts.SettleDuration(settle.DefaultBatchJobPhase),
		RunPhase:    m.opts.SettleDuration(settle.DefaultBatchJobRunDwell),
		Sequential:  hasSequential(in.DependsOn),
		LastEmitted: driver.JobStatusSubmitted,
	}

	m.jobs.Set(job.JobID, rec)

	m.jobsMu.Unlock()

	m.publishJobEvents(ctx, m.opts.Clock.Now())

	out := cloneJob(job)

	return &out, nil
}

// validateSubmitShape checks the request fields that need no stored state.
func validateSubmitShape(in *driver.SubmitJobInput) error {
	if err := validateSubmitRequired(in); err != nil {
		return err
	}

	if err := validateSubmitLimits(in); err != nil {
		return err
	}

	if err := validateTimeout(in.Timeout); err != nil {
		return err
	}

	return validateDependencyShape(in.DependsOn, in.ArraySize != nil)
}

// validateSubmitRequired checks the required SubmitJob fields and the job name
// rule.
func validateSubmitRequired(in *driver.SubmitJobInput) error {
	switch {
	case in.JobName == "":
		return cerrors.New(cerrors.InvalidArgument, "jobName is required")
	case !jobNameRE.MatchString(in.JobName):
		return cerrors.Newf(cerrors.InvalidArgument,
			"jobName %q must be up to 128 characters, start with a letter or number, "+
				"and contain only letters, numbers, hyphens and underscores", in.JobName)
	case in.JobQueue == "":
		return cerrors.New(cerrors.InvalidArgument, "jobQueue is required")
	case in.JobDefinition == "":
		return cerrors.New(cerrors.InvalidArgument, "jobDefinition is required")
	default:
		return nil
	}
}

// validateSubmitLimits checks the documented SubmitJob numeric bounds and the
// job's tags.
func validateSubmitLimits(in *driver.SubmitJobInput) error {
	if in.ArraySize != nil && (*in.ArraySize < minArraySize || *in.ArraySize > maxArraySize) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"arrayProperties.size must be between %d and %d, got %d", minArraySize, maxArraySize, *in.ArraySize)
	}

	if len(in.DependsOn) > maxDependencies {
		return cerrors.Newf(cerrors.InvalidArgument,
			"a job can depend on at most %d jobs, got %d", maxDependencies, len(in.DependsOn))
	}

	if p := in.SchedulingPriorityOverride; p != nil && (*p < 0 || *p > maxSchedulingPriority) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"schedulingPriorityOverride must be between 0 and %d", maxSchedulingPriority)
	}

	return validateTags(in.Tags)
}

// validateTimeout enforces the 60-second minimum attemptDurationSeconds.
func validateTimeout(raw json.RawMessage) error {
	if !isSet(raw) {
		return nil
	}

	var t struct {
		AttemptDurationSeconds *int64 `json:"attemptDurationSeconds"`
	}

	if err := json.Unmarshal(raw, &t); err != nil {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid timeout: %v", err)
	}

	if t.AttemptDurationSeconds != nil && *t.AttemptDurationSeconds < minAttemptDuration {
		return cerrors.Newf(cerrors.InvalidArgument,
			"timeout.attemptDurationSeconds must be at least %d seconds", minAttemptDuration)
	}

	return nil
}

// validateDependencyShape checks dependency types: a dependency without a job
// ID is only meaningful as an array job's SEQUENTIAL dependency, and the
// N_TO_N/SEQUENTIAL array dependency types apply only to array jobs.
func validateDependencyShape(deps []driver.JobDependency, isArray bool) error {
	for _, d := range deps {
		switch d.Type {
		case "":
		case driver.DependencyNToN, driver.DependencySequential:
			if !isArray {
				return cerrors.Newf(cerrors.InvalidArgument, "dependency type %s is only valid for array jobs", d.Type)
			}
		default:
			return cerrors.Newf(cerrors.InvalidArgument,
				"invalid dependency type %q; valid values are N_TO_N and SEQUENTIAL", d.Type)
		}

		if d.JobID == "" && d.Type != driver.DependencySequential {
			return cerrors.New(cerrors.InvalidArgument, "dependsOn.jobId is required unless the type is SEQUENTIAL")
		}
	}

	return nil
}

func hasSequential(deps []driver.JobDependency) bool {
	for _, d := range deps {
		if d.Type == driver.DependencySequential {
			return true
		}
	}

	return false
}

// validateDependencies requires every referenced job to exist. Callers hold
// jobsMu.
func (m *Mock) validateDependencies(deps []driver.JobDependency) error {
	for _, d := range deps {
		if d.JobID == "" {
			continue
		}

		if _, _, ok := m.lookup(d.JobID); !ok {
			return cerrors.Newf(cerrors.InvalidArgument, "dependency job %q does not exist", d.JobID)
		}
	}

	return nil
}

// submitQueue resolves the target queue, which must exist and be ENABLED (a
// DISABLED queue accepts no new jobs).
func (m *Mock) submitQueue(ref string) (driver.JobQueue, error) {
	q, ok := m.jobQueues.Get(nameFromARN(kindJobQueue, ref))
	if !ok {
		return driver.JobQueue{}, cerrors.Newf(cerrors.InvalidArgument, "job queue %q does not exist", ref)
	}

	if q.State != driver.StateEnabled {
		return driver.JobQueue{}, cerrors.Newf(cerrors.InvalidArgument,
			"job queue %q is %s and can't accept new jobs", q.Name, q.State)
	}

	return q, nil
}

// validateShare enforces the shareIdentifier rule: required on a queue with a
// fair-share scheduling policy, forbidden on a FIFO queue.
func validateShare(q *driver.JobQueue, in *driver.SubmitJobInput) error {
	if q.SchedulingPolicyARN != "" && in.ShareIdentifier == "" {
		return cerrors.Newf(cerrors.InvalidArgument,
			"job queue %q has a scheduling policy, so shareIdentifier must be specified", q.Name)
	}

	if q.SchedulingPolicyARN == "" && in.ShareIdentifier != "" {
		return cerrors.Newf(cerrors.InvalidArgument,
			"job queue %q has no scheduling policy, so shareIdentifier must not be specified", q.Name)
	}

	return nil
}

// resolveJobDefinition resolves name, name:revision, or an ARN with or without
// a revision. Without a revision the latest ACTIVE revision is used; an
// explicit revision must be ACTIVE.
func (m *Mock) resolveJobDefinition(ref string) (driver.JobDefinition, error) {
	key := nameFromARN(kindJobDefinition, ref)

	if strings.Contains(key, ":") {
		jd, ok := m.jobDefs.Get(key)
		if !ok {
			return driver.JobDefinition{}, cerrors.Newf(cerrors.InvalidArgument, "job definition %q does not exist", ref)
		}

		// A deregistered revision can't be submitted to, even by explicit
		// revision.
		if jd.Status != driver.JDStatusActive {
			return driver.JobDefinition{}, cerrors.Newf(cerrors.InvalidArgument,
				"job definition %q is %s and can't be used to submit jobs", ref, jd.Status)
		}

		return jd, nil
	}

	var (
		latest driver.JobDefinition
		found  bool
	)

	defs := m.sortedJobDefs()
	for i := range defs {
		if defs[i].Name == key && defs[i].Status == driver.JDStatusActive {
			latest, found = defs[i], true
		}
	}

	if !found {
		return driver.JobDefinition{}, cerrors.Newf(cerrors.InvalidArgument,
			"job definition %q has no ACTIVE revision", ref)
	}

	return latest, nil
}

// buildJob assembles the static detail of a new job from the request, the
// queue and the job definition (submit-time values override the definition's).
func (m *Mock) buildJob(in *driver.SubmitJobInput, q *driver.JobQueue, jd *driver.JobDefinition) (driver.Job, error) {
	container, err := mergeContainer(jd.ContainerProperties, in.ContainerOverrides)
	if err != nil {
		return driver.Job{}, err
	}

	props, err := applyPropertyOverrides(in, jd)
	if err != nil {
		return driver.Job{}, err
	}

	id := idgen.UUID()

	job := driver.Job{
		JobID:                id,
		JobARN:               m.arn(kindJob, id),
		JobName:              in.JobName,
		JobQueue:             q.ARN,
		JobDefinition:        jd.ARN,
		DependsOn:            cloneDeps(in.DependsOn),
		Parameters:           mergeParams(jd.Parameters, in.Parameters),
		Container:            container,
		NodeProperties:       props.node,
		EcsProperties:        props.ecs,
		EksProperties:        props.eks,
		RetryStrategy:        firstSet(in.RetryStrategy, jd.RetryStrategy),
		Timeout:              firstSet(in.Timeout, jd.Timeout),
		PlatformCapabilities: copyStrings(jd.PlatformCapabilities),
		PropagateTags:        copyBool(jd.PropagateTags),
		SchedulingPriority:   copyInt32(jd.SchedulingPriority),
		ShareIdentifier:      in.ShareIdentifier,
		Tags:                 copyTags(in.Tags),
	}

	if in.PropagateTags != nil {
		job.PropagateTags = copyBool(in.PropagateTags)
	}

	if in.SchedulingPriorityOverride != nil {
		job.SchedulingPriority = copyInt32(in.SchedulingPriorityOverride)
	}

	if in.ArraySize != nil {
		job.ArrayProperties = &driver.ArrayProperties{Size: *in.ArraySize}
	}

	return job, nil
}

func cloneDeps(in []driver.JobDependency) []driver.JobDependency {
	if in == nil {
		return nil
	}

	out := make([]driver.JobDependency, len(in))
	copy(out, in)

	return out
}

func firstSet(a, b json.RawMessage) json.RawMessage {
	if isSet(a) {
		return copyRaw(a)
	}

	return copyRaw(b)
}

func mergeParams(def, sub map[string]string) map[string]string {
	if def == nil && sub == nil {
		return nil
	}

	out := copyTags(def)
	if out == nil {
		out = make(map[string]string, len(sub))
	}

	for k, v := range sub {
		out[k] = v
	}

	return out
}

// mergeContainer applies SubmitJob containerOverrides to the job definition's
// containerProperties: environment variables and resource requirements are
// merged (by name / type), every other override replaces the property.
func mergeContainer(props, overrides json.RawMessage) (json.RawMessage, error) {
	if !isSet(props) {
		return nil, nil
	}

	if !isSet(overrides) {
		return copyRaw(props), nil
	}

	var base, over map[string]json.RawMessage

	if err := json.Unmarshal(props, &base); err != nil {
		return copyRaw(props), nil //nolint:nilerr // a non-object definition blob is echoed unchanged
	}

	if err := json.Unmarshal(overrides, &over); err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid containerOverrides: %v", err)
	}

	for k, v := range over {
		if !isSet(v) {
			continue
		}

		switch k {
		case "environment":
			base[k] = mergeKeyed(base[k], v, "name")
		case "resourceRequirements":
			base[k] = mergeKeyed(base[k], v, "type")
		default:
			base[k] = v
		}
	}

	out, err := json.Marshal(base)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "merge container overrides: %v", err)
	}

	return out, nil
}

// mergeKeyed merges two JSON arrays of objects, replacing entries of base whose
// key field matches an entry of over and appending the rest.
func mergeKeyed(base, over json.RawMessage, key string) json.RawMessage {
	var b, o []map[string]json.RawMessage

	if err := json.Unmarshal(over, &o); err != nil {
		return over
	}

	if isSet(base) {
		if err := json.Unmarshal(base, &b); err != nil {
			return over
		}
	}

	for _, entry := range o {
		replaced := false

		for i := range b {
			if string(b[i][key]) == string(entry[key]) {
				b[i] = entry
				replaced = true

				break
			}
		}

		if !replaced {
			b = append(b, entry)
		}
	}

	out, err := json.Marshal(b)
	if err != nil {
		return over
	}

	return out
}

// DescribeJobs returns the requested jobs (top-level, array-child "<id>:<i>" or
// multi-node "<id>#<i>" IDs) in request order, each observed now. Unknown IDs
// are omitted.
func (m *Mock) DescribeJobs(ctx context.Context, jobIDs []string) ([]driver.Job, error) {
	if len(jobIDs) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "jobs is required")
	}

	if len(jobIDs) > maxDescribeJobs {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "jobs accepts at most %d job IDs, got %d", maxDescribeJobs, len(jobIDs))
	}

	m.publishJobEvents(ctx, m.opts.Clock.Now())

	o := m.newObserver(m.opts.Clock.Now())
	out := make([]driver.Job, 0, len(jobIDs))

	for _, id := range jobIDs {
		if rec, idx, ok := m.lookup(id); ok {
			out = append(out, o.render(&rec, idx))

			continue
		}

		if rec, idx, layout, ok := m.lookupNode(id); ok {
			out = append(out, o.renderNode(&rec, &layout, idx))
		}
	}

	return out, nil
}

// ListJobs page sizes: up to 1000 results per page when listing by status and
// up to 100 when a filter is used (ListJobs API reference, maxResults).
const (
	listJobsStatusPage = 1000
	listJobsFilterPage = 100
)

// ListJobs lists the jobs of a queue (top-level jobs and array parents), the
// children of an array job or the nodes of a multi-node parallel job. Queue
// listings are newest first, as the ListJobs reference describes; children and
// nodes are listed by index. Exactly one of jobQueue,
// arrayJobId and multiNodeJobId must be given. Without a filter only jobs in
// jobStatus (default RUNNING) are returned.
//
// The page token is a cursor on the listing order (submission sequence, or
// child or node index), not an offset, so jobs submitted or changing status between
// pages never make a page skip or repeat an entry.
//
//nolint:gocritic // in is the driver ListJobsInput, taken by value to match the interface
func (m *Mock) ListJobs(ctx context.Context, in driver.ListJobsInput) (*driver.ListJobsOutput, error) {
	if err := validateListJobs(&in); err != nil {
		return nil, err
	}

	limit, err := listJobsLimit(&in)
	if err != nil {
		return nil, err
	}

	after, err := decodeListCursor(in.NextToken)
	if err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now()
	m.publishJobEvents(ctx, now)

	l := jobLister{o: m.newObserver(now), in: &in, limit: limit, after: after}
	status, useStatus := listStatus(&in)
	l.status, l.useStatus = status, useStatus

	switch {
	case in.MultiNodeJobID != "":
		return l.nodes(m), nil
	case in.ArrayJobID != "":
		return l.children(m), nil
	default:
		return l.queue(m), nil
	}
}

// listJobsLimit resolves the page size: maxResults (at least 1) capped at the
// default page size for the kind of listing.
func listJobsLimit(in *driver.ListJobsInput) (int, error) {
	limit := listJobsStatusPage
	if len(in.Filters) > 0 {
		limit = listJobsFilterPage
	}

	if in.MaxResults == nil {
		return limit, nil
	}

	if *in.MaxResults < 1 {
		return 0, cerrors.Newf(cerrors.InvalidArgument, "maxResults must be at least 1, got %d", *in.MaxResults)
	}

	return min(limit, int(*in.MaxResults)), nil
}

// decodeListCursor reads a ListJobs nextToken: the base64 of the listing
// position the previous page ended at. An empty token starts from the top.
func decodeListCursor(token string) (int64, error) {
	if token == "" {
		return -1, nil
	}

	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, cerrors.New(cerrors.InvalidArgument, "invalid nextToken")
	}

	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil || n < 0 {
		return 0, cerrors.New(cerrors.InvalidArgument, "invalid nextToken")
	}

	return n, nil
}

func encodeListCursor(n int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(n, 10)))
}

// jobLister builds one ListJobs page.
type jobLister struct {
	o         *observer
	in        *driver.ListJobsInput
	limit     int
	after     int64
	status    string
	useStatus bool
}

// queue lists a queue's top-level jobs newest first, resuming below the cursor
// sequence number.
func (l *jobLister) queue(m *Mock) *driver.ListJobsOutput {
	queue := nameFromARN(kindJobQueue, l.in.JobQueue)

	recs := m.jobs.SortedValues()
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Seq > recs[j].Seq })

	out := &driver.ListJobsOutput{Jobs: []driver.Job{}}

	var lastSeq int64

	for i := range recs {
		rec := &recs[i]
		if (l.after >= 0 && rec.Seq >= l.after) || nameFromARN(kindJobQueue, rec.Job.JobQueue) != queue {
			continue
		}

		j := l.o.render(rec, -1)
		if (l.useStatus && j.Status != l.status) || !matchesFilters(&j, l.in.Filters) {
			continue
		}

		if len(out.Jobs) == l.limit {
			out.NextToken = encodeListCursor(lastSeq)

			break
		}

		out.Jobs = append(out.Jobs, j)
		lastSeq = rec.Seq
	}

	return out
}

// children lists an array job's children by index, resuming after the cursor
// index. Filters apply to children the same way they apply to a queue listing.
func (l *jobLister) children(m *Mock) *driver.ListJobsOutput {
	out := &driver.ListJobsOutput{Jobs: []driver.Job{}}

	rec, ok := m.jobs.Get(l.in.ArrayJobID)
	if !ok {
		return out
	}

	for i := int(l.after + 1); i < rec.arraySize(); i++ {
		j := l.o.render(&rec, i)
		if (l.useStatus && j.Status != l.status) || !matchesFilters(&j, l.in.Filters) {
			continue
		}

		if len(out.Jobs) == l.limit {
			out.NextToken = encodeListCursor(int64(*out.Jobs[len(out.Jobs)-1].ArrayProperties.Index))

			break
		}

		out.Jobs = append(out.Jobs, j)
	}

	return out
}

//nolint:gochecknoglobals // immutable enum set, read-only after init
var validJobStatuses = map[string]bool{
	driver.JobStatusSubmitted: true, driver.JobStatusPending: true, driver.JobStatusRunnable: true,
	driver.JobStatusStarting: true, driver.JobStatusRunning: true, driver.JobStatusSucceeded: true,
	driver.JobStatusFailed: true,
}

//nolint:gochecknoglobals // immutable enum set, read-only after init
var validJobFilters = map[string]bool{
	driver.FilterJobName: true, driver.FilterJobDefinition: true, driver.FilterBeforeCreatedAt: true,
	driver.FilterAfterCreatedAt: true, driver.FilterShareIdentifier: true,
}

func validateListJobs(in *driver.ListJobsInput) error {
	set := 0

	for _, v := range []string{in.JobQueue, in.ArrayJobID, in.MultiNodeJobID} {
		if v != "" {
			set++
		}
	}

	if set != 1 {
		return cerrors.New(cerrors.InvalidArgument,
			"specify exactly one of jobQueue, arrayJobId or multiNodeJobId")
	}

	if in.JobStatus != "" && !validJobStatuses[in.JobStatus] {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid jobStatus %q", in.JobStatus)
	}

	return validateJobFilters(in.Filters)
}

// validateJobFilters checks filter names and values. Only one filter may be
// used at a time, except that SHARE_IDENTIFIER combines with the others.
func validateJobFilters(filters []driver.JobFilter) error {
	primary := 0

	for i := range filters {
		f := &filters[i]
		if !validJobFilters[f.Name] {
			return cerrors.Newf(cerrors.InvalidArgument, "invalid filter name %q", f.Name)
		}

		if f.Name != driver.FilterShareIdentifier {
			primary++
		}

		if err := validateCreatedAtValues(f); err != nil {
			return err
		}
	}

	if primary > 1 {
		return cerrors.New(cerrors.InvalidArgument, "only one filter can be used at a time")
	}

	return nil
}

// validateCreatedAtValues requires BEFORE/AFTER_CREATED_AT values to be epoch
// milliseconds.
func validateCreatedAtValues(f *driver.JobFilter) error {
	if f.Name != driver.FilterBeforeCreatedAt && f.Name != driver.FilterAfterCreatedAt {
		return nil
	}

	for _, v := range f.Values {
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return cerrors.Newf(cerrors.InvalidArgument, "%s value %q must be epoch milliseconds", f.Name, v)
		}
	}

	return nil
}

// listStatus returns the status ListJobs filters on. A filter other than
// SHARE_IDENTIFIER disables status filtering.
func listStatus(in *driver.ListJobsInput) (string, bool) {
	for _, f := range in.Filters {
		if f.Name != driver.FilterShareIdentifier {
			return "", false
		}
	}

	if in.JobStatus == "" {
		return driver.JobStatusRunning, true
	}

	return in.JobStatus, true
}

func matchesFilters(j *driver.Job, filters []driver.JobFilter) bool {
	for _, f := range filters {
		if !matchesFilter(j, f) {
			return false
		}
	}

	return true
}

// matchesFilter reports whether a job matches any value of one filter.
func matchesFilter(j *driver.Job, f driver.JobFilter) bool {
	for _, v := range f.Values {
		if matchesFilterValue(j, f.Name, v) {
			return true
		}
	}

	return false
}

func matchesFilterValue(j *driver.Job, name, v string) bool {
	switch name {
	case driver.FilterJobName:
		return matchPrefix(strings.ToLower(j.JobName), strings.ToLower(v))
	case driver.FilterJobDefinition:
		if strings.HasPrefix(v, "arn:") {
			return j.JobDefinition == v
		}

		jdName := nameFromARN(kindJobDefinition, j.JobDefinition)
		if i := strings.LastIndex(jdName, ":"); i >= 0 {
			jdName = jdName[:i]
		}

		return matchPrefix(jdName, v)
	case driver.FilterBeforeCreatedAt:
		n, _ := strconv.ParseInt(v, 10, 64)

		return j.CreatedAt < n
	case driver.FilterAfterCreatedAt:
		n, _ := strconv.ParseInt(v, 10, 64)

		return j.CreatedAt > n
	case driver.FilterShareIdentifier:
		return j.ShareIdentifier == v
	default:
		return false
	}
}

// matchPrefix matches s against pattern, where a trailing '*' makes the
// pattern a prefix.
func matchPrefix(s, pattern string) bool {
	if p, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(s, p)
	}

	return s == pattern
}

// CancelJob cancels a job that hasn't reached STARTING (it moves to FAILED with
// the given reason). A job that is STARTING, RUNNING or finished is left alone
// and the call still succeeds. Canceling an array parent cancels every child
// that hasn't started.
func (m *Mock) CancelJob(ctx context.Context, jobID, reason string) error {
	return m.stopJob(ctx, jobID, reason, false)
}

// TerminateJob moves any unfinished job to FAILED with the given reason (a job
// that hadn't reached STARTING is canceled). Terminating an array parent
// terminates every unfinished child.
func (m *Mock) TerminateJob(ctx context.Context, jobID, reason string) error {
	return m.stopJob(ctx, jobID, reason, true)
}

func (m *Mock) stopJob(ctx context.Context, jobID, reason string, terminate bool) error {
	if err := validateStopArgs(jobID, reason); err != nil {
		return err
	}

	m.jobsMu.Lock()

	rec, idx, ok := m.lookup(jobID)
	if !ok {
		m.jobsMu.Unlock()

		return cerrors.Newf(cerrors.NotFound, "job %q does not exist", jobID)
	}

	sr := stopRequest{now: m.opts.Clock.Now(), reason: reason, terminate: terminate}
	o := m.newObserver(sr.now)

	var (
		next    jobRecord
		changed bool
	)

	if idx >= 0 || rec.arraySize() > 0 {
		next, changed = sr.applyToArray(o, &rec, idx)
	} else {
		next, changed = sr.applyToJob(o, &rec)
	}

	if changed {
		m.jobs.Set(rec.Job.JobID, next)
	}

	m.jobsMu.Unlock()

	m.publishJobEvents(ctx, sr.now)

	return nil
}

func validateStopArgs(jobID, reason string) error {
	switch {
	case jobID == "":
		return cerrors.New(cerrors.InvalidArgument, "jobId is required")
	case reason == "":
		return cerrors.New(cerrors.InvalidArgument, "reason is required")
	case len(reason) > maxReasonLen:
		return cerrors.Newf(cerrors.InvalidArgument, "reason must be at most %d characters", maxReasonLen)
	default:
		return nil
	}
}

// stopRequest is one CancelJob (terminate false) or TerminateJob call.
type stopRequest struct {
	now       time.Time
	reason    string
	terminate bool
}

// affects reports whether the call stops a unit in status: TerminateJob stops
// anything unfinished, CancelJob only what hasn't reached STARTING.
func (sr *stopRequest) affects(status string) bool {
	if sr.terminate {
		return !isTerminal(status)
	}

	return cancellable(status)
}

// stopFor records the stop of a unit currently in v. A terminate of a unit
// that hadn't reached STARTING counts as a cancel.
func (sr *stopRequest) stopFor(v *jobView) jobStop {
	return jobStop{At: sr.now, Reason: sr.reason, Terminate: sr.terminate && !cancellable(v.Status), StartedAt: v.StartedAt}
}

func (sr *stopRequest) applyToJob(o *observer, rec *jobRecord) (jobRecord, bool) {
	v := o.view(rec)
	if !sr.affects(v.Status) {
		return jobRecord{}, false
	}

	next := cloneJobRecord(*rec)
	s := sr.stopFor(&v)
	next.Stop = &s

	return next, true
}

// applyToArray stops one child (idx >= 0) or, for the parent (idx -1), every
// affected child, marking the parent canceled/terminated when any child was.
func (sr *stopRequest) applyToArray(o *observer, rec *jobRecord, idx int) (jobRecord, bool) {
	next := cloneJobRecord(*rec)
	cv := o.childViews(rec)
	changed := false

	for i := range cv {
		if (idx >= 0 && i != idx) || !sr.affects(cv[i].Status) {
			continue
		}

		if next.ChildStops == nil {
			next.ChildStops = map[string]jobStop{}
		}

		next.ChildStops[strconv.Itoa(i)] = sr.stopFor(&cv[i])
		changed = true
	}

	if changed && idx < 0 {
		next.Stop = &jobStop{At: sr.now, Reason: sr.reason, Terminate: sr.terminate}
	}

	return next, changed
}
