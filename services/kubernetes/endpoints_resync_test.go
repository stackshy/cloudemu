package kubernetes_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// resyncEndpointsForNamespaceLocked runs for every Service on every Pod change
// in the namespace. A Service whose endpoint set does not change must not have
// its ResourceVersion bumped or emit a MODIFIED event — otherwise an informer
// on Endpoints sees a spurious event for every Service on unrelated Pod churn.
func TestEndpoints_NoSpuriousBumpOnUnrelatedPodChange(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	ns := do(t, http.MethodPost, base+"/api/v1/namespaces",
		[]byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"default"}}`))
	ns.Body.Close()

	// A Service whose selector matches nothing → Endpoints stays empty.
	svc := do(t, http.MethodPost, base+"/api/v1/namespaces/default/services",
		[]byte(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"quiet"},`+
			`"spec":{"selector":{"app":"absent"},"ports":[{"port":80}]}}`))
	svc.Body.Close()

	rv0 := endpointsRV(t, base, "quiet")

	// An unrelated Pod (no matching labels) triggers a namespace-wide resync.
	pod := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods",
		[]byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"other","labels":{"app":"other"}},`+
			`"spec":{"containers":[{"name":"c","image":"nginx"}]}}`))
	pod.Body.Close()

	if rv1 := endpointsRV(t, base, "quiet"); rv1 != rv0 {
		t.Fatalf("endpoints ResourceVersion changed on unrelated pod: %q -> %q (spurious bump)", rv0, rv1)
	}
}

func endpointsRV(t *testing.T, base, name string) string {
	t.Helper()

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/endpoints/"+name, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get endpoints %s: status %d", name, resp.StatusCode)
	}

	var ep struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ep); err != nil {
		t.Fatalf("decode endpoints: %v", err)
	}

	return ep.Metadata.ResourceVersion
}
