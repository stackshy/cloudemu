package keyvault_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newSecretsClient(t *testing.T) *azsecrets.Client {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{KeyVault: cloud.KeyVault})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := azsecrets.NewClient(ts.URL, fakeCred{}, &azsecrets.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
		// The emulated vault's host is 127.0.0.1, not *.vault.azure.net.
		DisableChallengeResourceVerification: true,
	})
	if err != nil {
		t.Fatalf("azsecrets.NewClient: %v", err)
	}

	return client
}

func TestSDKKeyVaultSecretLifecycle(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	set, err := client.SetSecret(ctx, "db-password", azsecrets.SetSecretParameters{
		Value: to.Ptr("hunter2"),
		Tags:  map[string]*string{"env": to.Ptr("test")},
	}, nil)
	if err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	if set.ID == nil || set.ID.Name() != "db-password" {
		t.Fatalf("SetSecret id = %v, want name db-password", set.ID)
	}

	got, err := client.GetSecret(ctx, "db-password", "", nil)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}

	if got.Value == nil || *got.Value != "hunter2" {
		t.Fatalf("got value %v, want hunter2", got.Value)
	}

	if got.Attributes == nil || got.Attributes.Enabled == nil || !*got.Attributes.Enabled {
		t.Fatalf("got attributes %+v, want enabled", got.Attributes)
	}

	var names []string

	pager := client.NewListSecretPropertiesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListSecretProperties: %v", err)
		}

		for _, item := range page.Value {
			names = append(names, item.ID.Name())
		}
	}

	if len(names) != 1 || names[0] != "db-password" {
		t.Fatalf("list = %v, want [db-password]", names)
	}

	deleted, err := client.DeleteSecret(ctx, "db-password", nil)
	if err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	if deleted.ID == nil || deleted.ID.Name() != "db-password" {
		t.Fatalf("DeleteSecret id = %v", deleted.ID)
	}

	if _, err := client.GetSecret(ctx, "db-password", "", nil); err == nil {
		t.Fatal("GetSecret after delete: want error, got nil")
	}
}

func TestSDKKeyVaultSecretVersioning(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	first, err := client.SetSecret(ctx, "api-key", azsecrets.SetSecretParameters{Value: to.Ptr("v1-value")}, nil)
	if err != nil {
		t.Fatalf("SetSecret(v1): %v", err)
	}

	second, err := client.SetSecret(ctx, "api-key", azsecrets.SetSecretParameters{Value: to.Ptr("v2-value")}, nil)
	if err != nil {
		t.Fatalf("SetSecret(v2): %v", err)
	}

	if first.ID.Version() == second.ID.Version() {
		t.Fatal("second SetSecret reused the first version id")
	}

	current, err := client.GetSecret(ctx, "api-key", "", nil)
	if err != nil {
		t.Fatalf("GetSecret(current): %v", err)
	}

	if *current.Value != "v2-value" {
		t.Fatalf("current value = %q, want v2-value", *current.Value)
	}

	old, err := client.GetSecret(ctx, "api-key", first.ID.Version(), nil)
	if err != nil {
		t.Fatalf("GetSecret(v1): %v", err)
	}

	if *old.Value != "v1-value" {
		t.Fatalf("v1 value = %q, want v1-value", *old.Value)
	}

	var versions []string

	pager := client.NewListSecretPropertiesVersionsPager("api-key", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListSecretPropertiesVersions: %v", err)
		}

		for _, item := range page.Value {
			versions = append(versions, item.ID.Version())
		}
	}

	if len(versions) != 2 {
		t.Fatalf("got %d versions %v, want 2", len(versions), versions)
	}
}

func TestSDKKeyVaultErrors(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	_, err := client.GetSecret(ctx, "missing", "", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("GetSecret(missing): got %v, want 404 ResponseError", err)
	}

	if respErr.ErrorCode != "SecretNotFound" {
		t.Fatalf("got error code %q, want SecretNotFound", respErr.ErrorCode)
	}

	_, err = client.DeleteSecret(ctx, "missing", nil)
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("DeleteSecret(missing): got %v, want 404 ResponseError", err)
	}
}
