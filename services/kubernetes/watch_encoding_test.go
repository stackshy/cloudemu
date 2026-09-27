// Watch wire-encoding tests. A real kube-apiserver frames every watch event as
// {"type":"<TYPE>","object":{<the resource JSON>}}, one JSON document per line,
// with Content-Type application/json. These tests drive typed kinds (Pods,
// Deployments, ConfigMaps, Namespaces) and registry kinds (Nodes, ReplicaSets,
// StatefulSets) over raw HTTP and through real client-go watches and informers.

package kubernetes_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

const (
	watchTestTimeout = 5 * time.Second
	tableWatchAccept = "application/json;as=Table;v=v1;g=meta.k8s.io,application/json"
)

// newWatchFixture is newFixture with keep-alives off and client connections
// closed on cleanup, so open watch streams never stall the server shutdown.
func newWatchFixture(t *testing.T) string {
	t.Helper()

	base, _ := newWatchFixtureAPI(t)

	return base
}

// newWatchFixtureAPI is newWatchFixture that also returns the APIServer, for
// tests that snapshot and restore it.
func newWatchFixtureAPI(t *testing.T) (string, *kubernetes.APIServer) {
	t.Helper()

	api := kubernetes.NewAPIServer()
	uid, _ := api.RegisterCluster()
	ts := httptest.NewServer(api)
	ts.Config.SetKeepAlivesEnabled(false)
	api.SetBaseURL(ts.URL)

	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})

	return ts.URL + "/k8s/" + uid, api
}

// watchKind describes one resource kind the encoding tests exercise.
type watchKind struct {
	name       string
	kind       string
	apiVersion string
	listPath   string
	// create builds the create request for a named object; nil for kinds the
	// cluster seeds itself (Nodes), where update mutates the seeded object.
	create func(name string) (path string, body []byte)
	// seeded is the name of an object that already exists (Nodes).
	seeded string
	// update, when set, mutates the seeded object with a merge patch.
	update func() (path string, body []byte)
}

func encodingKinds() []watchKind {
	return []watchKind{
		{
			name: "typed_pods", kind: "Pod", apiVersion: "v1", listPath: "/api/v1/namespaces/default/pods",
			create: func(n string) (string, []byte) {
				return "/api/v1/namespaces/default/pods", []byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"` + n +
					`"},"spec":{"containers":[{"name":"c","image":"nginx"}]}}`)
			},
		},
		{
			name: "typed_deployments", kind: "Deployment", apiVersion: "apps/v1", listPath: "/apis/apps/v1/namespaces/default/deployments",
			create: func(n string) (string, []byte) {
				return "/apis/apps/v1/namespaces/default/deployments", []byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"` +
					n + `"},"spec":{"replicas":1,"selector":{"matchLabels":{"app":"` + n + `"}},"template":{"metadata":{"labels":{"app":"` +
					n + `"}},"spec":{"containers":[{"name":"c","image":"nginx"}]}}}}`)
			},
		},
		{
			name: "typed_configmaps", kind: "ConfigMap", apiVersion: "v1", listPath: "/api/v1/namespaces/default/configmaps",
			create: func(n string) (string, []byte) {
				return "/api/v1/namespaces/default/configmaps", []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"` + n + `"}}`)
			},
		},
		{
			name: "typed_namespaces", kind: "Namespace", apiVersion: "v1", listPath: "/api/v1/namespaces",
			create: func(n string) (string, []byte) {
				return "/api/v1/namespaces", []byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"` + n + `"}}`)
			},
		},
		{
			name: "registry_replicasets", kind: "ReplicaSet", apiVersion: "apps/v1", listPath: "/apis/apps/v1/namespaces/default/replicasets",
			create: func(n string) (string, []byte) {
				return "/apis/apps/v1/namespaces/default/replicasets", []byte(`{"apiVersion":"apps/v1","kind":"ReplicaSet","metadata":{"name":"` +
					n + `"},"spec":{"replicas":0,"selector":{"matchLabels":{"app":"` + n + `"}},"template":{"metadata":{"labels":{"app":"` +
					n + `"}},"spec":{"containers":[{"name":"c","image":"nginx"}]}}}}`)
			},
		},
		{
			name: "registry_statefulsets", kind: "StatefulSet", apiVersion: "apps/v1", listPath: "/apis/apps/v1/namespaces/default/statefulsets",
			create: func(n string) (string, []byte) {
				return "/apis/apps/v1/namespaces/default/statefulsets", []byte(`{"apiVersion":"apps/v1","kind":"StatefulSet","metadata":{"name":"` +
					n + `"},"spec":{"replicas":0,"serviceName":"s","selector":{"matchLabels":{"app":"` + n +
					`"}},"template":{"metadata":{"labels":{"app":"` + n + `"}},"spec":{"containers":[{"name":"c","image":"nginx"}]}}}}`)
			},
		},
		{
			name: "registry_nodes", kind: "Node", apiVersion: "v1", listPath: "/api/v1/nodes", seeded: "cloudemu-node-0",
			update: func() (string, []byte) {
				return "/api/v1/nodes/cloudemu-node-0", []byte(`{"metadata":{"labels":{"watch-test":"yes"}}}`)
			},
		},
	}
}

