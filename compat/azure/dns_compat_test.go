package azure

import (
	"context"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzureDNSCompat drives an Azure DNS zone and record-set lifecycle through
// the real azure-sdk-for-go armdns clients. Azure DNS is an ARM control-plane
// API (Microsoft.Network/dnsZones), so the clients run over the harness's TLS
// server with a fake bearer credential, pointed at the emulator via a custom
// cloud.Configuration endpoint. The armdns zone/record operations map onto the
// portable "dns" driver, so operation names match Route53's / CloudDNS's in
// docs/coverage/coverage.json.
//
// The portable dns driver also models health checks (CreateHealthCheck, ...),
// but the Azure DNS wire handler routes only zones and record sets — Azure has
// no health-check resource under Microsoft.Network/dnsZones — so those ops are
// coverage gaps and are not asserted here.
func TestAzureDNSCompat(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{DNS: provider.DNS})

	const (
		testSub = "sub-1"
		testRG  = "rg-1"

		zoneName = "example.com"
		recName  = "www"
		recIP    = "192.0.2.1"

		ttlInitial int64 = 300
		ttlUpdated int64 = 3600
	)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: sess.Endpoint(),
				Audience: "https://management.azure.com",
			},
		},
	}

	factory, err := armdns.NewClientFactory(testSub, compat.FakeAzureCred(), &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	})
	if err != nil {
		t.Fatalf("armdns.NewClientFactory: %v", err)
	}

	zones := factory.NewZonesClient()
	records := factory.NewRecordSetsClient()
	ctx := context.Background()

	const svc = "dns"

	// First CreateOrUpdate on a new zone name creates it (driver CreateZone).
	sess.Op(svc, "CreateZone", func() error {
		created, err := zones.CreateOrUpdate(ctx, testRG, zoneName, armdns.Zone{
			Location: to.Ptr("global"),
			Tags:     map[string]*string{"env": to.Ptr("test")},
		}, nil)
		if err != nil {
			return err
		}

		if created.Name == nil || *created.Name != zoneName {
			return fmt.Errorf("CreateOrUpdate name = %v, want %q", created.Name, zoneName)
		}

		return nil
	})

	// A second CreateOrUpdate on the same zone updates its tags (driver UpdateZone).
	sess.Op(svc, "UpdateZone", func() error {
		updated, err := zones.CreateOrUpdate(ctx, testRG, zoneName, armdns.Zone{
			Location: to.Ptr("global"),
			Tags:     map[string]*string{"env": to.Ptr("prod")},
		}, nil)
		if err != nil {
			return err
		}

		if updated.Tags["env"] == nil || *updated.Tags["env"] != "prod" {
			return fmt.Errorf("UpdateZone tags = %v, want env=prod", updated.Tags)
		}

		return nil
	})

	sess.Op(svc, "GetZone", func() error {
		got, err := zones.Get(ctx, testRG, zoneName, nil)
		if err != nil {
			return err
		}

		if got.Name == nil || *got.Name != zoneName {
			return fmt.Errorf("GetZone name = %v, want %q", got.Name, zoneName)
		}

		return nil
	})

	sess.Op(svc, "ListZones", func() error {
		var names []string

		pager := zones.NewListByResourceGroupPager(testRG, nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				return perr
			}

			for _, z := range page.Value {
				names = append(names, *z.Name)
			}
		}

		if len(names) != 1 || names[0] != zoneName {
			return fmt.Errorf("ListZones = %v, want [%s]", names, zoneName)
		}

		return nil
	})

	// First record-set CreateOrUpdate creates it (driver CreateRecord).
	sess.Op(svc, "CreateRecord", func() error {
		set, err := records.CreateOrUpdate(ctx, testRG, zoneName, recName, armdns.RecordTypeA, armdns.RecordSet{
			Properties: &armdns.RecordSetProperties{
				TTL:      to.Ptr(ttlInitial),
				ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr(recIP)}},
			},
		}, nil)
		if err != nil {
			return err
		}

		if set.Name == nil || *set.Name != recName {
			return fmt.Errorf("CreateRecord name = %v, want %q", set.Name, recName)
		}

		return nil
	})

	// A second CreateOrUpdate on the same record set updates its TTL (driver UpdateRecord).
	sess.Op(svc, "UpdateRecord", func() error {
		set, err := records.CreateOrUpdate(ctx, testRG, zoneName, recName, armdns.RecordTypeA, armdns.RecordSet{
			Properties: &armdns.RecordSetProperties{
				TTL:      to.Ptr(ttlUpdated),
				ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr(recIP)}},
			},
		}, nil)
		if err != nil {
			return err
		}

		if set.Properties == nil || set.Properties.TTL == nil || *set.Properties.TTL != ttlUpdated {
			return fmt.Errorf("UpdateRecord TTL = %+v, want %d", set.Properties, ttlUpdated)
		}

		return nil
	})

	sess.Op(svc, "GetRecord", func() error {
		got, err := records.Get(ctx, testRG, zoneName, recName, armdns.RecordTypeA, nil)
		if err != nil {
			return err
		}

		if got.Properties == nil || len(got.Properties.ARecords) != 1 ||
			*got.Properties.ARecords[0].IPv4Address != recIP {
			return fmt.Errorf("GetRecord ARecords = %+v, want [%s]", got.Properties, recIP)
		}

		return nil
	})

	sess.Op(svc, "ListRecords", func() error {
		var listed []string

		pager := records.NewListByDNSZonePager(testRG, zoneName, nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				return perr
			}

			for _, rs := range page.Value {
				listed = append(listed, *rs.Name)
			}
		}

		// The zone's implicit SOA/NS sets may also list; require www present.
		found := false
		for _, n := range listed {
			if n == recName {
				found = true
			}
		}

		if !found {
			return fmt.Errorf("ListRecords = %v, want to contain %q", listed, recName)
		}

		return nil
	})

	sess.Op(svc, "DeleteRecord", func() error {
		_, err := records.Delete(ctx, testRG, zoneName, recName, armdns.RecordTypeA, nil)

		return err
	})

	sess.Op(svc, "DeleteZone", func() error {
		poller, err := zones.BeginDelete(ctx, testRG, zoneName, nil)
		if err != nil {
			return err
		}

		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			return err
		}

		return nil
	})
}
