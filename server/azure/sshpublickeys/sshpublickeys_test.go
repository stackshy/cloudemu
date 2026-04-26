package sshpublickeys_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestSDKSSHPublicKeyRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		SSHPublicKeys:   cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := armcompute.NewSSHPublicKeysClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	created, err := client.Create(ctx, "rg-1", "key-1",
		armcompute.SSHPublicKeyResource{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.SSHPublicKeyResourceProperties{
				PublicKey: to.Ptr("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ test@cloudemu"),
			},
		}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.Name == nil || *created.Name != "key-1" {
		t.Errorf("name=%v want key-1", created.Name)
	}

	if created.Properties == nil || created.Properties.PublicKey == nil ||
		*created.Properties.PublicKey == "" {
		t.Errorf("publicKey missing in response")
	}

	got, err := client.Get(ctx, "rg-1", "key-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name == nil || *got.Name != "key-1" {
		t.Errorf("got.Name=%v", got.Name)
	}

	pager := client.NewListByResourceGroupPager("rg-1", nil)

	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, k := range page.Value {
			if k.Name != nil && *k.Name == "key-1" {
				found = true
			}
		}
	}

	if !found {
		t.Error("List did not return key-1")
	}

	if _, err := client.Delete(ctx, "rg-1", "key-1", nil); err != nil {
		t.Errorf("Delete: %v", err)
	}
}
