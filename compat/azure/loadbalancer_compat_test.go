package azure

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzureLoadBalancerCompat drives an Azure Load Balancer lifecycle through
// the real azure-sdk-for-go armnetwork.LoadBalancersClient. Azure Load Balancer
// is an ARM control-plane API (Microsoft.Network/loadBalancers), so the client
// runs over the harness's TLS server with a fake bearer credential, pointed at
// the emulator via a custom cloud.Configuration endpoint.
//
// The portable "loadbalancer" driver (Azure native LB) models many ops —
// listeners, rules, target groups, target registration/health. The Azure LB
// wire handler routes only the loadBalancers resource itself
// (CreateOrUpdate / Get / List / Delete), reflecting rules and pools inside the
// LB body rather than as separately addressable ARM resources. So only the
// LB-lifecycle portable ops are asserted here; the listener/rule/target-group/
// target ops are coverage gaps for Azure and are not asserted.
func TestAzureLoadBalancerCompat(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{LB: provider.LB})

	const (
		testSub = "sub-1"
		testRG  = "rg-1"
		lbName  = "lb-1"
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

	client, err := armnetwork.NewLoadBalancersClient(testSub, compat.FakeAzureCred(), &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	})
	if err != nil {
		t.Fatalf("armnetwork.NewLoadBalancersClient: %v", err)
	}

	ctx := context.Background()

	const svc = "loadbalancer"

	sess.Op(svc, "CreateLoadBalancer", func() error {
		poller, err := client.BeginCreateOrUpdate(ctx, testRG, lbName, armnetwork.LoadBalancer{
			Location: to.Ptr("eastus"),
			Tags:     map[string]*string{"env": to.Ptr("test")},
			SKU:      &armnetwork.LoadBalancerSKU{Name: to.Ptr(armnetwork.LoadBalancerSKUNameStandard)},
			Properties: &armnetwork.LoadBalancerPropertiesFormat{
				BackendAddressPools: []*armnetwork.BackendAddressPool{
					{Name: to.Ptr("pool-a")},
				},
			},
		}, nil)
		if err != nil {
			return err
		}

		created, err := poller.PollUntilDone(ctx, nil)
		if err != nil {
			return err
		}

		if created.Name == nil || *created.Name != lbName {
			return fmt.Errorf("CreateLoadBalancer name = %v, want %q", created.Name, lbName)
		}

		if created.Properties == nil || len(created.Properties.BackendAddressPools) != 1 {
			return fmt.Errorf("CreateLoadBalancer backend pools = %+v, want 1", created.Properties)
		}

		return nil
	})

	sess.Op(svc, "DescribeLoadBalancers", func() error {
		got, err := client.Get(ctx, testRG, lbName, nil)
		if err != nil {
			return err
		}

		if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
			return fmt.Errorf("DescribeLoadBalancers tags = %v, want env=test", got.Tags)
		}

		var names []string

		pager := client.NewListPager(testRG, nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				return perr
			}

			for _, lb := range page.Value {
				names = append(names, *lb.Name)
			}
		}

		if len(names) != 1 || names[0] != lbName {
			return fmt.Errorf("DescribeLoadBalancers list = %v, want [%s]", names, lbName)
		}

		return nil
	})

	sess.Op(svc, "DeleteLoadBalancer", func() error {
		poller, err := client.BeginDelete(ctx, testRG, lbName, nil)
		if err != nil {
			return err
		}

		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			return err
		}

		_, err = client.Get(ctx, testRG, lbName, nil)

		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
			return fmt.Errorf("Get after delete: got %v, want 404", err)
		}

		return nil
	})
}
