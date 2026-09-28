package managedkafka

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	mkprovider "github.com/stackshy/cloudemu/v2/providers/gcp/managedkafka"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
)

const (
	wireSubnet  = `"gcpConfig":{"accessConfig":{"networkConfigs":[{"subnet":"projects/p/regions/us-central1/subnetworks/s"}]}}`
	wireCluster = `{"capacityConfig":{"vcpuCount":"3","memoryBytes":"3221225472"},` + wireSubnet
)

func standaloneServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts := httptest.NewServer(New(mkprovider.New(config.NewOptions(config.WithProjectID("p")))))
	t.Cleanup(ts.Close)

	return ts
}

func call(t *testing.T, ts *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)

	return resp.StatusCode, out
}

func dig(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}

		cur = mm[k]
	}

	return cur
}

// TestWireNumericEnumsAndOptionalFields: proto3 JSON allows an enum as its
// number; rebalanceConfig.mode 2 is AUTO_REBALANCE_ON_SCALE_UP, and an echoed
// output-only numeric state is accepted and ignored. The optional tlsConfig,
// updateOptions, brokerCapacityConfig and kafkaVersion round-trip; kafkaVersion
// defaults to 3.7.x.
func TestWireNumericEnumsAndOptionalFields(t *testing.T) {
	ts := standaloneServer(t)

	code, op := call(t, ts, http.MethodPost, loc+"/clusters?clusterId=n1",
		wireCluster+`,"rebalanceConfig":{"mode":2},"state":2}`)
	if code != http.StatusOK || dig(op, "response", "rebalanceConfig", "mode") != "AUTO_REBALANCE_ON_SCALE_UP" ||
		dig(op, "response", "kafkaVersion") != "3.7.x" || dig(op, "response", "state") != "ACTIVE" {
		t.Fatalf("create with numeric mode: %d %v", code, op)
	}

	if dig(op, "metadata", "@type") != opMetaTypeURL || dig(op, "metadata", "verb") != "create" ||
		dig(op, "metadata", "target") != "projects/p/locations/us-central1/clusters/n1" {
		t.Fatalf("operation metadata: %v", op["metadata"])
	}

	full := wireCluster + `,"kafkaVersion":"4.3.x","rebalanceConfig":{"mode":"NO_REBALANCE"},` +
		`"tlsConfig":{"sslPrincipalMappingRules":"DEFAULT","trustConfig":{"casConfigs":[{"caPool":"projects/x/locations/y/caPools/z"}]}},` +
		`"updateOptions":{"allowBrokerDownscaleOnClusterUpscale":true},"brokerCapacityConfig":{"diskSizeGib":"150"}}`

	if code, op = call(t, ts, http.MethodPost, loc+"/clusters?clusterId=n2", full); code != http.StatusOK {
		t.Fatalf("create full: %d %v", code, op)
	}

	code, got := call(t, ts, http.MethodGet, loc+"/clusters/n2", "")
	if code != http.StatusOK || got["kafkaVersion"] != "4.3.x" ||
		dig(got, "tlsConfig", "sslPrincipalMappingRules") != "DEFAULT" ||
		dig(got, "updateOptions", "allowBrokerDownscaleOnClusterUpscale") != true ||
		dig(got, "brokerCapacityConfig", "diskSizeGib") != "150" || dig(got, "rebalanceConfig", "mode") != "NO_REBALANCE" {
		t.Fatalf("get full: %d %v", code, got)
	}

	cas, _ := dig(got, "tlsConfig", "trustConfig", "casConfigs").([]any)
	if len(cas) != 1 || dig(cas[0].(map[string]any), "caPool") != "projects/x/locations/y/caPools/z" {
		t.Fatalf("casConfigs: %v", cas)
	}

	for name, body := range map[string]string{
		"out of range mode": wireCluster + `,"rebalanceConfig":{"mode":7}}`,
		"negative mode":     wireCluster + `,"rebalanceConfig":{"mode":-1}}`,
		"bool mode":         wireCluster + `,"rebalanceConfig":{"mode":true}}`,
		"bad state":         wireCluster + `,"state":99}`,
		"bad int64":         `{"capacityConfig":{"vcpuCount":"x"}}`,
	} {
		if code, out := call(t, ts, http.MethodPost, loc+"/clusters?clusterId=bad", body); code != http.StatusBadRequest {
			t.Fatalf("%s: %d %v, want 400", name, code, out)
		}
	}

	// null and 0 (the unspecified value) both mean unset: the default applies.
	for id, mode := range map[string]string{"z0": "0", "zn": "null"} {
		code, op := call(t, ts, http.MethodPost, loc+"/clusters?clusterId="+id,
			wireCluster+`,"rebalanceConfig":{"mode":`+mode+`},"state":null}`)
		if code != http.StatusOK || dig(op, "response", "rebalanceConfig", "mode") != "NO_REBALANCE" {
			t.Fatalf("mode %s: %d %v", mode, code, op)
		}
	}

	// A numeric-enum PATCH changes the mode.
	code, op = call(t, ts, http.MethodPatch, loc+"/clusters/n1?updateMask=rebalanceConfig.mode",
		`{"rebalanceConfig":{"mode":1},"state":"ACTIVE"}`)
	if code != http.StatusOK || dig(op, "response", "rebalanceConfig", "mode") != "NO_REBALANCE" {
		t.Fatalf("patch numeric mode: %d %v", code, op)
	}
}

