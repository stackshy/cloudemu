package monitor_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKAutoscaleSettingRoundTrip covers GAP-A: autoscaleSettings were entirely
// unserved (the canonicalType map skipped them), so `az monitor autoscale` and
// azurerm_monitor_autoscale_setting failed. The real armmonitor
// AutoscaleSettingsClient must create and read back a setting with its profiles,
// rules (metricTrigger + scaleAction) and capacity intact.
func TestSDKAutoscaleSettingRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Monitor: cloudP.Monitor}))
	t.Cleanup(ts.Close)

	client, err := armmonitor.NewAutoscaleSettingsClient("sub-1", fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	targetURI := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachineScaleSets/vmss1"

	setting := armmonitor.AutoscaleSettingResource{
		Location: to.Ptr("eastus"),
		Properties: &armmonitor.AutoscaleSetting{
			Enabled:           to.Ptr(true),
			Name:              to.Ptr("cpu-scale"),
			TargetResourceURI: to.Ptr(targetURI),
			Profiles: []*armmonitor.AutoscaleProfile{{
				Name:     to.Ptr("default"),
				Capacity: &armmonitor.ScaleCapacity{Minimum: to.Ptr("1"), Maximum: to.Ptr("10"), Default: to.Ptr("2")},
				Rules: []*armmonitor.ScaleRule{{
					MetricTrigger: &armmonitor.MetricTrigger{
						MetricName:        to.Ptr("Percentage CPU"),
						MetricResourceURI: to.Ptr(targetURI),
						Operator:          to.Ptr(armmonitor.ComparisonOperationTypeGreaterThan),
						Statistic:         to.Ptr(armmonitor.MetricStatisticTypeAverage),
						Threshold:         to.Ptr(75.0),
						TimeAggregation:   to.Ptr(armmonitor.TimeAggregationTypeAverage),
						TimeGrain:         to.Ptr("PT1M"),
						TimeWindow:        to.Ptr("PT5M"),
					},
					ScaleAction: &armmonitor.ScaleAction{
						Cooldown:  to.Ptr("PT5M"),
						Direction: to.Ptr(armmonitor.ScaleDirectionIncrease),
						Type:      to.Ptr(armmonitor.ScaleTypeChangeCount),
						Value:     to.Ptr("1"),
					},
				}},
			}},
		},
	}

	if _, err := client.CreateOrUpdate(ctx, "rg-1", "vmss-autoscale", setting, nil); err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	got, err := client.Get(ctx, "rg-1", "vmss-autoscale", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name == nil || *got.Name != "vmss-autoscale" {
		t.Fatalf("name = %v, want vmss-autoscale", got.Name)
	}

	assertAutoscaleRoundTrip(t, got.Properties, targetURI)
}

func assertAutoscaleRoundTrip(t *testing.T, props *armmonitor.AutoscaleSetting, targetURI string) {
	t.Helper()

	if props == nil {
		t.Fatal("properties nil")
	}

	if props.TargetResourceURI == nil || *props.TargetResourceURI != targetURI {
		t.Fatalf("targetResourceUri = %v, want %s", props.TargetResourceURI, targetURI)
	}

	if len(props.Profiles) != 1 {
		t.Fatalf("profiles = %d, want 1", len(props.Profiles))
	}

	profile := props.Profiles[0]
	if profile.Capacity == nil || deref(profile.Capacity.Maximum) != "10" {
		t.Fatalf("capacity max = %v, want 10", profile.Capacity)
	}

	if len(profile.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(profile.Rules))
	}

	rule := profile.Rules[0]
	if rule.MetricTrigger == nil || deref(rule.MetricTrigger.MetricName) != "Percentage CPU" {
		t.Fatalf("metricTrigger dropped: %+v", rule.MetricTrigger)
	}

	if rule.ScaleAction == nil || rule.ScaleAction.Direction == nil ||
		*rule.ScaleAction.Direction != armmonitor.ScaleDirectionIncrease {
		t.Fatalf("scaleAction dropped: %+v", rule.ScaleAction)
	}
}
