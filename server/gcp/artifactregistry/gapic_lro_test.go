package artifactregistry_test

import (
	"context"
	"net/http/httptest"
	"testing"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestGAPICCreateRepositoryWait is the review's #3 check: the finding targeted
// the GAPIC apiv1 client's LRO .Wait(), which the raw google.golang.org/api
// REST client never exercised. This drives the real apiv1 REST client end to
// end — CreateRepository(...).Wait() must resolve (not 404, and not a decode
// error from a missing response @type) and return the created repository.
func TestGAPICCreateRepositoryWait(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.DriversFrom(cloud)) // full server: exercises real dispatch
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	client, err := artifactregistry.NewRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	op, err := client.CreateRepository(ctx, &artifactregistrypb.CreateRepositoryRequest{
		Parent:       "projects/demo/locations/us",
		RepositoryId: "gapic-repo",
		Repository:   &artifactregistrypb.Repository{Description: "gapic"},
	})
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	repo, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("op.Wait (the #3 GAPIC LRO fix): %v", err)
	}

	if repo == nil || repo.GetName() == "" {
		t.Fatalf("Wait returned no repository: %+v", repo)
	}
}