// rawEvent is one decoded watch event with the object kept generic, so a test
// can assert the object is the resource itself and not a wrapper around it.
type rawEvent struct {
	Type   string         `json:"type"`
	Object map[string]any `json:"object"`
}

func (e rawEvent) name() string {
	md, _ := e.Object["metadata"].(map[string]any)
	n, _ := md["name"].(string)

	return n
}

func (e rawEvent) rv() string {
	md, _ := e.Object["metadata"].(map[string]any)
	v, _ := md["resourceVersion"].(string)

	return v
}

// openWatch starts a watch request and returns its response plus a line reader.
func openWatch(t *testing.T, ctx context.Context, url, accept string) (*http.Response, *bufio.Reader) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Close = true
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("watch %s: %v", url, err)
	}

	t.Cleanup(func() { resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("watch %s: status %d", url, resp.StatusCode)
	}

	return resp, bufio.NewReader(resp.Body)
}

// nextEvent reads one newline-terminated watch event. Every event must be a
// complete JSON document on its own line (the apiserver's JSON stream framing).
func nextEvent(t *testing.T, br *bufio.Reader) rawEvent {
	t.Helper()

	line, err := br.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read watch line: %v (partial %q)", err, line)
	}

	var ev rawEvent
	if err := json.Unmarshal(bytes.TrimSpace(line), &ev); err != nil {
		t.Fatalf("watch line is not one JSON event: %v: %s", err, line)
	}

	return ev
}

// assertResourceObject fails unless ev.object is the resource JSON itself.
func assertResourceObject(t *testing.T, ev rawEvent, wk watchKind) {
	t.Helper()

	if _, wrapped := ev.Object["Object"]; wrapped {
		t.Fatalf("%s event object is wrapped in an Object field: %v", ev.Type, ev.Object)
	}

	if ev.Object["kind"] != wk.kind || ev.Object["apiVersion"] != wk.apiVersion {
		t.Fatalf("%s event object: kind=%v apiVersion=%v, want %s %s", ev.Type,
			ev.Object["kind"], ev.Object["apiVersion"], wk.kind, wk.apiVersion)
	}

	if ev.name() == "" || ev.rv() == "" {
		t.Fatalf("%s event object missing metadata.name/resourceVersion: %v", ev.Type, ev.Object)
	}
}

// mutate creates a fresh object of the kind (or updates the seeded one) and
// returns the name it touched.
func mutate(t *testing.T, base string, wk watchKind, name string) string {
	t.Helper()

	if wk.create != nil {
		path, body := wk.create(name)
		resp := do(t, http.MethodPost, base+path, body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf("create %s %s: status %d", wk.kind, name, resp.StatusCode)
		}

		return name
	}

	path, body := wk.update()

	req, err := http.NewRequest(http.MethodPatch, base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new patch request: %v", err)
	}

	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch %s: %v", wk.kind, err)
	}

	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch %s: status %d", wk.kind, resp.StatusCode)
	}

	return wk.seeded
}

// readUntilName reads events until one for name arrives.
func readUntilName(t *testing.T, br *bufio.Reader, name string) rawEvent {
	t.Helper()

	for {
		ev := nextEvent(t, br)
		if ev.name() == name {
			return ev
		}
	}
}

