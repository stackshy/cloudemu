package eventgrid_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

// TestSDKDomainInputSchemaAndNetworkRoundTrip locks B4: a domain created with a
// non-default InputSchema and PublicNetworkAccess=Disabled must echo both back
// on the create response and a subsequent Get, not the hardcoded defaults.
func TestSDKDomainInputSchemaAndNetworkRoundTrip(t *testing.T) {
	cf, _ := newEGFactory(t)
	dc := cf.NewDomainsClient()
	ctx := context.Background()

	poller, err := dc.BeginCreateOrUpdate(ctx, testRG, "schema-domain", armeventgrid.Domain{
		Location: to.Ptr("eastus"),
		Properties: &armeventgrid.DomainProperties{
			InputSchema:         to.Ptr(armeventgrid.InputSchemaCloudEventSchemaV10),
			PublicNetworkAccess: to.Ptr(armeventgrid.PublicNetworkAccessDisabled),
		},
	}, nil)
	if err != nil {
		t.Fatalf("Domains.BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("create PollUntilDone: %v", err)
	}

	if created.Properties == nil || created.Properties.InputSchema == nil ||
		*created.Properties.InputSchema != armeventgrid.InputSchemaCloudEventSchemaV10 {
		t.Fatalf("create inputSchema = %v, want CloudEventSchemaV1_0", created.Properties)
	}

	if created.Properties.PublicNetworkAccess == nil ||
		*created.Properties.PublicNetworkAccess != armeventgrid.PublicNetworkAccessDisabled {
		t.Fatalf("create publicNetworkAccess = %v, want Disabled", created.Properties)
	}

	got, err := dc.Get(ctx, testRG, "schema-domain", nil)
	if err != nil {
		t.Fatalf("Domains.Get: %v", err)
	}

	if got.Properties == nil || got.Properties.InputSchema == nil ||
		*got.Properties.InputSchema != armeventgrid.InputSchemaCloudEventSchemaV10 {
		t.Fatalf("Get inputSchema = %v, want CloudEventSchemaV1_0", got.Properties)
	}

	if got.Properties.PublicNetworkAccess == nil ||
		*got.Properties.PublicNetworkAccess != armeventgrid.PublicNetworkAccessDisabled {
		t.Fatalf("Get publicNetworkAccess = %v, want Disabled", got.Properties)
	}
}

// TestSDKDomainInputSchemaImmutable asserts a domain's input schema cannot be
// changed by a later CreateOrUpdate, mirroring topic input-schema immutability.
func TestSDKDomainInputSchemaImmutable(t *testing.T) {
	cf, _ := newEGFactory(t)
	dc := cf.NewDomainsClient()
	ctx := context.Background()

	mk := func(schema armeventgrid.InputSchema) {
		poller, err := dc.BeginCreateOrUpdate(ctx, testRG, "immutable-domain", armeventgrid.Domain{
			Location: to.Ptr("eastus"),
			Properties: &armeventgrid.DomainProperties{
				InputSchema: to.Ptr(schema),
			},
		}, nil)
		if err != nil {
			t.Fatalf("BeginCreateOrUpdate: %v", err)
		}

		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			t.Fatalf("PollUntilDone: %v", err)
		}
	}

	mk(armeventgrid.InputSchemaCloudEventSchemaV10)
	mk(armeventgrid.InputSchemaEventGridSchema)

	got, err := dc.Get(ctx, testRG, "immutable-domain", nil)
	if err != nil {
		t.Fatalf("Domains.Get: %v", err)
	}

	if got.Properties == nil || got.Properties.InputSchema == nil ||
		*got.Properties.InputSchema != armeventgrid.InputSchemaCloudEventSchemaV10 {
		t.Fatalf("inputSchema changed after re-create = %v, want CloudEventSchemaV1_0", got.Properties)
	}
}
