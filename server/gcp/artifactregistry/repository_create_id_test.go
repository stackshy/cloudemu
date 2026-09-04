package artifactregistry_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	ar "google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/option"
)

// TestCreateRepositoryIDParamAliases guards the HIGH finding: the Terraform
// google provider and gcloud send the create-repository id as the proto
// snake_case query parameter `repository_id`, whereas the
// google.golang.org/api client sends the JSON camelCase `repositoryId`. Real
// Artifact Registry accepts either; the handler previously read only
// `repositoryId`, so every Terraform-driven create 400'd with
// "repository name is required". Both aliases must now create the repository.
func TestCreateRepositoryIDParamAliases(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{ArtifactRegistry: cloud.ArtifactRegistry})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := ar.NewService(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	for _, param := range []string{"repositoryId", "repository_id"} {
		id := "tf-" + strings.ReplaceAll(param, "_", "")

		url := ts.URL + "/v1/" + testParent + "/repositories?" + param + "=" + id
		body := strings.NewReader(`{"format":"DOCKER"}`)

		resp, err := http.Post(url, "application/json", body)
		if err != nil {
			t.Fatalf("[%s] POST: %v", param, err)
		}

		var op struct {
			Done     bool `json:"done"`
			Response struct {
				Name string `json:"name"`
			} `json:"response"`
		}

		if derr := json.NewDecoder(resp.Body).Decode(&op); derr != nil {
			t.Fatalf("[%s] decode: %v", param, derr)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("[%s] status=%d want 200", param, resp.StatusCode)
		}

		want := testParent + "/repositories/" + id
		if op.Response.Name != want {
			t.Fatalf("[%s] response name=%q want %q", param, op.Response.Name, want)
		}

		got, gerr := svc.Projects.Locations.Repositories.Get(want).Do()
		if gerr != nil {
			t.Fatalf("[%s] Get after create: %v", param, gerr)
		}

		if got.Format != "DOCKER" {
			t.Fatalf("[%s] format=%q want DOCKER", param, got.Format)
		}
	}
}
