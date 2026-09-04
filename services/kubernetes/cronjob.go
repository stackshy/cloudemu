package kubernetes

import (
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// CronJob scheduling. cloudemu runs synchronously with no background timer, so
// callers drive the scheduler by calling TickCronJobs (reachable via
// APIServer.Lookup(uid)) — advancing the injected clock between ticks. Each tick
// evaluates every CronJob's cron `spec.schedule` against the clock and only
// materializes a Job when a scheduled time falls in (lastScheduleTime, now],
// honoring concurrencyPolicy and startingDeadlineSeconds. This makes scheduling
// real due-evaluation rather than "fire every CronJob on every tick".

// Concurrency policies (batch/v1 CronJobSpec.concurrencyPolicy). Allow is the
// default when the field is empty.
const (
	concurrencyForbid  = "Forbid"
	concurrencyReplace = "Replace"

	// maxCatchupIterations bounds the scan for the most-recent due slot when many
	// scheduled times were missed between ticks (a large clock jump), so a tiny
	// interval over a huge gap can't spin unbounded.
	maxCatchupIterations = 1000
)

// TickCronJobs evaluates every non-suspended CronJob against the cluster clock,
// creating a Job for any schedule that is due since its last run. Safe for
// external callers.
func (s *ClusterState) TickCronJobs() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Defense-in-depth self-gate (mirrors Tick's at lifecycle.go): CronJob firing
	// is opt-in time-driven behavior, so it stays a no-op unless progression is on
	// — even if a caller other than the gated serve ticker reaches this.
	if !s.lifecycleProgression {
		return
	}

	cronStore := s.reg.getStore(apiGroupBatch, "v1", "cronjobs")
	jobStore := s.reg.getStore(apiGroupBatch, "v1", "jobs")

	if cronStore == nil || jobStore == nil {
		return
	}

	now := s.now().Time
	for _, cj := range cronStore.items {
		s.evaluateCronJobLocked(cj, jobStore, now)
	}
}

// evaluateCronJobLocked runs the due-check + concurrency gates for one CronJob
// and fires it at most once. Callers hold s.mu.
func (s *ClusterState) evaluateCronJobLocked(cj *unstructured.Unstructured, jobStore *registryStore, now time.Time) {
	if suspended, _, _ := unstructured.NestedBool(cj.Object, "spec", "suspend"); suspended {
		return
	}

	scheduleStr, _, _ := unstructured.NestedString(cj.Object, "spec", "schedule")

	sched, err := parseSchedule(scheduleStr)
	if err != nil {
		return
	}

	fireTime, due := dueSchedule(sched, lastScheduleOrCreation(cj), now)
	if !due {
		return
	}

	// A run whose scheduled time is already older than startingDeadlineSeconds is
	// missed: record it so it isn't re-evaluated, but don't run it.
	if missedStartingDeadline(cj, fireTime, now) {
		setLastScheduleTime(cj, fireTime)

		return
	}

	// Forbid with a still-active prior Job skips this run WITHOUT advancing
	// lastScheduleTime, so the slot fires once the prior Job finishes.
	if !s.applyConcurrencyLocked(cj, jobStore) {
		return
	}

	s.fireCronJobLocked(cj, jobStore, fireTime)
}

// dueSchedule reports the most recent scheduled time in (last, now], if any. The
// most-recent (not earliest) slot is chosen so a large clock jump collapses a
// backlog into a single run, matching the upstream controller.
func dueSchedule(sched *cronSchedule, last, now time.Time) (time.Time, bool) {
	fire, err := sched.nextAfter(last)
	if err != nil || fire.After(now) {
		return time.Time{}, false
	}

	for range maxCatchupIterations {
		following, ferr := sched.nextAfter(fire)
		if ferr != nil || following.After(now) {
			break
		}

		fire = following
	}

	return fire, true
}

// lastScheduleOrCreation is the reference time the next due slot is measured
// from: status.lastScheduleTime once set, else the CronJob's creation time.
func lastScheduleOrCreation(cj *unstructured.Unstructured) time.Time {
	if str, found, _ := unstructured.NestedString(cj.Object, "status", "lastScheduleTime"); found && str != "" {
		if t, err := time.Parse(time.RFC3339, str); err == nil {
			return t
		}
	}

	return cj.GetCreationTimestamp().Time
}

// setLastScheduleTime records the fired (or missed) slot on status so the next
// tick at the same wall-clock time doesn't re-create the Job.
func setLastScheduleTime(cj *unstructured.Unstructured, t time.Time) {
	_ = unstructured.SetNestedField(cj.Object, t.UTC().Format(time.RFC3339), "status", "lastScheduleTime")
}

// missedStartingDeadline reports whether the due slot is older than the
// CronJob's startingDeadlineSeconds relative to now (unset = no deadline).
func missedStartingDeadline(cj *unstructured.Unstructured, fireTime, now time.Time) bool {
	secs, found, _ := unstructured.NestedInt64(cj.Object, "spec", "startingDeadlineSeconds")
	if !found {
		return false
	}

	return now.Sub(fireTime) > time.Duration(secs)*time.Second
}

// applyConcurrencyLocked enforces concurrencyPolicy against the CronJob's active
// Jobs and reports whether this run may proceed. Forbid blocks while a prior Job
// is active; Replace deletes active Jobs first; Allow (default) always proceeds.
// Callers hold s.mu.
func (s *ClusterState) applyConcurrencyLocked(cj *unstructured.Unstructured, jobStore *registryStore) bool {
	active := activeJobsFor(cj, jobStore)
	if len(active) == 0 {
		return true
	}

	policy, _, _ := unstructured.NestedString(cj.Object, "spec", "concurrencyPolicy")

	switch policy {
	case concurrencyForbid:
		return false
	case concurrencyReplace:
		for _, job := range active {
			s.deleteJobLocked(job, jobStore)
		}

		return true
	default:
		return true
	}
}

// activeJobsFor returns the CronJob's owned Jobs that are not yet terminal
// (no Complete/Failed condition). Callers hold s.mu.
func activeJobsFor(cj *unstructured.Unstructured, jobStore *registryStore) []*unstructured.Unstructured {
	uid := cj.GetUID()

	var active []*unstructured.Unstructured

	for _, job := range jobStore.items {
		if ownedBy(job.GetOwnerReferences(), uid) && !jobTerminal(job) {
			active = append(active, job)
		}
	}

	return active
}

// jobTerminal reports whether a Job has finished (Complete or Failed True).
func jobTerminal(job *unstructured.Unstructured) bool {
	return jobHasCondition(job, "Complete") || jobHasCondition(job, "Failed")
}

// deleteJobLocked removes a Job and cascade-collects its Pods (Replace policy).
// Callers hold s.mu.
func (s *ClusterState) deleteJobLocked(job *unstructured.Unstructured, jobStore *registryStore) {
	s.stampRegistryRVLocked(job)
	delete(jobStore.items, objKey(job.GetNamespace(), job.GetName()))
	s.garbageCollectLocked(job.GetUID())
	jobStore.watch.publish(EventDeleted, job.GetNamespace(), *job.DeepCopy())
}

// fireCronJobLocked materializes one Job from the CronJob's jobTemplate, runs the
// Job reconciler, and records the fired schedule time. Callers hold s.mu.
func (s *ClusterState) fireCronJobLocked(cj *unstructured.Unstructured, jobStore *registryStore, fireTime time.Time) {
	jobSpec, found, _ := unstructured.NestedMap(cj.Object, "spec", "jobTemplate", "spec")
	if !found {
		return
	}

	ns := cj.GetNamespace()
	jobName := cj.GetName() + "-" + shortID()

	job := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata":   map[string]any{"name": jobName, "namespace": ns},
		"spec":       jobSpec,
	}}
	job.SetUID(types.UID(newUID()))
	job.SetCreationTimestamp(s.now())
	job.SetOwnerReferences([]metav1.OwnerReference{ownerRefOf(cj)})

	s.stampRegistryRVLocked(job)
	jobStore.items[objKey(ns, jobName)] = job
	reconcileJob(s, job)
	jobStore.watch.publish(EventAdded, ns, *job.DeepCopy())
	s.recordEventLocked(objectReferenceForUnstructured(cj), "SuccessfulCreate",
		"Created job "+jobName)

	setLastScheduleTime(cj, fireTime)

	// Trim finished Jobs beyond the history limits so `kubectl get jobs` self-
	// trims and a long-lived `serve` doesn't accumulate Jobs unbounded.
	s.pruneJobHistoryLocked(cj, jobStore)
}

