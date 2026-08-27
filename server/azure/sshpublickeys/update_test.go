package sshpublickeys_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newSSHClient(t *testing.T, ts *httptest.Server) *armcompute.SSHPublicKeysClient {
	t.Helper()

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

	return client
}

// TestSDKSSHPublicKeyUpdate drives SSHPublicKeysClient.Update (PATCH) through a
// real armcompute client: the public key and tags are updated in place, and a
// subsequent Get reflects the new values (tags are replaced, not merged).
func TestSDKSSHPublicKeyUpdate(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		SSHPublicKeys:   cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSSHClient(t, ts)
	ctx := context.Background()

	const keyA = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ a@cloudemu"

	const keyB = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ b@cloudemu"

	if _, err := client.Create(ctx, "rg-1", "key-upd",
		armcompute.SSHPublicKeyResource{
			Location:   to.Ptr("eastus"),
			Tags:       map[string]*string{"env": to.Ptr("test")},
			Properties: &armcompute.SSHPublicKeyResourceProperties{PublicKey: to.Ptr(keyA)},
		}, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := client.Update(ctx, "rg-1", "key-upd",
		armcompute.SSHPublicKeyUpdateResource{
			Tags:       map[string]*string{"team": to.Ptr("infra")},
			Properties: &armcompute.SSHPublicKeyResourceProperties{PublicKey: to.Ptr(keyB)},
		}, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if updated.Properties == nil || updated.Properties.PublicKey == nil || *updated.Properties.PublicKey != keyB {
		t.Errorf("update publicKey=%v want %q", updated.Properties, keyB)
	}

	got, err := client.Get(ctx, "rg-1", "key-upd", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.PublicKey == nil || *got.Properties.PublicKey != keyB {
		t.Errorf("get publicKey mismatch, want %q", keyB)
	}

	if got.Tags["team"] == nil || *got.Tags["team"] != "infra" {
		t.Errorf("tags=%v want team=infra", got.Tags)
	}

	if _, ok := got.Tags["env"]; ok {
		t.Errorf("PATCH should replace tags, but env survived: %v", got.Tags)
	}
}

// TestSDKSSHPublicKeyUpdateMissing asserts PATCH on a key that does not exist
// returns an error rather than silently creating one.
func TestSDKSSHPublicKeyUpdateMissing(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		SSHPublicKeys:   cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSSHClient(t, ts)

	if _, err := client.Update(context.Background(), "rg-1", "ghost",
		armcompute.SSHPublicKeyUpdateResource{
			Properties: &armcompute.SSHPublicKeyResourceProperties{PublicKey: to.Ptr("ssh-rsa X")},
		}, nil); err == nil {
		t.Fatal("expected Update on a missing key to fail")
	}
}
