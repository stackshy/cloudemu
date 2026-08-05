package kubernetes

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/stackshy/cloudemu/v2/config"
)

// cronBase is an off-boundary reference time (00:00:30) so a freshly created
// CronJob is never immediately "due" at its own creation timestamp.
func cronBase() time.Time {
	return time.Date(2026, time.January, 1, 0, 0, 30, 0, time.UTC)
}

// newCronFixture returns a cluster whose data-plane clock is the returned
// FakeClock, so tests can advance time deterministically between ticks.
func newCronFixture(t *testing.T) (*ClusterState, *config.FakeClock) {
	t.Helper()

	api := NewAPIServer()
	clock := config.NewFakeClock(cronBase())
	api.SetClock(clock)

	_, state := api.RegisterCluster()

	return state, clock
}

// putCronJob inserts a CronJob straight into the store (creationTimestamp from
// the fake clock), returning it so tests can reference its UID.
func putCronJob(t *testing.T, s *ClusterState, name, schedule, policy string, deadlineSecs int64) *unstructured.Unstructured {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	store := s.reg.getStore(apiGroupBatch, "v1", "cronjobs")

	cj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "CronJob",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
		"spec": map[string]any{
			"schedule": schedule,
			"jobTemplate": map[string]any{"spec": map[string]any{
				"template": map[string]any{"spec": map[string]any{
					"containers": []any{map[string]any{"name": "c", "image": "busybox"}},
				}},
			}},
		},
	}}

	if policy != "" {
		_ = unstructured.SetNestedField(cj.Object, policy, "spec", "concurrencyPolicy")
	}

	if deadlineSecs > 0 {
		_ = unstructured.SetNestedField(cj.Object, deadlineSecs, "spec", "startingDeadlineSeconds")
	}

	cj.SetUID(types.UID(newUID()))
	cj.SetCreationTimestamp(s.now())
	store.stampRVLocked(cj)
	store.items[objKey("default", name)] = cj

	return cj
}

// putActiveJob injects a non-terminal Job owned by cj (no Complete/Failed
// condition), simulating a prior run still in flight.
func putActiveJob(t *testing.T, s *ClusterState, cj *unstructured.Unstructured, name string) {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	store := s.reg.getStore(apiGroupBatch, "v1", "jobs")

	job := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
		"status":     map[string]any{"active": int64(1)},
	}}
	job.SetUID(types.UID(newUID()))
	job.SetOwnerReferences([]metav1.OwnerReference{ownerRefOf(cj)})
	store.stampRVLocked(job)
	store.items[objKey("default", name)] = job
}

func TestTickCronJobs_FiresOnceAtBoundary(t *testing.T) {
	state, clock := newCronFixture(t)
	putCronJob(t, state, "backup", "*/5 * * * *", "", 0)

	// Not due yet: only 30s past creation, next slot is 00:05:00.
	state.TickCronJobs()

	if got := countJobs(state); got != 0 {
		t.Fatalf("before boundary: %d jobs, want 0", got)
	}

	// Advance to the 00:05:00 boundary and fire.
	clock.Advance(4*time.Minute + 30*time.Second)
	state.TickCronJobs()

	if got := countJobs(state); got != 1 {
		t.Fatalf("at boundary: %d jobs, want 1", got)
	}

	// Ticking again at the SAME wall-clock time must not double-create.
	state.TickCronJobs()

	if got := countJobs(state); got != 1 {
		t.Fatalf("re-tick same time: %d jobs, want 1 (double-create regression)", got)
	}
}

func TestTickCronJobs_NotDue(t *testing.T) {
	state, _ := newCronFixture(t)
	putCronJob(t, state, "nightly", "0 0 * * *", "", 0)

	// Midnight-only schedule; the clock sits at 00:00:30, so the next due slot is
	// tomorrow — nothing should fire.
	state.TickCronJobs()

	if got := countJobs(state); got != 0 {
		t.Fatalf("not-due schedule: %d jobs, want 0", got)
	}
}

func TestTickCronJobs_ForbidSkipsWhilePriorActive(t *testing.T) {
	state, clock := newCronFixture(t)
	cj := putCronJob(t, state, "report", "*/5 * * * *", concurrencyForbid, 0)
	putActiveJob(t, state, cj, "report-prior")

	clock.Advance(4*time.Minute + 30*time.Second) // reach 00:05:00
	state.TickCronJobs()

	// The prior Job is still active, so Forbid blocks a second Job.
	if got := countJobs(state); got != 1 {
		t.Fatalf("forbid with active job: %d jobs, want 1", got)
	}
}

func TestTickCronJobs_ReplaceDeletesActive(t *testing.T) {
	state, clock := newCronFixture(t)
	cj := putCronJob(t, state, "sync", "*/5 * * * *", concurrencyReplace, 0)
	putActiveJob(t, state, cj, "sync-prior")

	clock.Advance(4*time.Minute + 30*time.Second)
	state.TickCronJobs()

	// Replace deletes the active prior Job and creates the new one: net one Job.
	if got := countJobs(state); got != 1 {
		t.Fatalf("replace: %d jobs, want 1", got)
	}

	if jobExists(state, "sync-prior") {
		t.Fatalf("replace: prior job should have been deleted")
	}
}