// Default CronJob history limits (batch/v1 CronJobSpec), applied when the field
// is unset — how many finished Jobs the controller keeps per CronJob.
const (
	defaultSuccessfulJobsHistoryLimit = 3
	defaultFailedJobsHistoryLimit     = 1
)

// pruneJobHistoryLocked deletes the CronJob's finished Jobs beyond its
// successfulJobsHistoryLimit / failedJobsHistoryLimit (defaults 3 / 1), keeping
// the most recent of each. Active Jobs are never touched. Callers hold s.mu.
func (s *ClusterState) pruneJobHistoryLocked(cj *unstructured.Unstructured, jobStore *registryStore) {
	uid := cj.GetUID()

	var succeeded, failed []*unstructured.Unstructured

	for _, job := range jobStore.items {
		if !ownedBy(job.GetOwnerReferences(), uid) {
			continue
		}

		switch {
		case jobHasCondition(job, "Complete"):
			succeeded = append(succeeded, job)
		case jobHasCondition(job, "Failed"):
			failed = append(failed, job)
		}
	}

	s.trimFinishedJobsLocked(succeeded, jobHistoryLimit(cj, "successfulJobsHistoryLimit",
		defaultSuccessfulJobsHistoryLimit), jobStore)
	s.trimFinishedJobsLocked(failed, jobHistoryLimit(cj, "failedJobsHistoryLimit",
		defaultFailedJobsHistoryLimit), jobStore)
}