// TestStandaloneOperationPoll: a standalone package server resolves its own
// operation polls with the typed response and metadata, and 404s a name it
// never issued (it used to answer done with no response for any name).
func TestStandaloneOperationPoll(t *testing.T) {
	ts := standaloneServer(t)

	code, op := call(t, ts, http.MethodPost, loc+"/clusters?clusterId=c1", wireCluster+`}`)
	if code != http.StatusOK {
		t.Fatalf("create: %d %v", code, op)
	}

	name, _ := op["name"].(string)

	code, polled := call(t, ts, http.MethodGet, "/v1/"+name, "")
	if code != http.StatusOK || polled["done"] != true || dig(polled, "response", "@type") != clusterTypeURL ||
		dig(polled, "response", "name") != "projects/p/locations/us-central1/clusters/c1" ||
		dig(polled, "metadata", "@type") != opMetaTypeURL {
		t.Fatalf("poll: %d %v", code, polled)
	}

	if code, out := call(t, ts, http.MethodGet, loc+"/operations/never-issued", ""); code != http.StatusNotFound {
		t.Fatalf("unknown op: %d %v, want 404", code, out)
	}

	code, del := call(t, ts, http.MethodDelete, loc+"/clusters/c1", "")
	if code != http.StatusOK || dig(del, "response", "@type") != emptyTypeURL || dig(del, "metadata", "verb") != "delete" {
		t.Fatalf("delete: %d %v", code, del)
	}

	if code, _ := call(t, ts, http.MethodGet, loc+"/operations", ""); code != http.StatusNotFound {
		t.Fatalf("operations collection: %d, want 404", code)
	}
}