func TestTickCronJobs_StartingDeadlineSkipsStaleRun(t *testing.T) {
	state, clock := newCronFixture(t)
	putCronJob(t, state, "ingest", "*/5 * * * *", "", 60) // 60s deadline

	// Jump to 00:22:00 — the most recent slot (00:20:00) is 120s stale, past the
	// 60s deadline, so the missed run is skipped.
	clock.Advance(21*time.Minute + 30*time.Second)
	state.TickCronJobs()

	if got := countJobs(state); got != 0 {
		t.Fatalf("stale run past deadline: %d jobs, want 0", got)
	}

	// A subsequent on-time boundary still fires (deadline only skips stale slots).
	clock.Advance(3 * time.Minute) // 00:25:00
	state.TickCronJobs()

	if got := countJobs(state); got != 1 {
		t.Fatalf("on-time run within deadline: %d jobs, want 1", got)
	}
}

func TestTickCronJobs_SuspendedNeverFires(t *testing.T) {
	state, clock := newCronFixture(t)
	cj := putCronJob(t, state, "paused", "*/5 * * * *", "", 0)

	state.mu.Lock()
	_ = unstructured.SetNestedField(cj.Object, true, "spec", "suspend")
	state.mu.Unlock()

	clock.Advance(10 * time.Minute)
	state.TickCronJobs()

	if got := countJobs(state); got != 0 {
		t.Fatalf("suspended cronjob: %d jobs, want 0", got)
	}
}

func jobExists(state *ClusterState, name string) bool {
	state.mu.RLock()
	defer state.mu.RUnlock()

	st := state.reg.getStore(apiGroupBatch, "v1", "jobs")
	_, ok := st.items[objKey("default", name)]

	return ok
}

// --- cron parser unit tests -------------------------------------------------

func TestParseSchedule_Errors(t *testing.T) {
	for _, spec := range []string{"", "* * * *", "* * * * * *", "bad * * * *", "*/0 * * * *", "60 * * * *", "9-5 * * * *"} {
		if _, err := parseSchedule(spec); err == nil {
			t.Errorf("parseSchedule(%q): want error, got nil", spec)
		}
	}
}

func TestParseCronField_Forms(t *testing.T) {
	tests := []struct {
		name       string
		field      string
		lo, hi     int
		wantMember []int
		wantAbsent []int
	}{
		{"star", "*", 0, 5, []int{0, 3, 5}, nil},
		{"step", "*/15", 0, 59, []int{0, 15, 30, 45}, []int{1, 14, 46}},
		{"list", "0,30", 0, 59, []int{0, 30}, []int{15, 45}},
		{"range", "9-17", 0, 23, []int{9, 13, 17}, []int{8, 18}},
		{"steppedRange", "10-20/5", 0, 59, []int{10, 15, 20}, []int{11, 25}},
		{"openStep", "50/5", 0, 59, []int{50, 55}, []int{45, 49}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			set, err := parseCronField(tc.field, tc.lo, tc.hi)
			if err != nil {
				t.Fatalf("parseCronField(%q): %v", tc.field, err)
			}

			for _, v := range tc.wantMember {
				if !set[v] {
					t.Errorf("%q: expected %d to be a member", tc.field, v)
				}
			}

			for _, v := range tc.wantAbsent {
				if set[v] {
					t.Errorf("%q: expected %d to be absent", tc.field, v)
				}
			}
		})
	}
}

func TestNextAfter_Boundary(t *testing.T) {
	sched, err := parseSchedule("*/5 * * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// From 00:00:30 the next slot is 00:05:00.
	got, err := sched.nextAfter(cronBase())
	if err != nil {
		t.Fatalf("nextAfter: %v", err)
	}

	want := time.Date(2026, time.January, 1, 0, 5, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("nextAfter(%v) = %v, want %v", cronBase(), got, want)
	}

	// From exactly 00:05:00 the next slot is strictly after: 00:10:00.
	got2, _ := sched.nextAfter(want)

	want2 := time.Date(2026, time.January, 1, 0, 10, 0, 0, time.UTC)
	if !got2.Equal(want2) {
		t.Fatalf("nextAfter(%v) = %v, want %v", want, got2, want2)
	}
}

func TestDayMatches_DomOrDowSemantics(t *testing.T) {
	// Both day fields restricted: a day matches if EITHER matches (cron OR rule).
	// 2026-01-01 is a Thursday (weekday 4), day-of-month 1.
	sched, err := parseSchedule("0 0 1 * 0") // day-of-month 1 OR Sunday
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	thu1 := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC) // dom=1 → match
	if !sched.matches(thu1) {
		t.Errorf("expected match on day-of-month 1")
	}

	sun4 := time.Date(2026, time.January, 4, 0, 0, 0, 0, time.UTC) // Sunday → match
	if !sched.matches(sun4) {
		t.Errorf("expected match on Sunday")
	}

	fri2 := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC) // neither → no match
	if sched.matches(fri2) {
		t.Errorf("expected no match on a non-1, non-Sunday day")
	}
}
