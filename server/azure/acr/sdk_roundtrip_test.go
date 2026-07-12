package acr_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azacr "github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newACRClient(t *testing.T) (*azacr.Client, crdriver.ContainerRegistry) {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{ACR: cloud.ACR})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := azacr.NewClient(ts.URL, fakeCred{}, &azacr.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	})
	if err != nil {
		t.Fatalf("azacr.NewClient: %v", err)
	}

	return client, cloud.ACR
}

func seedImage(t *testing.T, reg crdriver.ContainerRegistry, repo, tag string) {
	t.Helper()

	if _, err := reg.PutImage(context.Background(), &crdriver.ImageManifest{
		Repository: repo,
		Tag:        tag,
		MediaType:  "application/vnd.docker.distribution.manifest.v2+json",
		SizeBytes:  512,
	}); err != nil {
		t.Fatalf("seed PutImage(%s:%s): %v", repo, tag, err)
	}
}

func TestSDKACRCatalogAndProperties(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	// ACR has no data-plane create; repositories appear on push.
	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	seedImage(t, reg, "app", "v1")

	var names []string

	pager := client.NewListRepositoriesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListRepositories: %v", err)
		}

		for _, n := range page.Repositories.Names {
			names = append(names, *n)
		}
	}

	if !contains(names, "app") {
		t.Fatalf("catalog %v missing \"app\"", names)
	}

	props, err := client.GetRepositoryProperties(ctx, "app", nil)
	if err != nil {
		t.Fatalf("GetRepositoryProperties: %v", err)
	}

	if props.Name == nil || *props.Name != "app" {
		t.Fatalf("got repository name %v, want app", props.Name)
	}

	if props.TagCount == nil || *props.TagCount != 1 {
		t.Fatalf("got tag count %v, want 1", props.TagCount)
	}
}

func TestSDKACRListTags(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "imgs"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	seedImage(t, reg, "imgs", "v1")
	seedImage(t, reg, "imgs", "v2")

	var tags []string

	pager := client.NewListTagsPager("imgs", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListTags: %v", err)
		}

		for _, tag := range page.Tags {
			tags = append(tags, *tag.Name)
		}
	}

	if !contains(tags, "v1") || !contains(tags, "v2") {
		t.Fatalf("tags %v missing v1/v2", tags)
	}
}

func TestSDKACRDeleteRepository(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "old"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	if _, err := client.DeleteRepository(ctx, "old", nil); err != nil {
		t.Fatalf("DeleteRepository: %v", err)
	}

	_, err := client.GetRepositoryProperties(ctx, "old", nil)
	assertResponseCode(t, err, 404)
}

func TestSDKACRHierarchicalRepositoryName(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	// Registry names are commonly hierarchical (e.g. "team/app"); the bare
	// name must survive the resource-ID round-trip, not be truncated to the
	// last path segment.
	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "team/app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	var names []string

	pager := client.NewListRepositoriesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListRepositories: %v", err)
		}

		for _, n := range page.Repositories.Names {
			names = append(names, *n)
		}
	}

	if !contains(names, "team/app") {
		t.Fatalf("catalog %v missing hierarchical name \"team/app\"", names)
	}

	props, err := client.GetRepositoryProperties(ctx, "team/app", nil)
	if err != nil {
		t.Fatalf("GetRepositoryProperties(team/app): %v", err)
	}

	if props.Name == nil || *props.Name != "team/app" {
		t.Fatalf("got repository name %v, want team/app", props.Name)
	}
}

func TestSDKACRErrors(t *testing.T) {
	client, _ := newACRClient(t)

	_, err := client.GetRepositoryProperties(context.Background(), "ghost", nil)
	assertResponseCode(t, err, 404)
}

func assertResponseCode(t *testing.T, err error, want int) {
	t.Helper()

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("want *azcore.ResponseError with code %d, got %v", want, err)
	}

	if respErr.StatusCode != want {
		t.Fatalf("got HTTP %d, want %d (%v)", respErr.StatusCode, want, err)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}

	return false
}
