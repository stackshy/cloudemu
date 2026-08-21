package azure

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/notificationhubs/armnotificationhubs"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	notifService = "notification"
	notifSub     = "sub-1"
	notifRG      = "rg-1"
	notifNS      = "compat-ns"
)

// TestNotificationHubsCompat drives a real armnotificationhubs client against
// CloudEmu's Azure wire server and records one compat result per portable
// notification op the Microsoft.NotificationHubs handler routes.
func TestNotificationHubsCompat(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{NotificationHubs: provider.NotificationHubs})

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: sess.Endpoint(),
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armnotificationhubs.NewClientFactory(notifSub, compat.FakeAzureCred(), opts)
	if err != nil {
		t.Fatalf("armnotificationhubs.NewClientFactory: %v", err)
	}

	namespaces := cf.NewNamespacesClient()
	ctx := context.Background()

	sess.Op(notifService, "CreateTopic", func() error {
		_, cerr := namespaces.CreateOrUpdate(ctx, notifRG, notifNS,
			armnotificationhubs.NamespaceCreateOrUpdateParameters{
				Location: to.Ptr("global"),
				Tags:     map[string]*string{"env": to.Ptr("test")},
			}, nil)
		return cerr
	})

	sess.Op(notifService, "GetTopic", func() error {
		_, cerr := namespaces.Get(ctx, notifRG, notifNS, nil)
		return cerr
	})

	sess.Op(notifService, "UpdateTopic", func() error {
		_, cerr := namespaces.CreateOrUpdate(ctx, notifRG, notifNS,
			armnotificationhubs.NamespaceCreateOrUpdateParameters{
				Location: to.Ptr("global"),
				Tags:     map[string]*string{"env": to.Ptr("prod")},
			}, nil)
		return cerr
	})

	sess.Op(notifService, "ListTopics", func() error {
		pager := namespaces.NewListPager(notifRG, nil)
		for pager.More() {
			if _, perr := pager.NextPage(ctx); perr != nil {
				return perr
			}
		}
		return nil
	})

	sess.Op(notifService, "DeleteTopic", func() error {
		poller, perr := namespaces.BeginDelete(ctx, notifRG, notifNS, nil)
		if perr != nil {
			return perr
		}
		_, perr = poller.PollUntilDone(ctx, nil)
		return perr
	})
}