// TestWatchEncoding_ObjectIsResourceJSON covers both the initial ADDED replay
// and a live event for every kind: object must be the resource JSON, not a
// {"Object":{...}} wrapper (which client-go rejects as "Object 'Kind' is
// missing" and kubectl prints as an ERROR).
func TestWatchEncoding_ObjectIsResourceJSON(t *testing.T) {
	for _, wk := range encodingKinds() {
		t.Run(wk.name, func(t *testing.T) {
			base := newWatchFixture(t)

			first := wk.seeded
			if first == "" {
				first = mutate(t, base, wk, "first")
			}

			ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
			defer cancel()

			resp, br := openWatch(t, ctx, base+wk.listPath+"?watch=true&allowWatchBookmarks=true", "")

			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("watch Content-Type: got %q, want application/json", ct)
			}

			// Initial replay up to the post-sync BOOKMARK.
			sawFirst := false

			for {
				ev := nextEvent(t, br)
				if ev.Type == "BOOKMARK" {
					break
				}

				if ev.Type != "ADDED" {
					t.Fatalf("initial replay event type %q, want ADDED", ev.Type)
				}

				assertResourceObject(t, ev, wk)

				sawFirst = sawFirst || ev.name() == first
			}

			if !sawFirst {
				t.Fatalf("initial replay did not include %q", first)
			}

			// A live event after the sync.
			live := readUntilName(t, br, mutate(t, base, wk, "second"))
			assertResourceObject(t, live, wk)
		})
	}
}

// TestWatchEncoding_ResourceVersionSemantics checks resourceVersion=0 replays
// current state, a resume from the list resourceVersion does not, and live
// events arrive in strictly increasing resourceVersion order.
func TestWatchEncoding_ResourceVersionSemantics(t *testing.T) {
	base := newWatchFixture(t)
	wk := encodingKinds()[4] // registry ReplicaSets

	mutate(t, base, wk, "pre")

	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	defer cancel()

	// resourceVersion=0: the current state is replayed as ADDED.
	_, br0 := openWatch(t, ctx, base+wk.listPath+"?watch=true&resourceVersion=0", "")
	if ev := nextEvent(t, br0); ev.Type != "ADDED" || ev.name() != "pre" {
		t.Fatalf("rv=0 first event: %s %q, want ADDED pre", ev.Type, ev.name())
	}

	// Resume from the list resourceVersion: no replay, only new events.
	listResp := do(t, http.MethodGet, base+wk.listPath, nil)

	var list struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}

	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	listResp.Body.Close()

	_, br := openWatch(t, ctx, base+wk.listPath+"?watch=true&resourceVersion="+list.Metadata.ResourceVersion, "")

	last := 0

	for _, n := range []string{"a", "b", "c"} {
		mutate(t, base, wk, n)

		ev := nextEvent(t, br)
		if ev.name() != n {
			t.Fatalf("resumed watch event for %q, want %q (no replay of existing objects)", ev.name(), n)
		}

		rv, err := strconv.Atoi(ev.rv())
		if err != nil || rv <= last {
			t.Fatalf("event resourceVersion %q not increasing past %d", ev.rv(), last)
		}

		last = rv
	}
}

// TestWatchEncoding_ProtobufAcceptFallsBackToJSON: the emulator serves JSON
// only, so a client that prefers protobuf (client-go's protobuf content config
// sends "application/vnd.kubernetes.protobuf, application/json") must still get
// a JSON watch stream rather than an error.
func TestWatchEncoding_ProtobufAcceptFallsBackToJSON(t *testing.T) {
	base := newWatchFixture(t)
	wk := encodingKinds()[6] // registry Nodes

	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	defer cancel()

	for _, accept := range []string{
		runtime.ContentTypeProtobuf + ";stream=watch," + runtime.ContentTypeJSON,
		runtime.ContentTypeProtobuf,
	} {
		resp, br := openWatch(t, ctx, base+wk.listPath+"?watch=true", accept)

		if ct := resp.Header.Get("Content-Type"); ct != runtime.ContentTypeJSON {
			t.Fatalf("Accept %q: Content-Type %q, want application/json", accept, ct)
		}

		assertResourceObject(t, nextEvent(t, br), wk)
	}
}

