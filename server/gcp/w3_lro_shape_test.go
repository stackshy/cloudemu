package gcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	alloydb "cloud.google.com/go/alloydb/apiv1"
	"cloud.google.com/go/alloydb/apiv1/alloydbpb"
	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	filestore "cloud.google.com/go/filestore/apiv1"
	"cloud.google.com/go/filestore/apiv1/filestorepb"
	"google.golang.org/api/option"
)

func gapicOpts(ts *httptest.Server, prefix string) []option.ClientOption {
	return []option.ClientOption{
		option.WithEndpoint(ts.URL + prefix),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	}
}

// TestGAPICOperationWaitDecodesResponse (GADB-06, GFST-02, GAR-02): a GAPIC
// client's op.Wait() decodes the done operation's response as a
// google.protobuf.Any, so a response without "@type" (or a delete with no
// response at all) fails the wait. Each case also polls the returned operation
// name and expects the same typed response.
func TestGAPICOperationWaitDecodesResponse(t *testing.T) {
	tests := []struct {
		name     string
		wantType string
		run      func(ctx context.Context, ts *httptest.Server) (string, error)
	}{
		{
			name:     "alloydb create cluster",
			wantType: "type.googleapis.com/google.cloud.alloydb.v1.Cluster",
			run: func(ctx context.Context, ts *httptest.Server) (string, error) {
				c, err := alloydb.NewAlloyDBAdminRESTClient(ctx, gapicOpts(ts, "/alloydb.googleapis.com")...)
				if err != nil {
					return "", err
				}
				defer c.Close()

				op, err := c.CreateCluster(ctx, &alloydbpb.CreateClusterRequest{
					Parent:    "projects/demo/locations/us-central1",
					ClusterId: "ac1",
					Cluster: &alloydbpb.Cluster{NetworkConfig: &alloydbpb.Cluster_NetworkConfig{
						Network: "projects/demo/global/networks/default",
					}},
				})
				if err != nil {
					return "", err
				}

				got, err := op.Wait(ctx)
				if err == nil && !strings.HasSuffix(got.GetName(), "/clusters/ac1") {
					t.Errorf("Wait cluster name = %q", got.GetName())
				}

				return op.Name(), err
			},
		},
		{
			name:     "filestore create instance",
			wantType: "type.googleapis.com/google.cloud.filestore.v1.Instance",
			run: func(ctx context.Context, ts *httptest.Server) (string, error) {
				c, err := filestore.NewCloudFilestoreManagerRESTClient(ctx, gapicOpts(ts, "")...)
				if err != nil {
					return "", err
				}
				defer c.Close()

				op, err := c.CreateInstance(ctx, &filestorepb.CreateInstanceRequest{
					Parent:     "projects/demo/locations/us-central1-a",
					InstanceId: "fs1",
					Instance: &filestorepb.Instance{
						Tier:       filestorepb.Instance_BASIC_HDD,
						FileShares: []*filestorepb.FileShareConfig{{Name: "vol1", CapacityGb: 1024}},
						Networks:   []*filestorepb.NetworkConfig{{Network: "default"}},
					},
				})
				if err != nil {
					return "", err
				}

				got, err := op.Wait(ctx)
				if err == nil && !strings.HasSuffix(got.GetName(), "/instances/fs1") {
					t.Errorf("Wait instance name = %q", got.GetName())
				}

				return op.Name(), err
			},
		},
		{
			name:     "artifact registry delete repository",
			wantType: "type.googleapis.com/google.protobuf.Empty",
			run: func(ctx context.Context, ts *httptest.Server) (string, error) {
				c, err := artifactregistry.NewRESTClient(ctx, gapicOpts(ts, "")...)
				if err != nil {
					return "", err
				}
				defer c.Close()

				create, err := c.CreateRepository(ctx, &artifactregistrypb.CreateRepositoryRequest{
					Parent:       "projects/demo/locations/us",
					RepositoryId: "r1",
					Repository:   &artifactregistrypb.Repository{Format: artifactregistrypb.Repository_DOCKER},
				})
				if err != nil {
					return "", err
				}

				op, err := c.DeleteRepository(ctx, &artifactregistrypb.DeleteRepositoryRequest{
					Name: "projects/demo/locations/us/repositories/r1",
				})
				if err != nil {
					return "", err
				}

				// GLRO-05: the delete must not reuse (and overwrite) the create's name.
				if op.Name() == create.Name() {
					t.Errorf("create and delete share operation name %q", op.Name())
				}

				if code, body := do(t, ts, http.MethodGet, "/v1/"+create.Name(), ""); code != http.StatusOK ||
					!strings.Contains(body, "artifactregistry.v1.Repository") {
					t.Errorf("create op after delete: code=%d body=%.300s", code, body)
				}

				return op.Name(), op.Wait(ctx)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := fullServer(t)

			name, err := tc.run(context.Background(), ts)
			if err != nil {
				t.Fatalf("op.Wait: %v", err)
			}

			if !strings.Contains(name, "/operations/operation-") {
				t.Errorf("operation name %q, want an operation-<ms>-<hex> id", name)
			}

			code, body := do(t, ts, http.MethodGet, "/v1/"+name, "")
			if code != http.StatusOK {
				t.Fatalf("poll %s: code=%d body=%.300s", name, code, body)
			}

			var op struct {
				Response map[string]any `json:"response"`
				Metadata map[string]any `json:"metadata"`
			}
			if uErr := json.Unmarshal([]byte(body), &op); uErr != nil {
				t.Fatalf("decode poll: %v", uErr)
			}

			if op.Response["@type"] != tc.wantType {
				t.Errorf("poll response @type = %v, want %s", op.Response["@type"], tc.wantType)
			}

			if op.Metadata["@type"] == nil {
				t.Errorf("poll metadata has no @type: %.300s", body)
			}
		})
	}
}

