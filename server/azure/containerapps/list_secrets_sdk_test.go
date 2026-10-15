package containerapps_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v2"
)

// azurerm_container_app reads secret values through POST
// containerApps/{app}/listSecrets, since a GET returns names only.
func TestSDKContainerAppListSecrets(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()
	envID := seedRevisionEnv(t, ctx, ts)

	appClient, err := armappcontainers.NewContainerAppsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewContainerAppsClient: %v", err)
	}

	kvURL := "https://kv1.vault.azure.net/secrets/db"
	identity := "system"

	poller, err := appClient.BeginCreateOrUpdate(ctx, rgName, "secret-app", armappcontainers.ContainerApp{
		Location: to.Ptr("eastus"),
		Properties: &armappcontainers.ContainerAppProperties{
			EnvironmentID: to.Ptr(envID),
			Configuration: &armappcontainers.Configuration{
				Secrets: []*armappcontainers.Secret{
					{Name: to.Ptr("api-key"), Value: to.Ptr("s3cr3t")},
					{Name: to.Ptr("db"), KeyVaultURL: to.Ptr(kvURL), Identity: to.Ptr(identity)},
				},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{{Name: to.Ptr("main"), Image: to.Ptr("nginx")}},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	got, err := appClient.Get(ctx, rgName, "secret-app", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	for _, s := range got.Properties.Configuration.Secrets {
		if s.Value != nil {
			t.Fatalf("GET leaked the value of secret %s", *s.Name)
		}
	}

	resp, err := appClient.ListSecrets(ctx, rgName, "secret-app", nil)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	tests := []struct {
		name, value, kvURL, identity string
	}{
		{name: "api-key", value: "s3cr3t"},
		{name: "db", kvURL: kvURL, identity: identity},
	}

	if len(resp.Value) != len(tests) {
		t.Fatalf("ListSecrets returned %d secrets, want %d", len(resp.Value), len(tests))
	}

	for i, tc := range tests {
		s := resp.Value[i]
		if deref(s.Name) != tc.name || deref(s.Value) != tc.value ||
			deref(s.KeyVaultURL) != tc.kvURL || deref(s.Identity) != tc.identity {
			t.Fatalf("secret %d = {%s %s %s %s}, want %+v", i,
				deref(s.Name), deref(s.Value), deref(s.KeyVaultURL), deref(s.Identity), tc)
		}
	}

	if _, err := appClient.ListSecrets(ctx, rgName, "missing-app", nil); err == nil {
		t.Fatal("ListSecrets on a missing app succeeded, want 404")
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