// TestWatchEncoding_TableWatch: `kubectl get -w` asks for Table watch events.
// kube-apiserver converts each event object into a one-row meta.k8s.io/v1
// Table and sends the column definitions only on the first event.
func TestWatchEncoding_TableWatch(t *testing.T) {
	kinds := encodingKinds()

	for _, wk := range []watchKind{kinds[0], kinds[6]} { // typed Pods, registry Nodes
		t.Run(wk.name, func(t *testing.T) {
			base := newWatchFixture(t)

			first := wk.seeded
			if first == "" {
				first = mutate(t, base, wk, "first")
			}

			ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
			defer cancel()

			_, br := openWatch(t, ctx, base+wk.listPath+"?watch=true", tableWatchAccept)

			ev := nextEvent(t, br)
			assertTableEvent(t, ev, first, true)

			second := mutate(t, base, wk, "second")

			ev = nextEvent(t, br)
			assertTableEvent(t, ev, second, false)
		})
	}
}

func assertTableEvent(t *testing.T, ev rawEvent, name string, wantHeaders bool) {
	t.Helper()

	if ev.Object["kind"] != "Table" || ev.Object["apiVersion"] != "meta.k8s.io/v1" {
		t.Fatalf("table watch %s event object kind=%v apiVersion=%v, want Table meta.k8s.io/v1",
			ev.Type, ev.Object["kind"], ev.Object["apiVersion"])
	}

	cols, _ := ev.Object["columnDefinitions"].([]any)
	if wantHeaders != (len(cols) > 0) {
		t.Fatalf("table watch %s event: %d column definitions, want headers=%v", ev.Type, len(cols), wantHeaders)
	}

	rows, _ := ev.Object["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("table watch %s event: %d rows, want 1", ev.Type, len(rows))
	}

	row, _ := rows[0].(map[string]any)
	obj, _ := row["object"].(map[string]any)
	md, _ := obj["metadata"].(map[string]any)

	if md["name"] != name {
		t.Fatalf("table watch %s event row object name %v, want %q", ev.Type, md["name"], name)
	}
}

// TestWatchEncoding_ClientGoWatch drives real client-go typed watches, in both
// JSON and protobuf-preferring content configs, over several kinds.
func TestWatchEncoding_ClientGoWatch(t *testing.T) {
	for _, protobuf := range []bool{false, true} {
		t.Run("protobuf="+strconv.FormatBool(protobuf), func(t *testing.T) {
			base := newWatchFixture(t)
			cs := newClientset(t, base, protobuf)

			ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
			defer cancel()

			nodeW, err := cs.CoreV1().Nodes().Watch(ctx, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("watch nodes: %v", err)
			}
			defer nodeW.Stop()

			expectTyped[*corev1.Node](t, nodeW, watch.Added, "cloudemu-node-0")

			rsW, err := cs.AppsV1().ReplicaSets("default").Watch(ctx, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("watch replicasets: %v", err)
			}
			defer rsW.Stop()

			depW, err := cs.AppsV1().Deployments("default").Watch(ctx, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("watch deployments: %v", err)
			}
			defer depW.Stop()

			podW, err := cs.CoreV1().Pods("default").Watch(ctx, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("watch pods: %v", err)
			}
			defer podW.Stop()

			replicas := int32(1)
			dep := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "web"},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
						Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "nginx"}}},
					},
				},
			}

			if _, err := cs.AppsV1().Deployments("default").Create(ctx, dep, metav1.CreateOptions{}); err != nil {
				t.Fatalf("create deployment: %v", err)
			}

			expectTyped[*appsv1.Deployment](t, depW, watch.Added, "web")
			expectTypedPrefix[*appsv1.ReplicaSet](t, rsW, watch.Added, "web-")
			expectTypedPrefix[*corev1.Pod](t, podW, watch.Added, "web-")

			node, err := cs.CoreV1().Nodes().Get(ctx, "cloudemu-node-0", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get node: %v", err)
			}

			node.Labels["watch-test"] = "yes"

			if _, err := cs.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{}); err != nil {
				t.Fatalf("update node: %v", err)
			}

			got := expectTyped[*corev1.Node](t, nodeW, watch.Modified, "cloudemu-node-0")
			if got.Labels["watch-test"] != "yes" {
				t.Fatalf("modified node labels: %v", got.Labels)
			}
		})
	}
}

