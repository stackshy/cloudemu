// End-to-end coverage for the #877 real-cluster parity work: server-side Table
// printing (#871), list-level resourceVersion (#872), event emission (#873),
// the real-looking node (#875), and opt-in staged Pod lifecycle (#874). One walk
// exercises the whole lifecycle a kubectl user drives; a second test guards the
// default (progression-off) invariant that Pods still come up Running at once.

package kubernetes_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

const tableAccept = "application/json;as=Table;v=v1;g=meta.k8s.io"

// progressionFixture is a data plane with a FakeClock and opt-in staged Pod
// lifecycle enabled, exposing the ClusterState so the test can drive Tick().
type progressionFixture struct {
	base  string
	state *kubernetes.ClusterState
	clock *config.FakeClock
}

func newProgressionFixture(t *testing.T) (progressionFixture, func()) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC))

	api := kubernetes.NewAPIServer()
	api.SetClock(fc)
	api.SetLifecycleProgression(true)

	uid, state := api.RegisterCluster()
	ts := httptest.NewServer(api)
	api.SetBaseURL(ts.URL)

	return progressionFixture{base: ts.URL + "/k8s/" + uid, state: state, clock: fc}, ts.Close
}

func doAccept(t *testing.T, method, url, accept string, body []byte) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	return resp
}

func getTable(t *testing.T, url string) metav1.Table {
	t.Helper()

	resp := doAccept(t, http.MethodGet, url, tableAccept, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("table GET %s: status %d", url, resp.StatusCode)
	}

	var table metav1.Table
	mustDecode(t, resp.Body, &table)

	if table.Kind != "Table" || table.APIVersion != "meta.k8s.io/v1" {
		t.Fatalf("expected a meta.k8s.io/v1 Table, got %s/%s", table.APIVersion, table.Kind)
	}

	return table
}

// tableRowByName finds the row whose NAME cell (column 0) equals name.
func tableRowByName(t *testing.T, table metav1.Table, name string) []any {
	t.Helper()

	for _, row := range table.Rows {
		if len(row.Cells) > 0 {
			if s, ok := row.Cells[0].(string); ok && s == name {
				return row.Cells
			}
		}
	}

	t.Fatalf("row %q not found in table (%d rows)", name, len(table.Rows))

	return nil
}

// columnIndex returns the index of the column named name (case-insensitive).
func columnIndex(t *testing.T, table metav1.Table, name string) int {
	t.Helper()

	for i, c := range table.ColumnDefinitions {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}

	t.Fatalf("column %q not found", name)

	return -1
}

