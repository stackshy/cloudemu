package kubernetes

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// CronJob scheduling. cloudemu runs synchronously with no background timer, so
// a CronJob's cron schedule string is stored but not evaluated on a wall clock.
// Instead, TickCronJobs simulates the scheduler firing once — every
// non-suspended CronJob materializes a Job from its jobTemplate (which the Job
// reconciler then runs to completion). Tests and tools drive scheduling by
// calling TickCronJobs (reachable via APIServer.Lookup(uid)), the same way
// `kubectl create job --from=cronjob/x` triggers an ad-hoc run.

// TickCronJobs fires every non-suspended CronJob once, creating a Job from its
// jobTemplate and updating status.lastScheduleTime. Safe for external callers.
func (s *ClusterState) TickCronJobs() {
	s.mu.Lock()
	defer s.mu.Unlock()

	cronStore := s.reg.getStore(apiGroupBatch, "v1", "cronjobs")
	jobStore := s.reg.getStore(apiGroupBatch, "v1", "jobs")

	if cronStore == nil || jobStore == nil {
		return
	}

	for _, cj := range cronStore.items {
		if suspended, _, _ := unstructured.NestedBool(cj.Object, "spec", "suspend"); suspended {
			continue
		}

		s.fireCronJobLocked(cj, jobStore)
	}
}

// fireCronJobLocked materializes one Job from the CronJob's jobTemplate, runs the
// Job reconciler, and records the schedule time. Callers hold s.mu.
func (s *ClusterState) fireCronJobLocked(cj *unstructured.Unstructured, jobStore *registryStore) {
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

	jobStore.stampRVLocked(job)
	jobStore.items[objKey(ns, jobName)] = job
	reconcileJob(s, job)
	jobStore.watch.publish(EventAdded, ns, *job.DeepCopy())

	_ = unstructured.SetNestedField(cj.Object, s.now().Format(time.RFC3339), "status", "lastScheduleTime")
}
