package cloudrun_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpprovider "github.com/stackshy/cloudemu/v2/providers/gcp"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	cloudrunsrv "github.com/stackshy/cloudemu/v2/server/gcp/cloudrun"
)

// TestOperationPollReplaysOrIs404 pins GRUN-01: polling a create operation
// returns the stored operation with its typed Service response, and a name
// that was never minted is 404, both for the standalone handler and the full
// server (where the operation lives in the shared registry).
func TestOperationPollReplaysOrIs404(t *testing.T) {
	tests := []struct {
		name    string
		handler func(*gcpprovider.Provider) http.Handler
	}{
		{name: "standalone handler", handler: func(c *gcpprovider.Provider) http.Handler {
			return cloudrunsrv.New(c.CloudRun)
		}},
		{name: "full server", handler: func(c *gcpprovider.Provider) http.Handler {
			return gcpserver.NewFromProvider(c)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(tt.handler(cloudemu.NewGCP(config.WithContainerEngine(nil))))
			t.Cleanup(ts.Close)

			svc, err := run.NewService(context.Background(), option.WithEndpoint(ts.URL), option.WithoutAuthentication())
			if err != nil {
				t.Fatalf("run.NewService: %v", err)
			}

			ctx := context.Background()

			op, err := svc.Projects.Locations.Services.Create(parent, sdkService()).ServiceId("polled").Context(ctx).Do()
			if err != nil {
				t.Fatalf("Services.Create: %v", err)
			}

			got, err := svc.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
			if err != nil {
				t.Fatalf("Operations.Get(%s): %v", op.Name, err)
			}

			var resp struct {
				Type string `json:"@type"`
				Name string `json:"name"`
			}

			if !got.Done || json.Unmarshal(got.Response, &resp) != nil ||
				resp.Type != "type.googleapis.com/google.cloud.run.v2.Service" || resp.Name != parent+"/services/polled" {
				t.Fatalf("poll = done:%v response:%s, want the typed Service", got.Done, got.Response)
			}

			_, err = svc.Projects.Locations.Operations.Get(parent + "/operations/nope").Context(ctx).Do()

			var apiErr *googleapi.Error
			if !errors.As(err, &apiErr) || apiErr.Code != http.StatusNotFound {
				t.Fatalf("unknown op: err=%v, want 404", err)
			}
		})
	}
}
