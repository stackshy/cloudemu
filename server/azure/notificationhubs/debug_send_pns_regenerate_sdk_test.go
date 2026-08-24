// Real-user tests for the Notification Hubs DebugSend, GetPnsCredentials and
// RegenerateKeys operations, driving the official armnotificationhubs SDK
// clients against the CloudEmu Azure server mounted in an httptest TLS server.
package notificationhubs_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/notificationhubs/armnotificationhubs"

	"github.com/stackshy/cloudemu/v2"
	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// newExtrasEnv returns a client factory plus the provider backing it, so a test
// can seed data-plane device registrations directly on the driver.
func newExtrasEnv(t *testing.T) (*armnotificationhubs.ClientFactory, *azureprovider.Provider) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{NotificationHubs: cloudP.NotificationHubs})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud:     myCloud,
		Transport: ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}

	cf, err := armnotificationhubs.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewClientFactory: %v", err)
	}

	return cf, cloudP
}

// mkNamespaceHub creates namespace ns and hub under it through the SDK.
func mkNamespaceHub(ctx context.Context, t *testing.T, cf *armnotificationhubs.ClientFactory, ns, hub string) {
	t.Helper()

	if _, err := cf.NewNamespacesClient().CreateOrUpdate(ctx, testRG, ns,
		armnotificationhubs.NamespaceCreateOrUpdateParameters{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("namespace create: %v", err)
	}

	if _, err := cf.NewClient().CreateOrUpdate(ctx, testRG, ns, hub,
		armnotificationhubs.NotificationHubCreateOrUpdateParameters{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("hub create: %v", err)
	}
}

// TestSDKDebugSend seeds two device registrations, then asserts DebugSend
// reports both as successful targets and an empty hub reports zero.
func TestSDKDebugSend(t *testing.T) {
	ctx := context.Background()
	cf, provider := newExtrasEnv(t)
	mkNamespaceHub(ctx, t, cf, "my-ns", "hub1")

	// An empty hub: zero success, zero failure.
	empty, err := cf.NewClient().DebugSend(ctx, testRG, "my-ns", "hub1", nil)
	if err != nil {
		t.Fatalf("DebugSend (empty): %v", err)
	}

	if empty.Properties == nil || empty.Properties.Success == nil || *empty.Properties.Success != 0 {
		t.Fatalf("empty DebugSend success = %v, want 0", empty.Properties)
	}

	// Seed two registrations on the data plane (driver key is "{ns}/{hub}").
	const hubKey = "my-ns/hub1"

	for _, id := range []string{"reg-a", "reg-b"} {
		if _, err := provider.NotificationHubs.CreateRegistration(ctx, hubKey, notifdriver.AzureRegistration{
			RegistrationID: id,
			Platform:       "gcm",
			Handle:         "token-" + id,
		}); err != nil {
			t.Fatalf("seed registration %s: %v", id, err)
		}
	}

	got, err := cf.NewClient().DebugSend(ctx, testRG, "my-ns", "hub1", nil)
	if err != nil {
		t.Fatalf("DebugSend: %v", err)
	}

	if got.Properties == nil || got.Properties.Success == nil || *got.Properties.Success != 2 {
		t.Fatalf("DebugSend success = %v, want 2", got.Properties)
	}

	if got.Properties.Failure == nil || *got.Properties.Failure != 0 {
		t.Fatalf("DebugSend failure = %v, want 0", got.Properties.Failure)
	}

	// DebugSend on a missing hub 404s.
	if _, err := cf.NewClient().DebugSend(ctx, testRG, "my-ns", "ghost", nil); err == nil {
		t.Fatal("DebugSend on missing hub: expected error, got nil")
	}
}

// TestSDKGetPnsCredentials creates a hub carrying a GCM credential and asserts
// GetPnsCredentials echoes it back, while a hub without credentials reports none.
func TestSDKGetPnsCredentials(t *testing.T) {
	ctx := context.Background()
	cf, _ := newExtrasEnv(t)

	if _, err := cf.NewNamespacesClient().CreateOrUpdate(ctx, testRG, "my-ns",
		armnotificationhubs.NamespaceCreateOrUpdateParameters{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("namespace create: %v", err)
	}

	// Create a hub with a GCM (FCM legacy) credential.
	if _, err := cf.NewClient().CreateOrUpdate(ctx, testRG, "my-ns", "hub1",
		armnotificationhubs.NotificationHubCreateOrUpdateParameters{
			Location: to.Ptr("eastus"),
			Properties: &armnotificationhubs.NotificationHubProperties{
				GCMCredential: &armnotificationhubs.GCMCredential{
					Properties: &armnotificationhubs.GCMCredentialProperties{
						GoogleAPIKey: to.Ptr("AIzaSy-test-key"),
					},
				},
			},
		}, nil); err != nil {
		t.Fatalf("hub create with credential: %v", err)
	}

	creds, err := cf.NewClient().GetPnsCredentials(ctx, testRG, "my-ns", "hub1", nil)
	if err != nil {
		t.Fatalf("GetPnsCredentials: %v", err)
	}

	if creds.Properties == nil || creds.Properties.GCMCredential == nil ||
		creds.Properties.GCMCredential.Properties == nil ||
		creds.Properties.GCMCredential.Properties.GoogleAPIKey == nil ||
		*creds.Properties.GCMCredential.Properties.GoogleAPIKey != "AIzaSy-test-key" {
		t.Fatalf("GcmCredential round-trip failed: %+v", creds.Properties)
	}

	// A hub created without credentials reports an empty (but valid) set.
	if _, err := cf.NewClient().CreateOrUpdate(ctx, testRG, "my-ns", "bare",
		armnotificationhubs.NotificationHubCreateOrUpdateParameters{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("bare hub create: %v", err)
	}

	bare, err := cf.NewClient().GetPnsCredentials(ctx, testRG, "my-ns", "bare", nil)
	if err != nil {
		t.Fatalf("GetPnsCredentials (bare): %v", err)
	}

	if bare.Properties != nil && bare.Properties.GCMCredential != nil {
		t.Fatalf("bare hub reported a GCM credential: %+v", bare.Properties)
	}
}

// TestSDKRegenerateKeys rotates a hub authorization rule's primary key and
// asserts the change is visible and durable (a subsequent ListKeys reflects it).
func TestSDKRegenerateKeys(t *testing.T) {
	ctx := context.Background()
	cf, _ := newExtrasEnv(t)
	mkNamespaceHub(ctx, t, cf, "my-ns", "hub1")

	hubs := cf.NewClient()

	if _, err := hubs.CreateOrUpdateAuthorizationRule(ctx, testRG, "my-ns", "hub1", "sender",
		armnotificationhubs.SharedAccessAuthorizationRuleCreateOrUpdateParameters{
			Properties: &armnotificationhubs.SharedAccessAuthorizationRuleProperties{
				Rights: []*armnotificationhubs.AccessRights{to.Ptr(armnotificationhubs.AccessRightsSend)},
			},
		}, nil); err != nil {
		t.Fatalf("hub CreateOrUpdateAuthorizationRule: %v", err)
	}

	before, err := hubs.ListKeys(ctx, testRG, "my-ns", "hub1", "sender", nil)
	if err != nil {
		t.Fatalf("ListKeys before: %v", err)
	}

	regen, err := hubs.RegenerateKeys(ctx, testRG, "my-ns", "hub1", "sender",
		armnotificationhubs.PolicykeyResource{PolicyKey: to.Ptr("PrimaryKey")}, nil)
	if err != nil {
		t.Fatalf("RegenerateKeys: %v", err)
	}

	if regen.PrimaryKey == nil || before.PrimaryKey == nil || *regen.PrimaryKey == *before.PrimaryKey {
		t.Fatalf("primary key did not change: before=%v after=%v", before.PrimaryKey, regen.PrimaryKey)
	}

	if regen.SecondaryKey == nil || before.SecondaryKey == nil || *regen.SecondaryKey != *before.SecondaryKey {
		t.Fatalf("secondary key changed on PrimaryKey regenerate: before=%v after=%v",
			before.SecondaryKey, regen.SecondaryKey)
	}

	// The rotation is durable: ListKeys now returns the regenerated primary.
	after, err := hubs.ListKeys(ctx, testRG, "my-ns", "hub1", "sender", nil)
	if err != nil {
		t.Fatalf("ListKeys after: %v", err)
	}

	if after.PrimaryKey == nil || *after.PrimaryKey != *regen.PrimaryKey {
		t.Fatalf("ListKeys after regenerate = %v, want rotated primary %v", after.PrimaryKey, regen.PrimaryKey)
	}
}
