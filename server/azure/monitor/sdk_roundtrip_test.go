package monitor_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// fakeCred is a static-token credential for tests. The real ARM endpoint
// requires AAD tokens; our handler ignores the Authorization header, but the
// SDK still demands a credential implementation.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// armClientOptions builds the shared arm.ClientOptions that point every SDK
// client in this file at ts.
func armClientOptions(ts *httptest.Server) *arm.ClientOptions {
	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			// SDK wants TLS by default; disable retries so failures surface
			// immediately instead of being retried away.
			Retry: policy.RetryOptions{MaxRetries: -1},
		},
	}
}

// TestSDKMetricAlertCreateOrUpdateReturns200 is the load-bearing regression
// for the BLOCKER finding: the generated armmonitor MetricAlertsClient only
// accepts a 200 response from CreateOrUpdate (see runtime.HasStatusCode(...,
// http.StatusOK) in metricalerts_client.go — 201 is not in the accepted set),
// so a first-time create returning 201 makes every real client error out.
// This test fails with a ResponseError before the fix and passes after.
func TestSDKMetricAlertCreateOrUpdateReturns200(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Monitor: cloudP.Monitor, VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := armmonitor.NewMetricAlertsClient("sub-1", fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	rule := metricAlertResource(80)

	// First-time create.
	created, err := client.CreateOrUpdate(ctx, "rg-1", "cpu-alert", rule, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate (create): %v", err)
	}

	if created.Name == nil || *created.Name != "cpu-alert" {
		t.Fatalf("created name = %v, want cpu-alert", created.Name)
	}

	// Idempotent re-PUT (update in place).
	rule2 := metricAlertResource(90)
	if _, err := client.CreateOrUpdate(ctx, "rg-1", "cpu-alert", rule2, nil); err != nil {
		t.Fatalf("CreateOrUpdate (update): %v", err)
	}

	got, err := client.Get(ctx, "rg-1", "cpu-alert", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.Severity == nil || *got.Properties.Severity != 90 {
		t.Fatalf("severity after update = %v, want 90 (update did not apply)", got.Properties.Severity)
	}

	pager := client.NewListByResourceGroupPager("rg-1", nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NewListByResourceGroupPager: %v", err)
		}

		for _, v := range page.Value {
			if v.Name != nil {
				names = append(names, *v.Name)
			}
		}
	}

	if len(names) != 1 || names[0] != "cpu-alert" {
		t.Fatalf("list = %v, want [cpu-alert]", names)
	}

	if _, err := client.Delete(ctx, "rg-1", "cpu-alert", nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// metricAlertResource builds a minimal, valid MetricAlertResource whose
// severity is the given value (so two calls produce observably different
// bodies for the create-then-update assertion).
func metricAlertResource(severity int32) armmonitor.MetricAlertResource {
	return armmonitor.MetricAlertResource{
		Location: to.Ptr("global"),
		Properties: &armmonitor.MetricAlertProperties{
			Severity:            to.Ptr(severity),
			Enabled:             to.Ptr(true),
			Scopes:              []*string{to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm1")},
			EvaluationFrequency: to.Ptr("PT1M"),
			WindowSize:          to.Ptr("PT5M"),
			Criteria: &armmonitor.MetricAlertSingleResourceMultipleMetricCriteria{
				AllOf: []*armmonitor.MetricCriteria{{
					Name:            to.Ptr("cpu"),
					MetricName:      to.Ptr("Percentage CPU"),
					MetricNamespace: to.Ptr("Microsoft.Compute/virtualMachines"),
					Operator:        to.Ptr(armmonitor.OperatorGreaterThan),
					Threshold:       to.Ptr(80.0),
					TimeAggregation: to.Ptr(armmonitor.AggregationTypeEnumAverage),
				}},
			},
		},
	}
}

// newVMSDKClient builds a real azure-sdk-for-go armcompute VirtualMachinesClient
// pointed at ts, mirroring server/azure/virtualmachines's sdk_roundtrip_test.go.
func newVMSDKClient(t *testing.T, ts *httptest.Server) *armcompute.VirtualMachinesClient {
	t.Helper()

	client, err := armcompute.NewVirtualMachinesClient("sub-1", fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	return client
}

func putSDKVM(t *testing.T, client *armcompute.VirtualMachinesClient, name string) {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(context.Background(), "rg-1", name, armcompute.VirtualMachine{
		Location: to.Ptr("eastus"),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardB1S)},
			StorageProfile: &armcompute.StorageProfile{
				ImageReference: &armcompute.ImageReference{
					Publisher: to.Ptr("Canonical"), Offer: to.Ptr("UbuntuServer"), SKU: to.Ptr("22.04-LTS"), Version: to.Ptr("latest"),
				},
			},
			OSProfile: &armcompute.OSProfile{ComputerName: to.Ptr(name), AdminUsername: to.Ptr("azureuser")},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate %s: %v", name, err)
	}

	if _, err := poller.PollUntilDone(context.Background(), nil); err != nil {
		t.Fatalf("PollUntilDone %s: %v", name, err)
	}
}

// TestSDKMetricsIsolatedPerResource is the load-bearing regression for the
// per-resource isolation finding: a real armmonitor MetricsClient query
// against one VM's resourceUri must not see another VM's datapoints, even
// though both VMs share the same Microsoft.Compute/virtualMachines namespace
// and metric name.
func TestSDKMetricsIsolatedPerResource(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Monitor: cloudP.Monitor, VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	vmClient := newVMSDKClient(t, ts)
	putSDKVM(t, vmClient, "vm1")
	putSDKVM(t, vmClient, "vm2")

	ctx := context.Background()

	// Stop vm2 only, driving its Percentage CPU to 0.
	poller, err := vmClient.BeginPowerOff(ctx, "rg-1", "vm2", nil)
	if err != nil {
		t.Fatalf("BeginPowerOff: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone (powerOff): %v", err)
	}

	metricsClient, err := armmonitor.NewMetricsClient("sub-1", fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	vm1URI := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm1"

	resp, err := metricsClient.List(ctx, vm1URI, &armmonitor.MetricsClientListOptions{
		Metricnames: to.Ptr("Percentage CPU"),
		Aggregation: to.Ptr("average"),
	})
	if err != nil {
		t.Fatalf("List vm1 metrics: %v", err)
	}

	if len(resp.Value) != 1 || len(resp.Value[0].Timeseries) == 0 {
		t.Fatalf("vm1 metrics response shape = %+v", resp.Value)
	}

	for _, dp := range resp.Value[0].Timeseries[0].Data {
		if dp.Average == nil || *dp.Average != 25 {
			t.Fatalf("vm1 metrics leaked another resource's datapoint: average = %v, want 25", dp.Average)
		}
	}
}
