package containerapps_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v2"
)

func rawDo(t *testing.T, ts *httptest.Server, method, path string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method,
		ts.URL+path+"?api-version=2024-03-01", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, body
}

func createEnv(t *testing.T, ts *httptest.Server) *armappcontainers.ManagedEnvironmentsClient {
	t.Helper()

	envs, err := armappcontainers.NewManagedEnvironmentsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	poller, err := envs.BeginCreateOrUpdate(context.Background(), rgName, envName, armappcontainers.ManagedEnvironment{
		Location: to.Ptr("eastus"), Tags: map[string]*string{"team": to.Ptr("core")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := poller.PollUntilDone(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	return envs
}

func is404(err error) bool {
	var re *azcore.ResponseError

	return errors.As(err, &re) && re.StatusCode == http.StatusNotFound
}

// TestSDKDaprComponentLifecycle is AZAPP-01 for daprComponents: the child is
// its own resource, its secrets are names only on GET, and the environment is
// untouched by its writes.
func TestSDKDaprComponentLifecycle(t *testing.T) {
	ctx := context.Background()
	ts := newServer(t)
	createEnv(t, ts)

	envPath := "/subscriptions/" + subID + "/resourceGroups/" + rgName +
		"/providers/Microsoft.App/managedEnvironments/" + envName
	_, before := rawDo(t, ts, http.MethodGet, envPath)

	dapr, err := armappcontainers.NewDaprComponentsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := dapr.Get(ctx, rgName, envName, "state", nil); !is404(err) {
		t.Fatalf("GET before PUT: %v, want 404", err)
	}

	comp := armappcontainers.DaprComponent{Properties: &armappcontainers.DaprComponentProperties{
		ComponentType: to.Ptr("state.azure.blobstorage"), Version: to.Ptr("v1"),
		Metadata: []*armappcontainers.DaprMetadata{
			{Name: to.Ptr("accountName"), Value: to.Ptr("acct")},
			{Name: to.Ptr("accountKey"), SecretRef: to.Ptr("key")},
		},
		Secrets: []*armappcontainers.Secret{{Name: to.Ptr("key"), Value: to.Ptr("s3cr3t")}},
		Scopes:  []*string{to.Ptr("api")},
	}}

	if _, err := dapr.CreateOrUpdate(ctx, rgName, envName, "state", comp, nil); err != nil {
		t.Fatal(err)
	}

	got, err := dapr.Get(ctx, rgName, envName, "state", nil)
	if err != nil {
		t.Fatal(err)
	}

	p := got.Properties
	if *p.ComponentType != "state.azure.blobstorage" || len(p.Metadata) != 2 || *p.Metadata[1].SecretRef != "key" ||
		len(p.Secrets) != 1 || p.Secrets[0].Value != nil || *p.Scopes[0] != "api" {
		t.Errorf("GET dapr component = %+v", p)
	}

	secrets, err := dapr.ListSecrets(ctx, rgName, envName, "state", nil)
	if err != nil || len(secrets.Value) != 1 || *secrets.Value[0].Value != "s3cr3t" {
		t.Errorf("listSecrets = %+v, %v", secrets, err)
	}

	if _, after := rawDo(t, ts, http.MethodGet, envPath); !bytes.Equal(before, after) {
		t.Errorf("environment changed by a dapr PUT:\nbefore %s\nafter  %s", before, after)
	}

	if _, err := dapr.Delete(ctx, rgName, envName, "state", nil); err != nil {
		t.Fatal(err)
	}

	if _, err := dapr.Get(ctx, rgName, envName, "state", nil); !is404(err) {
		t.Errorf("GET after DELETE: %v, want 404", err)
	}

	if code, _ := rawDo(t, ts, http.MethodGet, envPath); code != http.StatusOK {
		t.Errorf("environment GET after dapr DELETE: %d, want 200", code)
	}
}

func TestSDKEnvStorageLifecycle(t *testing.T) {
	ctx := context.Background()
	ts := newServer(t)
	envs := createEnv(t, ts)

	storages, err := armappcontainers.NewManagedEnvironmentsStoragesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	mk := func(mode armappcontainers.AccessMode) armappcontainers.ManagedEnvironmentStorage {
		return armappcontainers.ManagedEnvironmentStorage{Properties: &armappcontainers.ManagedEnvironmentStorageProperties{
			AzureFile: &armappcontainers.AzureFileProperties{
				AccountName: to.Ptr("acct"), AccountKey: to.Ptr("k3y"), ShareName: to.Ptr("share"), AccessMode: to.Ptr(mode),
			},
		}}
	}

	put, err := storages.CreateOrUpdate(ctx, rgName, envName, "s1", mk(armappcontainers.AccessModeReadOnly), nil)
	if err != nil {
		t.Fatal(err)
	}

	if put.Properties.AzureFile.AccountKey != nil {
		t.Error("PUT echoed the account key")
	}

	if _, err := storages.CreateOrUpdate(ctx, rgName, envName, "s1", mk("Bogus"), nil); err == nil {
		t.Error("accessMode Bogus accepted, want 400")
	}

	list, err := storages.List(ctx, rgName, envName, nil)
	if err != nil || len(list.Value) != 1 || *list.Value[0].Properties.AzureFile.AccessMode != "ReadOnly" ||
		list.Value[0].Properties.AzureFile.AccountKey != nil {
		t.Errorf("list = %+v, %v", list, err)
	}

	// Deleting the environment removes its storages: a recreated environment
	// starts empty.
	poller, err := envs.BeginDelete(ctx, rgName, envName, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := storages.CreateOrUpdate(ctx, rgName, envName, "s2", mk(armappcontainers.AccessModeReadWrite), nil); !is404(err) ||
		!strings.Contains(err.Error(), "ParentResourceNotFound") {
		t.Errorf("PUT under a deleted environment: %v, want 404 ParentResourceNotFound", err)
	}

	createEnv(t, ts)

	if _, err := storages.Get(ctx, rgName, envName, "s1", nil); !is404(err) {
		t.Errorf("storage survived its environment's delete: %v", err)
	}
}