// TestWireRoutingErrors covers the not-found, method and malformed-body paths.
func TestWireRoutingErrors(t *testing.T) {
	ts := standaloneServer(t)

	if code, _ := call(t, ts, http.MethodPost, loc+"/clusters?clusterId=c1", wireCluster+`}`); code != http.StatusOK {
		t.Fatalf("seed: %d", code)
	}

	if code, _ := call(t, ts, http.MethodPost, loc+"/clusters/c1/topics?topicId=t1",
		`{"partitionCount":1,"replicationFactor":3}`); code != http.StatusOK {
		t.Fatalf("seed topic: %d", code)
	}

	cases := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPut, loc + "/clusters", "", http.StatusMethodNotAllowed},
		{http.MethodPut, loc + "/clusters/c1", "", http.StatusMethodNotAllowed},
		{http.MethodPut, loc + "/clusters/c1/topics", "", http.StatusMethodNotAllowed},
		{http.MethodPut, loc + "/clusters/c1/topics/t1", "", http.StatusMethodNotAllowed},
		{http.MethodGet, loc + "/clusters/ghost", "", http.StatusNotFound},
		{http.MethodDelete, loc + "/clusters/ghost", "", http.StatusNotFound},
		{http.MethodPatch, loc + "/clusters/ghost?updateMask=labels", `{"labels":{}}`, http.StatusNotFound},
		{http.MethodPatch, loc + "/clusters/c1?updateMask=labels", `{`, http.StatusBadRequest},
		{http.MethodPost, loc + "/clusters?clusterId=c2", `{`, http.StatusBadRequest},
		{http.MethodPost, loc + "/clusters?clusterId=c2", "", http.StatusBadRequest},
		{http.MethodPost, loc + "/clusters?clusterId=c1", wireCluster + `}`, http.StatusConflict},
		{http.MethodGet, loc + "/clusters?pageToken=garbage", "", http.StatusBadRequest},
		{http.MethodGet, loc + "/clusters/c1/topics?pageToken=garbage", "", http.StatusBadRequest},
		{http.MethodGet, loc + "/clusters/ghost/topics", "", http.StatusNotFound},
		{http.MethodGet, loc + "/clusters/c1/topics/ghost", "", http.StatusNotFound},
		{http.MethodDelete, loc + "/clusters/c1/topics/ghost", "", http.StatusNotFound},
		{http.MethodPost, loc + "/clusters/c1/topics?topicId=t2", `{`, http.StatusBadRequest},
		{http.MethodPost, loc + "/clusters/c1/topics?topicId=t2", `{"partitionCount":0}`, http.StatusBadRequest},
		{http.MethodPatch, loc + "/clusters/c1/topics/t1?updateMask=partitionCount", `{`, http.StatusBadRequest},
		{http.MethodPatch, loc + "/clusters/c1/topics/t1?updateMask=replicationFactor", `{}`, http.StatusBadRequest},
		{http.MethodGet, loc + "/clusters/c1/topics/t1", "", http.StatusOK},
		{http.MethodDelete, loc + "/clusters/c1/topics/t1", "", http.StatusOK},
	}

	for _, tc := range cases {
		if code, out := call(t, ts, tc.method, tc.path, tc.body); code != tc.want {
			t.Fatalf("%s %s: %d %v, want %d", tc.method, tc.path, code, out, tc.want)
		}
	}

	h := New(mkprovider.New(config.NewOptions()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request(http.MethodGet, "/v1/projects/p/regions/r/clusters", ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unparseable path: %d, want 404", rec.Code)
	}
}

// fakeSibling is a ClusterSibling owning a fixed set of cluster ids.
type fakeSibling struct{ ids map[string]bool }

func (f fakeSibling) HasClusters(context.Context, string, string) bool { return len(f.ids) > 0 }

func (f fakeSibling) OwnsCluster(_ context.Context, _, _, id string) bool { return f.ids[id] }

// TestMatchesWithSibling covers ownership routing against a wired sibling.
func TestMatchesWithSibling(t *testing.T) {
	h := sharedHandler(t)
	h.SetClusterSibling(fakeSibling{ids: map[string]bool{"gke": true}})

	cases := []struct {
		name, method, path, body string
		want                     bool
	}{
		{"list yields when sibling owns clusters", http.MethodGet, loc + "/clusters", "", false},
		{"owned item", http.MethodGet, loc + "/clusters/owned", "", true},
		{"sibling item", http.MethodPatch, loc + "/clusters/gke", `{"labels":{}}`, false},
		{"missing item GET falls through", http.MethodGet, loc + "/clusters/ghost", "", false},
		{"missing item kafka PATCH", http.MethodPatch, loc + "/clusters/ghost", `{"capacityConfig":{}}`, true},
		{"missing item labels PATCH", http.MethodPatch, loc + "/clusters/ghost", `{"labels":{"a":"b"},"name":"x"}`, true},
		{"missing item alloydb PATCH", http.MethodPatch, loc + "/clusters/ghost", `{"labels":{},"displayName":"d"}`, false},
		{"missing item allowMissing PATCH", http.MethodPatch, loc + "/clusters/ghost?allowMissing=true", `{"labels":{}}`, false},
		{"missing item non-object PATCH", http.MethodPatch, loc + "/clusters/ghost", `[]`, false},
		{"gke create over kafka id", http.MethodPost, loc + "/clusters", `{"cluster":{"name":"owned"}}`, true},
		{"alloydb create over kafka id", http.MethodPost, loc + "/clusters?clusterId=owned", `{"network":"n"}`, true},
		{"gke create fresh id", http.MethodPost, loc + "/clusters", `{"cluster":{"name":"fresh"}}`, false},
		{"gke create no body", http.MethodPost, loc + "/clusters", "", false},
		{"delete collection", http.MethodDelete, loc + "/clusters", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := request(tc.method, tc.path, tc.body)
			if tc.body == "" {
				r.Body = nil
			}

			if got := h.Matches(r); got != tc.want {
				t.Fatalf("Matches(%s %s %s) = %v, want %v", tc.method, tc.path, tc.body, got, tc.want)
			}
		})
	}
}

// TestSharedCreateRefusesTakenIDs: in an assembled server a create naming an id
// the other service already uses on the shared path is 409 ALREADY_EXISTS.
func TestSharedCreateRefusesTakenIDs(t *testing.T) {
	h := sharedHandler(t)
	h.SetClusterSibling(fakeSibling{ids: map[string]bool{"gke": true}})

	for name, tc := range map[string]struct{ path, body string }{
		"kafka over sibling id": {loc + "/clusters?clusterId=gke", wireCluster + `}`},
		"sibling over kafka id": {loc + "/clusters", `{"cluster":{"name":"owned"}}`},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, request(http.MethodPost, tc.path, tc.body))

		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "ALREADY_EXISTS") {
			t.Fatalf("%s: %d %s, want 409 ALREADY_EXISTS", name, rec.Code, rec.Body.String())
		}
	}

	// A fresh id with a wired shared registry registers its op there.
	reg := lro.NewRegistry()
	h.SetOperationRegistry(reg)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request(http.MethodPost, loc+"/clusters?clusterId=fresh", wireCluster+`}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("fresh create: %d %s", rec.Code, rec.Body.String())
	}

	// Operation paths are the shared poller's, never this handler's.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, request(http.MethodGet, loc+"/operations/x", ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("operations on shared handler: %d", rec.Code)
	}
}