// trimFinishedJobsLocked deletes all but the `limit` most-recent Jobs from a set
// of finished Jobs, sorting oldest-first (creationTimestamp, then name) for a
// deterministic choice of which to drop. Callers hold s.mu.
func (s *ClusterState) trimFinishedJobsLocked(jobs []*unstructured.Unstructured, limit int, jobStore *registryStore) {
	if len(jobs) <= limit {
		return
	}

	sort.Slice(jobs, func(i, j int) bool {
		ti := jobs[i].GetCreationTimestamp().Time
		tj := jobs[j].GetCreationTimestamp().Time

		if !ti.Equal(tj) {
			return ti.Before(tj)
		}

		return jobs[i].GetName() < jobs[j].GetName()
	})

	for _, job := range jobs[:len(jobs)-limit] {
		s.deleteJobLocked(job, jobStore)
	}
}

// jobHistoryLimit reads a CronJob history-limit field, falling back to def when
// unset (a negative value clamps to 0 — keep none).
func jobHistoryLimit(cj *unstructured.Unstructured, field string, def int) int {
	n, found, _ := unstructured.NestedInt64(cj.Object, "spec", field)
	if !found {
		return def
	}

	if n < 0 {
		return 0
	}

	return int(n)
}

// jobHasCondition reports whether a Job carries the named condition set True.
func jobHasCondition(job *unstructured.Unstructured, condType string) bool {
	conds, _, _ := unstructured.NestedSlice(job.Object, "status", "conditions")
	for _, raw := range conds {
		cond, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		ctype, _, _ := unstructured.NestedString(cond, "type")
		cstatus, _, _ := unstructured.NestedString(cond, "status")

		if ctype == condType && cstatus == "True" {
			return true
		}
	}

	return false
}