// TestUnknownDriverOperationIsNotFound (T5-04/05): an operation name that was
// never created is 404 NOT_FOUND on the services whose driver mocks used to
// fabricate a done operation for it.
func TestUnknownDriverOperationIsNotFound(t *testing.T) {
	ts := fullServer(t)

	tests := []struct {
		name string
		path string
	}{
		{"bigtable", "/v2/operations/bigtable-create-instance-999"},
		{"spanner", "/v1/projects/demo/instances/i1/operations/nope"},
		{"dataproc", "/v1/projects/demo/regions/us-central1/operations/nope"},
		{"api gateway v1beta", "/v1beta/projects/demo/locations/global/operations/nope"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, body := do(t, ts, http.MethodGet, tc.path, "")
			if code != http.StatusNotFound || !strings.Contains(body, "NOT_FOUND") {
				t.Fatalf("GET %s: code=%d body=%.300s, want 404 NOT_FOUND", tc.path, code, body)
			}
		})
	}
}

// TestKnownDriverOperationReplaysTypedResponse: a poll of a real operation
// returns 200 with its typed response (Bigtable used to send none, Spanner had
// no "@type", API Gateway v1beta dropped it).
func TestKnownDriverOperationReplaysTypedResponse(t *testing.T) {
	ts := fullServer(t)

	tests := []struct {
		name     string
		create   string
		body     string
		pollBase string
		wantType string
	}{
		{
			name:     "bigtable",
			create:   "/v2/projects/demo/instances",
			body:     `{"instanceId":"bt1","instance":{"displayName":"bt1"},"clusters":{"c1":{"location":"projects/demo/locations/us-central1-b","serveNodes":1}}}`,
			pollBase: "/v2/",
			wantType: "type.googleapis.com/google.bigtable.admin.v2.Instance",
		},
		{
			name:     "spanner",
			create:   "/v1/projects/demo/instances",
			body:     `{"instanceId":"sp1","instance":{"config":"projects/demo/instanceConfigs/regional-us-central1","displayName":"sp1","nodeCount":1}}`,
			pollBase: "/v1/",
			wantType: "type.googleapis.com/google.spanner.admin.instance.v1.Instance",
		},
		{
			name:     "api gateway v1beta",
			create:   "/v1beta/projects/demo/locations/global/apis?apiId=a1",
			body:     `{"displayName":"a1"}`,
			pollBase: "/v1beta/",
			wantType: "type.googleapis.com/google.cloud.apigateway.v1beta.Api",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, body := do(t, ts, http.MethodPost, tc.create, tc.body)
			if code != http.StatusOK {
				t.Fatalf("create: code=%d body=%.300s", code, body)
			}

			var created struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(body), &created); err != nil || created.Name == "" {
				t.Fatalf("create op: %v body=%.300s", err, body)
			}

			code, body = do(t, ts, http.MethodGet, tc.pollBase+created.Name, "")
			if code != http.StatusOK || !strings.Contains(body, `"@type":"`+tc.wantType+`"`) {
				t.Fatalf("poll %s: code=%d body=%.400s, want response @type %s", created.Name, code, body, tc.wantType)
			}
		})
	}
}
