package servicenetworking_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestOperationPollInFullServer pins T5-07: with Cloud Functions mounted
// (which also serves /v1/operations/{op}), polling a connection's operation
// reaches Service Networking and replays the typed Connection, and an
// operation it never minted is 404.
func TestOperationPollInFullServer(t *testing.T) {
	ts := httptest.NewServer(gcpserver.NewFromProvider(cloudemu.NewGCP()))
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/v1/services/servicenetworking.googleapis.com/connections",
		"application/json", strings.NewReader(`{"network":"projects/p/global/networks/n1","reservedPeeringRanges":["r1"]}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var op struct {
		Name string `json:"name"`
	}

	err = json.NewDecoder(resp.Body).Decode(&op)
	resp.Body.Close()

	if err != nil || !strings.HasPrefix(op.Name, "operations/sn-") {
		t.Fatalf("create op = %q (%v), want operations/sn-{id}", op.Name, err)
	}

	name := op.Name

	tests := []struct {
		name     string
		op       string
		wantCode int
		wantBody string
	}{
		{name: "minted op replays the connection", op: name, wantCode: http.StatusOK,
			wantBody: `"@type":"type.googleapis.com/google.cloud.servicenetworking.v1.Connection"`},
		{name: "unknown op is 404", op: "operations/sn-bogus", wantCode: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(ts.URL + "/v1/" + tt.op)
			if err != nil {
				t.Fatalf("poll: %v", err)
			}

			defer resp.Body.Close()

			raw, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.wantCode || !strings.Contains(string(raw), tt.wantBody) {
				t.Fatalf("poll %s = %d %s, want %d containing %s", tt.op, resp.StatusCode, raw, tt.wantCode, tt.wantBody)
			}
		})
	}
}