// TestWatchEncoding_ClientGoInformers runs shared informers (reflector
// list+watch) for Nodes and ReplicaSets and checks they observe live changes.
func TestWatchEncoding_ClientGoInformers(t *testing.T) {
	base := newWatchFixture(t)
	cs := newClientset(t, base, false)

	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	defer cancel()

	factory := informers.NewSharedInformerFactory(cs, 0)

	nodeUpdated := make(chan string, 8)
	rsAdded := make(chan string, 8)

	if _, err := factory.Core().V1().Nodes().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(_, obj any) {
			if n, ok := obj.(*corev1.Node); ok && n.Labels["watch-test"] == "yes" {
				nodeUpdated <- n.Name
			}
		},
	}); err != nil {
		t.Fatalf("node handler: %v", err)
	}

	if _, err := factory.Apps().V1().ReplicaSets().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if rs, ok := obj.(*appsv1.ReplicaSet); ok {
				rsAdded <- rs.Name
			}
		},
	}); err != nil {
		t.Fatalf("rs handler: %v", err)
	}

	factory.Start(ctx.Done())

	for typ, ok := range factory.WaitForCacheSync(ctx.Done()) {
		if !ok {
			t.Fatalf("informer %v did not sync", typ)
		}
	}

	defer factory.Shutdown()
	defer cancel()

	wk := encodingKinds()
	mutate(t, base, wk[6], "")
	mutate(t, base, wk[4], "late-rs")

	waitName(ctx, t, nodeUpdated, "cloudemu-node-0", "node informer update")
	waitName(ctx, t, rsAdded, "late-rs", "replicaset informer add")
}

func waitName(ctx context.Context, t *testing.T, ch <-chan string, want, what string) {
	t.Helper()

	for {
		select {
		case got := <-ch:
			if got == want {
				return
			}
		case <-ctx.Done():
			t.Fatalf("%s: never observed %q", what, want)
		}
	}
}

func newClientset(t *testing.T, base string, protobuf bool) *k8sclient.Clientset {
	t.Helper()

	cfg := &rest.Config{Host: base}
	if protobuf {
		cfg.ContentType = runtime.ContentTypeProtobuf
		cfg.AcceptContentTypes = runtime.ContentTypeProtobuf + "," + runtime.ContentTypeJSON
	}

	cs, err := k8sclient.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}

	return cs
}

// expectTyped waits for an event of type typ whose object is a T named name.
func expectTyped[T metav1.Object](t *testing.T, w watch.Interface, typ watch.EventType, name string) T {
	t.Helper()

	return expectTypedMatch[T](t, w, typ, func(n string) bool { return n == name }, name)
}

// expectTypedPrefix is expectTyped matching a generated-name prefix.
func expectTypedPrefix[T metav1.Object](t *testing.T, w watch.Interface, typ watch.EventType, prefix string) T {
	t.Helper()

	return expectTypedMatch[T](t, w, typ, func(n string) bool { return strings.HasPrefix(n, prefix) }, prefix+"*")
}

func expectTypedMatch[T metav1.Object](t *testing.T, w watch.Interface, typ watch.EventType, match func(string) bool, want string) T {
	t.Helper()

	timeout := time.After(watchTestTimeout)

	for {
		select {
		case ev, ok := <-w.ResultChan():
			if !ok {
				var zero T

				t.Fatalf("watch closed before %s %s", typ, want)

				return zero
			}

			if ev.Type == watch.Error {
				t.Fatalf("watch ERROR event while waiting for %s %s: %v", typ, want, ev.Object)
			}

			obj, isT := ev.Object.(T)
			if !isT {
				t.Fatalf("watch event object is %T, want %T", ev.Object, *new(T))
			}

			if ev.Type == typ && match(obj.GetName()) {
				return obj
			}
		case <-timeout:
			var zero T

			t.Fatalf("timed out waiting for %s %s", typ, want)

			return zero
		}
	}
}

