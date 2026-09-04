package azure

import (
	"context"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzureSecretsCompat drives a Key Vault secret lifecycle through the real
// azure-sdk-for-go azsecrets client. Key Vault secrets map onto the portable
// "secrets" driver, so operation names match SecretsManager's in
// docs/coverage/coverage.json. Key Vault's data plane is a bearer-token API, so
// the client runs over the harness's TLS server with a fake credential and
// challenge-resource verification disabled (the emulated vault host is
// 127.0.0.1, not *.vault.azure.net).
func TestAzureSecretsCompat(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{KeyVault: provider.KeyVault})

	// On a bare host the vault is named by a leading path segment (/{vault}); the
	// emulator no longer collapses an unnamed host to a single "default" vault.
	client, err := azsecrets.NewClient(sess.Endpoint()+"/default", compat.FakeAzureCred(), &azsecrets.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
		DisableChallengeResourceVerification: true,
	})
	if err != nil {
		t.Fatalf("azsecrets client: %v", err)
	}

	ctx := context.Background()

	const (
		svc     = "secrets"
		name    = "db-password"
		valueV1 = "hunter2"
		valueV2 = "hunter3"

		wantVersions = 2
	)

	var v1Version string

	// First SetSecret on a new name creates the secret (driver CreateSecret).
	sess.Op(svc, "CreateSecret", func() error {
		set, err := client.SetSecret(ctx, name, azsecrets.SetSecretParameters{
			Value: to.Ptr(valueV1),
			Tags:  map[string]*string{"env": to.Ptr("test")},
		}, nil)
		if err != nil {
			return err
		}

		if set.ID == nil || set.ID.Name() != name {
			return fmt.Errorf("SetSecret id = %v, want name %q", set.ID, name)
		}

		v1Version = set.ID.Version()

		return nil
	})

	// A second SetSecret on the same name adds a version (driver PutSecretValue).
	sess.Op(svc, "PutSecretValue", func() error {
		set, err := client.SetSecret(ctx, name, azsecrets.SetSecretParameters{Value: to.Ptr(valueV2)}, nil)
		if err != nil {
			return err
		}

		if set.ID.Version() == v1Version {
			return fmt.Errorf("PutSecretValue reused the first version id %q", v1Version)
		}

		return nil
	})

	// GetSecret with no version returns the current bundle (driver GetSecret).
	sess.Op(svc, "GetSecret", func() error {
		got, err := client.GetSecret(ctx, name, "", nil)
		if err != nil {
			return err
		}

		if got.Value == nil || *got.Value != valueV2 {
			return fmt.Errorf("GetSecret value = %v, want %q", got.Value, valueV2)
		}

		return nil
	})

	// GetSecret pinned to a version reads that version (driver GetSecretValue).
	sess.Op(svc, "GetSecretValue", func() error {
		got, err := client.GetSecret(ctx, name, v1Version, nil)
		if err != nil {
			return err
		}

		if got.Value == nil || *got.Value != valueV1 {
			return fmt.Errorf("GetSecretValue(v1) = %v, want %q", got.Value, valueV1)
		}

		return nil
	})

	sess.Op(svc, "ListSecretVersions", func() error {
		var versions []string

		pager := client.NewListSecretPropertiesVersionsPager(name, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return err
			}

			for _, item := range page.Value {
				versions = append(versions, item.ID.Version())
			}
		}

		if len(versions) != wantVersions {
			return fmt.Errorf("got %d versions %v, want %d", len(versions), versions, wantVersions)
		}

		return nil
	})

	sess.Op(svc, "ListSecrets", func() error {
		var names []string

		pager := client.NewListSecretPropertiesPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return err
			}

			for _, item := range page.Value {
				names = append(names, item.ID.Name())
			}
		}

		if len(names) != 1 || names[0] != name {
			return fmt.Errorf("ListSecrets = %v, want [%s]", names, name)
		}

		return nil
	})

	sess.Op(svc, "DeleteSecret", func() error {
		deleted, err := client.DeleteSecret(ctx, name, nil)
		if err != nil {
			return err
		}

		if deleted.ID == nil || deleted.ID.Name() != name {
			return fmt.Errorf("DeleteSecret id = %v, want name %q", deleted.ID, name)
		}

		return nil
	})
}