//nolint:gocyclo // one cohesive end-to-end walk; splitting hides the lifecycle.
func TestK8sParity_EndToEnd(t *testing.T) {
	f, cleanup := newProgressionFixture(t)
	defer cleanup()

	ns := f.base + "/apis/apps/v1/namespaces/default/deployments"

	// 1. Create a Deployment with 3 replicas.
	depBody := mustJSON(t, map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web"},
		"spec": map[string]any{
			"replicas": 3,
			"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "web"}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "nginx:1.25"}}},
			},
		},
	})

	resp := doAccept(t, http.MethodPost, ns, "", depBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create deployment: status %d", resp.StatusCode)
	}

	resp.Body.Close()

	// 2. GET deployments as Table — columns render, and the list carries a
	//    non-empty resourceVersion (#871 + #872).
	table := getTable(t, ns)
	if table.ResourceVersion == "" {
		t.Fatal("deployment Table has empty list resourceVersion (#872)")
	}

	cells := tableRowByName(t, table, "web")
	if got := cells[columnIndex(t, table, "Ready")]; got != "3/3" {
		t.Fatalf("Deployment READY: got %v, want 3/3", got)
	}

	if got := cells[columnIndex(t, table, "Available")]; got != float64(3) {
		t.Fatalf("Deployment AVAILABLE: got %v, want 3", got)
	}

	// list-level resourceVersion also present on the plain JSON list.
	plain := doAccept(t, http.MethodGet, ns, "", nil)

	var depList map[string]any
	mustDecode(t, plain.Body, &depList)

	if md, _ := depList["metadata"].(map[string]any); md == nil || md["resourceVersion"] == "" || md["resourceVersion"] == nil {
		t.Fatal("plain deployment list missing metadata.resourceVersion (#872)")
	}

	// 3. Rollout status: observedGeneration == generation, Available/Progressing.
	statusResp := doAccept(t, http.MethodGet, ns+"/web/status", "", nil)

	var dep struct {
		Metadata struct {
			Generation int64 `json:"generation"`
		} `json:"metadata"`
		Status struct {
			ObservedGeneration int64 `json:"observedGeneration"`
			AvailableReplicas  int64 `json:"availableReplicas"`
			Conditions         []struct {
				Type, Status string
			} `json:"conditions"`
		} `json:"status"`
	}
	mustDecode(t, statusResp.Body, &dep)

	if dep.Status.ObservedGeneration != dep.Metadata.Generation {
		t.Fatalf("rollout not observed: observedGeneration %d != generation %d",
			dep.Status.ObservedGeneration, dep.Metadata.Generation)
	}

	if !hasTrueCondition(dep.Status.Conditions, "Available") {
		t.Fatal("deployment Available condition not True")
	}

	// 4. Events: ScalingReplicaSet (about the Deployment) + SuccessfulCreate
	//    (about the ReplicaSet) were emitted (#873).
	reasons := eventReasons(t, f.base+"/api/v1/namespaces/default/events")
	if !reasons["ScalingReplicaSet"] {
		t.Fatalf("missing ScalingReplicaSet event; saw %v", reasons)
	}

	if !reasons["SuccessfulCreate"] {
		t.Fatalf("missing SuccessfulCreate event; saw %v", reasons)
	}

	// field selector on involvedObject.name returns the Deployment's events.
	depReasons := eventReasons(t, f.base+"/api/v1/namespaces/default/events?fieldSelector=involvedObject.name=web")
	if !depReasons["ScalingReplicaSet"] {
		t.Fatalf("field-selected events missing ScalingReplicaSet; saw %v", depReasons)
	}

	// 5. Scale to 5 and confirm 5 Running Pods via a Pod Table.
	scaleBody := mustJSON(t, map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web"},
		"spec": map[string]any{
			"replicas": 5,
			"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "web"}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "nginx:1.25"}}},
			},
		},
	})
	doAccept(t, http.MethodPut, ns+"/web", "", scaleBody).Body.Close()

	podTable := getTable(t, f.base+"/api/v1/namespaces/default/pods")
	if len(podTable.Rows) != 5 {
		t.Fatalf("pod Table rows: got %d, want 5", len(podTable.Rows))
	}

	statusIdx := columnIndex(t, podTable, "Status")
	for _, row := range podTable.Rows {
		if row.Cells[statusIdx] != "Running" {
			t.Fatalf("controller Pod STATUS: got %v, want Running", row.Cells[statusIdx])
		}
	}

	// 6. Service selecting the Pods gets 5 endpoint addresses.
	svcBody := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"name": "web"},
		"spec": map[string]any{
			"selector": map[string]any{"app": "web"},
			"ports":    []any{map[string]any{"port": 80}},
		},
	})
	doAccept(t, http.MethodPost, f.base+"/api/v1/namespaces/default/services", "", svcBody).Body.Close()

	if n := endpointAddressCount(t, f.base+"/api/v1/namespaces/default/endpoints/web"); n != 5 {
		t.Fatalf("endpoint addresses: got %d, want 5", n)
	}

	// 7. A Job with completions=2 runs to Complete; its Table shows 2/2.
	jobBody := mustJSON(t, map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{"name": "batch"},
		"spec": map[string]any{
			"completions": 2,
			"template": map[string]any{
				"spec": map[string]any{
					"restartPolicy": "Never",
					"containers":    []any{map[string]any{"name": "c", "image": "busybox"}},
				},
			},
		},
	})
	doAccept(t, http.MethodPost, f.base+"/apis/batch/v1/namespaces/default/jobs", "", jobBody).Body.Close()

	f.clock.Advance(2 * time.Second)
	f.state.Tick()

	jobTable := getTable(t, f.base+"/apis/batch/v1/namespaces/default/jobs")
	jobCells := tableRowByName(t, jobTable, "batch")
	if got := jobCells[columnIndex(t, jobTable, "Completions")]; got != "2/2" {
		t.Fatalf("Job COMPLETIONS: got %v, want 2/2", got)
	}

	if !eventReasons(t, f.base+"/api/v1/namespaces/default/events")["Completed"] {
		t.Fatal("missing Job Completed event")
	}

	// 8. Node looks real: Ready, with a version (#875).
	nodeTable := getTable(t, f.base+"/api/v1/nodes")
	nodeCells := tableRowByName(t, nodeTable, "cloudemu-node-0")
	if got := nodeCells[columnIndex(t, nodeTable, "Status")]; got != "Ready" {
		t.Fatalf("Node STATUS: got %v, want Ready", got)
	}

	// 9. A watch resuming from the current list RV does not replay the existing
	//    Pods (the list-RV anchor, #872).
	listRV := podTable.ResourceVersion
	if added := countWatchAddedWithin(t,
		f.base+"/api/v1/namespaces/default/pods?watch=true&resourceVersion="+listRV, 400*time.Millisecond); added != 0 {
		t.Fatalf("resumed watch replayed %d ADDED events, want 0", added)
	}

	// 10. Opt-in staged lifecycle: a bare Pod starts Pending and advances on Tick.
	podBody := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "bare"},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "busybox"}}},
	})

	createResp := doAccept(t, http.MethodPost, f.base+"/api/v1/namespaces/default/pods", "", podBody)

	var barePod corev1.Pod
	mustDecode(t, createResp.Body, &barePod)

	if barePod.Status.Phase != corev1.PodPending {
		t.Fatalf("staged Pod initial phase: got %q, want Pending", barePod.Status.Phase)
	}

	f.clock.Advance(2 * time.Second)
	f.state.Tick()

	if got := podStatusColumn(t, f, "bare"); got != "ContainerCreating" {
		t.Fatalf("staged Pod after first Tick: got STATUS %q, want ContainerCreating", got)
	}

	f.clock.Advance(2 * time.Second)
	f.state.Tick()

	if got := podStatusColumn(t, f, "bare"); got != "Running" {
		t.Fatalf("staged Pod after second Tick: got STATUS %q, want Running", got)
	}

	// The staged Pod accrued kubelet/scheduler events.
	bareReasons := eventReasons(t, f.base+"/api/v1/namespaces/default/events?fieldSelector=involvedObject.name=bare")
	if !bareReasons["Scheduled"] || !bareReasons["Started"] {
		t.Fatalf("staged Pod missing lifecycle events; saw %v", bareReasons)
	}
}