// TestWatchEncoding_TooOldResourceVersionExpires: after a snapshot restore the
// watch history starts at the restored RV, so a watch resuming from an older RV
// gets a single ERROR event carrying a 410 Expired Status and the stream ends,
// which makes a reflector relist.
func TestWatchEncoding_TooOldResourceVersionExpires(t *testing.T) {
	kinds := encodingKinds()

	for _, wk := range []watchKind{kinds[0], kinds[6]} { // typed Pods, registry Nodes
		t.Run(wk.name, func(t *testing.T) {
			base, api := newWatchFixtureAPI(t)

			ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
			defer cancel()

			snap, err := api.Snapshot(ctx, true)
			if err != nil {
				t.Fatalf("snapshot: %v", err)
			}

			if err := api.Restore(ctx, snap); err != nil {
				t.Fatalf("restore: %v", err)
			}

			_, br := openWatch(t, ctx, base+wk.listPath+"?watch=true&resourceVersion=1", "")

			ev := nextEvent(t, br)
			if ev.Type != "ERROR" || ev.Object["kind"] != "Status" || ev.Object["reason"] != "Expired" ||
				ev.Object["code"] != float64(http.StatusGone) {
				t.Fatalf("stale rv watch: got %s %v, want ERROR 410 Expired Status", ev.Type, ev.Object)
			}

			if msg, _ := ev.Object["message"].(string); !strings.HasPrefix(msg, "too old resource version: 1 ") {
				t.Fatalf("stale rv message: %q", msg)
			}

			if _, err := br.ReadBytes('\n'); !errors.Is(err, io.EOF) {
				t.Fatalf("stream after ERROR: got %v, want EOF", err)
			}
		})
	}
}

// TestWatchEncoding_TimeoutSecondsEndsStream: timeoutSeconds bounds the watch;
// the server ends the stream cleanly once it elapses.
func TestWatchEncoding_TimeoutSecondsEndsStream(t *testing.T) {
	kinds := encodingKinds()

	for _, wk := range []watchKind{kinds[0], kinds[6]} { // typed Pods, registry Nodes
		t.Run(wk.name, func(t *testing.T) {
			base := newWatchFixture(t)

			ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
			defer cancel()

			start := time.Now()
			_, br := openWatch(t, ctx, base+wk.listPath+"?watch=true&timeoutSeconds=1", "")

			for {
				if _, err := br.ReadBytes('\n'); err != nil {
					if !errors.Is(err, io.EOF) {
						t.Fatalf("watch ended with %v, want a clean EOF", err)
					}

					break
				}
			}

			if d := time.Since(start); d < 900*time.Millisecond || d > 3*time.Second {
				t.Fatalf("watch with timeoutSeconds=1 ended after %v", d)
			}
		})
	}
}

// TestWatchEncoding_SeededObjectRVIsWatchable: on a fresh cluster a watch from
// a bootstrap object's own resourceVersion (the client-go Get-then-Watch
// pattern) must stream, not expire. The first event is the object's update.
func TestWatchEncoding_SeededObjectRVIsWatchable(t *testing.T) {
	cases := []struct {
		name, getPath, listPath, objName string
	}{
		{"typed_namespace", "/api/v1/namespaces/kube-system", "/api/v1/namespaces", "kube-system"},
		{"registry_node", "/api/v1/nodes/cloudemu-node-0", "/api/v1/nodes", "cloudemu-node-0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := newWatchFixture(t)

			resp := do(t, http.MethodGet, base+tc.getPath, nil)

			var obj struct {
				Metadata struct {
					ResourceVersion string `json:"resourceVersion"`
				} `json:"metadata"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
				t.Fatalf("decode %s: %v", tc.objName, err)
			}

			resp.Body.Close()

			ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
			defer cancel()

			_, br := openWatch(t, ctx, base+tc.listPath+"?watch=true&fieldSelector=metadata.name%3D"+tc.objName+
				"&resourceVersion="+obj.Metadata.ResourceVersion, "")

			req, err := http.NewRequest(http.MethodPatch, base+tc.getPath,
				bytes.NewReader([]byte(`{"metadata":{"labels":{"watch-test":"yes"}}}`)))
			if err != nil {
				t.Fatalf("new patch request: %v", err)
			}

			req.Header.Set("Content-Type", "application/merge-patch+json")

			patchResp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("patch %s: %v", tc.objName, err)
			}

			patchResp.Body.Close()

			ev := nextEvent(t, br)
			if ev.Type != "MODIFIED" || ev.name() != tc.objName {
				t.Fatalf("watch from seeded RV %s: got %s %v, want MODIFIED %s",
					obj.Metadata.ResourceVersion, ev.Type, ev.Object, tc.objName)
			}
		})
	}
}
