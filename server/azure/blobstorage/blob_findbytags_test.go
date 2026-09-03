package blobstorage_test

import (
	"context"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKFindBlobsByTags drives the real azblob client through Find Blobs by
// Tags at both the account and container scope, asserting the ?where tag query
// returns exactly the blobs whose index tags match — including a multi-term AND
// query and an @container-scoped account query.
func TestSDKFindBlobsByTags(t *testing.T) {
	ctx := context.Background()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{BlobStorage: cloudP.BlobStorage})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	clientOpts := &azblob.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	}

	svcClient, err := azblob.NewClientWithNoCredential(ts.URL+"/", clientOpts)
	if err != nil {
		t.Fatalf("NewClientWithNoCredential: %v", err)
	}

	for _, c := range []string{"c1", "c2"} {
		if _, err := svcClient.CreateContainer(ctx, c, nil); err != nil {
			t.Fatalf("CreateContainer %s: %v", c, err)
		}
	}

	putTagged(t, ctx, svcClient, "c1", "a", map[string]string{"env": "prod", "team": "red"})
	putTagged(t, ctx, svcClient, "c1", "b", map[string]string{"env": "dev"})
	putTagged(t, ctx, svcClient, "c1", "notag", nil)
	putTagged(t, ctx, svcClient, "c2", "c", map[string]string{"env": "prod"})

	svc := svcClient.ServiceClient()

	// Account-scoped: env=prod spans both containers.
	assertFound(t, filterService(t, ctx, svc, `"env"='prod'`), []string{"c1/a", "c2/c"})

	// Account-scoped multi-term AND narrows to the single blob with both tags.
	assertFound(t, filterService(t, ctx, svc, `"env"='prod' AND "team"='red'`), []string{"c1/a"})

	// A blob that carries no tags never matches.
	assertFound(t, filterService(t, ctx, svc, `"env"='dev'`), []string{"c1/b"})

	// @container narrows an account query to one container.
	assertFound(t, filterService(t, ctx, svc, `@container='c2' AND "env"='prod'`), []string{"c2/c"})

	// Container-scoped Find Blobs searches only that container.
	assertFound(t, filterContainer(t, ctx, svc, "c1", `"env"='prod'`), []string{"c1/a"})
	assertFound(t, filterContainer(t, ctx, svc, "c2", `"env"='prod'`), []string{"c2/c"})

	// The matched blob carries its full tag set in the response.
	res := filterService(t, ctx, svc, `"team"='red'`)
	if len(res) != 1 {
		t.Fatalf("team=red matched %d blobs, want 1", len(res))
	}

	if got := res[0].tags["env"]; got != "prod" {
		t.Fatalf("matched blob env tag = %q, want prod", got)
	}
}

type matchedBlob struct {
	ref  string // "container/name"
	tags map[string]string
}

func putTagged(t *testing.T, ctx context.Context, c *azblob.Client, container, blob string, tags map[string]string) {
	t.Helper()

	if _, err := c.UploadBuffer(ctx, container, blob, []byte("body-"+blob), nil); err != nil {
		t.Fatalf("upload %s/%s: %v", container, blob, err)
	}

	if len(tags) == 0 {
		return
	}

	bc := c.ServiceClient().NewContainerClient(container).NewBlobClient(blob)
	if _, err := bc.SetTags(ctx, tags, nil); err != nil {
		t.Fatalf("set tags %s/%s: %v", container, blob, err)
	}
}

func filterService(t *testing.T, ctx context.Context, svc *service.Client, where string) []matchedBlob {
	t.Helper()

	resp, err := svc.FilterBlobs(ctx, where, nil)
	if err != nil {
		t.Fatalf("service FilterBlobs %q: %v", where, err)
	}

	return collectFilter(resp.Blobs)
}

func filterContainer(t *testing.T, ctx context.Context, svc *service.Client, container, where string) []matchedBlob {
	t.Helper()

	resp, err := svc.NewContainerClient(container).FilterBlobs(ctx, where, nil)
	if err != nil {
		t.Fatalf("container FilterBlobs %s %q: %v", container, where, err)
	}

	return collectFilter(resp.Blobs)
}

func collectFilter(items []*service.FilterBlobItem) []matchedBlob {
	out := make([]matchedBlob, 0, len(items))

	for _, it := range items {
		mb := matchedBlob{tags: map[string]string{}}
		if it.ContainerName != nil && it.Name != nil {
			mb.ref = *it.ContainerName + "/" + *it.Name
		}

		if it.Tags != nil {
			for _, tag := range it.Tags.BlobTagSet {
				if tag.Key != nil && tag.Value != nil {
					mb.tags[*tag.Key] = *tag.Value
				}
			}
		}

		out = append(out, mb)
	}

	return out
}

func assertFound(t *testing.T, got []matchedBlob, want []string) {
	t.Helper()

	refs := make([]string, 0, len(got))
	for _, b := range got {
		refs = append(refs, b.ref)
	}

	sort.Strings(refs)
	sort.Strings(want)

	if len(refs) != len(want) {
		t.Fatalf("matched %v, want %v", refs, want)
	}

	for i := range want {
		if refs[i] != want[i] {
			t.Fatalf("matched %v, want %v", refs, want)
		}
	}
}