// TestK8sParity_ProgressionOffInstantRunning guards the default invariant: with
// progression off, controller Pods still come up Running on the create request.
func TestK8sParity_ProgressionOffInstantRunning(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	depBody := mustJSON(t, map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web"},
		"spec": map[string]any{
			"replicas": 2,
			"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "web"}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "nginx"}}},
			},
		},
	})
	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments", depBody).Body.Close()

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods", nil)

	var list corev1.PodList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 2 {
		t.Fatalf("pods: got %d, want 2", len(list.Items))
	}

	for _, p := range list.Items {
		if p.Status.Phase != corev1.PodRunning {
			t.Fatalf("pod %s phase: got %q, want Running (progression off)", p.Name, p.Status.Phase)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func hasTrueCondition(conds []struct{ Type, Status string }, typ string) bool {
	for _, c := range conds {
		if c.Type == typ && c.Status == "True" {
			return true
		}
	}

	return false
}

func eventReasons(t *testing.T, url string) map[string]bool {
	t.Helper()

	resp := doAccept(t, http.MethodGet, url, "", nil)

	var list struct {
		Items []struct {
			Reason string `json:"reason"`
		} `json:"items"`
	}
	mustDecode(t, resp.Body, &list)

	out := map[string]bool{}
	for _, e := range list.Items {
		out[e.Reason] = true
	}

	return out
}

func endpointAddressCount(t *testing.T, url string) int {
	t.Helper()

	resp := doAccept(t, http.MethodGet, url, "", nil)

	var ep corev1.Endpoints
	mustDecode(t, resp.Body, &ep)

	n := 0
	for _, s := range ep.Subsets {
		n += len(s.Addresses)
	}

	return n
}

func podStatusColumn(t *testing.T, f progressionFixture, name string) string {
	t.Helper()

	table := getTable(t, f.base+"/api/v1/namespaces/default/pods/"+name)
	cells := tableRowByName(t, table, name)

	s, _ := cells[columnIndex(t, table, "Status")].(string)

	return s
}

// countWatchAddedWithin opens a streaming watch and counts ADDED events that
// arrive within d, then gives up. A resumed watch should replay nothing.
func countWatchAddedWithin(t *testing.T, url string, d time.Duration) int {
	t.Helper()

	client := &http.Client{Timeout: d}

	resp, err := client.Get(url) //nolint:noctx // bounded by client.Timeout.
	if err != nil {
		// A timeout with no body is fine — it means nothing was streamed.
		return 0
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body) // returns when the timeout closes the stream

	return strings.Count(string(body), `"type":"ADDED"`)
}
