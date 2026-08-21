package azure

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azacr "github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

const crService = "containerregistry"

// noRetries disables SDK retries so the emulator's first response is asserted.
const noRetries = -1

// TestAzureContainerRegistryCompat drives the real azcontainerregistry SDK
// (ACR data-plane catalog API) against CloudEmu's in-process wire server and
// records one compat result per portable containerregistry op the SDK exercises.
//
// ACR's data plane is list/get/delete oriented — repositories appear on push,
// so there is no data-plane create. Repositories and images are seeded through
// the driver directly (as the SDK offers no data-plane push), then the SDK
// lists repositories, reads repository properties, lists tags, and deletes.
func TestAzureContainerRegistryCompat(t *testing.T) {
	cloud := cloudemu.NewAzure()

	// ACR bearer tokens are refused over plaintext, so boot over TLS and pair
	// with a fake credential (the emulator does not verify tokens).
	sess := compat.BootAzureTLS(t, azureserver.Drivers{ACR: cloud.ACR})

	client, err := azacr.NewClient(sess.Endpoint(), compat.FakeAzureCred(), &azacr.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: noRetries},
		},
	})
	if err != nil {
		t.Fatalf("azacr.NewClient: %v", err)
	}

	ctx := context.Background()
	reg := cloud.ACR

	// Seed a repository with two tags via the driver — the data plane has no
	// push surface, so this stands in for a `docker push`.
	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	seedImage(t, reg, "app", "v1")
	seedImage(t, reg, "app", "v2")

	sess.Op(crService, "ListRepositories", func() error {
		pager := client.NewListRepositoriesPager(nil)
		for pager.More() {
			if _, perr := pager.NextPage(ctx); perr != nil {
				return perr
			}
		}

		return nil
	})

	sess.Op(crService, "GetRepository", func() error {
		_, gerr := client.GetRepositoryProperties(ctx, "app", nil)

		return gerr
	})

	sess.Op(crService, "ListImages", func() error {
		pager := client.NewListTagsPager("app", nil)
		for pager.More() {
			if _, perr := pager.NextPage(ctx); perr != nil {
				return perr
			}
		}

		return nil
	})

	sess.Op(crService, "DeleteRepository", func() error {
		_, derr := client.DeleteRepository(ctx, "app", nil)

		return derr
	})
}

// seedImage pushes one image manifest through the driver so the ACR data-plane
// catalog and tag lists have something to return.
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
