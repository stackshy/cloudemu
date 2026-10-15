package containerinstances_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestContainerPortsAndResourcesAlwaysReported guards AZCMP-N8: azurerm's
// flattenContainerGroupContainers dereferences containers[].properties.ports
// and resources.requests without a nil check, so a container with no ports
// crashed the provider. Real ARM always reports "ports" (empty when none) and
// the required resources block; a declared port must round-trip on GET.
func TestContainerPortsAndResourcesAlwaysReported(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.DriversFrom(cloud)))
	t.Cleanup(srv.Close)
	ensureRG(t, srv.URL, subID, rgName)

	tests := []struct {
		name      string
		ports     string
		wantPorts []float64
	}{
		{name: "no-ports", ports: "", wantPorts: []float64{}},
		{name: "with-ports", ports: `,"ports":[{"port":80,"protocol":"TCP"}]`, wantPorts: []float64{80}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"location":"westus2","properties":{"osType":"Linux","containers":[{"name":"c1",` +
				`"properties":{"image":"busybox:latest"` + tc.ports + `}}]}}`
			doReq(t, srv.URL, http.MethodPut, groupURL(tc.name)+apiVer, strings.NewReader(body), http.StatusCreated)

			raw := doReq(t, srv.URL, http.MethodGet, groupURL(tc.name)+apiVer, nil, http.StatusOK)

			var got struct {
				Properties struct {
					Containers []struct {
						Properties map[string]any `json:"properties"`
					} `json:"containers"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decode: %v", err)
			}

			if len(got.Properties.Containers) != 1 {
				t.Fatalf("containers = %d, want 1 (body: %s)", len(got.Properties.Containers), raw)
			}

			props := got.Properties.Containers[0].Properties

			ports, ok := props["ports"].([]any)
			if !ok {
				t.Fatalf("ports missing or not an array: %v (body: %s)", props["ports"], raw)
			}

			if len(ports) != len(tc.wantPorts) {
				t.Fatalf("ports = %v, want %v", ports, tc.wantPorts)
			}

			for i, p := range ports {
				if m, _ := p.(map[string]any); m["port"] != tc.wantPorts[i] {
					t.Errorf("ports[%d] = %v, want %v", i, p, tc.wantPorts[i])
				}
			}

			res, _ := props["resources"].(map[string]any)
			if _, ok := res["requests"].(map[string]any); !ok {
				t.Errorf("resources.requests missing: %v", props["resources"])
			}
		})
	}
}
