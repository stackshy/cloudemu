// Internal test for Phase 3 CronJob scheduling: TickCronJobs materializes a Job
// from the CronJob's jobTemplate.

package kubernetes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

func TestTickCronJobs_MaterializesJob(t *testing.T) {
	api := NewAPIServer()
	// Deterministic clock so the schedule's due-evaluation is reproducible: the
	// CronJob is created at an off-boundary time, then the clock is advanced to a
	// 5-minute boundary where "*/5 * * * *" is actually due.
	clock := config.NewFakeClock(time.Date(2026, time.January, 1, 0, 0, 30, 0, time.UTC))
	api.SetClock(clock)
	// TickCronJobs self-gates on progression (opt-in time-driven firing).
	api.SetLifecycleProgression(true)
	uid, state := api.RegisterCluster()
	ts := httptest.NewServer(api)

	defer ts.Close()

	// Create a CronJob via the HTTP path.
	cj := map[string]any{
		"apiVersion": "batch/v1", "kind": "CronJob",
		"metadata": map[string]any{"name": "backup"},
		"spec": map[string]any{
			"schedule": "*/5 * * * *",
			"jobTemplate": map[string]any{
				"spec": map[string]any{
					"template": map[string]any{
						"spec": map[string]any{
							"containers": []any{map[string]any{"name": "c", "image": "busybox"}},
						},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(cj)
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/k8s/"+uid+"/apis/batch/v1/namespaces/default/cronjobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create cronjob: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create cronjob: status %d", resp.StatusCode)
	}

	// No Jobs yet (no scheduler has fired).
	if got := countJobs(state); got != 0 {
		t.Fatalf("before tick: %d jobs, want 0", got)
	}

	// Not due yet at creation time; only a boundary crossing fires the schedule.
	state.TickCronJobs()

	if got := countJobs(state); got != 0 {
		t.Fatalf("before boundary: %d jobs, want 0", got)
	}

	// Advance to the 00:05:00 boundary and fire the schedule once.
	clock.Advance(4*time.Minute + 30*time.Second)
	state.TickCronJobs()

	if got := countJobs(state); got != 1 {
		t.Fatalf("after tick: %d jobs, want 1", got)
	}
}

func countJobs(state *ClusterState) int {
	state.mu.RLock()
	defer state.mu.RUnlock()

	st := state.reg.getStore(apiGroupBatch, "v1", "jobs")
	if st == nil {
		return 0
	}

	return len(st.items)
}
