package monitor_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const memMet = "Available Memory Bytes"

// A metric alert with two allOf criteria fires only when both are met.
// Microsoft Learn: the rule "fires an alert when all conditions are met".
// Before, only allOf[0] was registered, so the CPU breach alone fired it.
func TestSDKMultiCriteriaAlertNeedsAllCriteria(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cloudP := cloudemu.NewAzure(config.WithClock(clock))

	srv := azureserver.New(azureserver.Drivers{Monitor: cloudP.Monitor})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ensureRG(t, ts, "sub-1", "rg-1")

	ctx := context.Background()

	agClient, err := armmonitor.NewActionGroupsClient("sub-1", fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := agClient.CreateOrUpdate(ctx, "rg-1", "ops-ag", armmonitor.ActionGroupResource{
		Location: to.Ptr("global"),
		Properties: &armmonitor.ActionGroup{
			GroupShortName: to.Ptr("ops"),
			Enabled:        to.Ptr(true),
			EmailReceivers: []*armmonitor.EmailReceiver{{Name: to.Ptr("oncall"), EmailAddress: to.Ptr("oncall@example.com")}},
		},
	}, nil); err != nil {
		t.Fatalf("action group CreateOrUpdate: %v", err)
	}

	const agID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/microsoft.insights/actionGroups/ops-ag"

	alert := cpuAlert(vm1URI, agID, 50)
	crit := alert.Properties.Criteria.(*armmonitor.MetricAlertSingleResourceMultipleMetricCriteria)
	crit.AllOf = append(crit.AllOf, &armmonitor.MetricCriteria{
		Name:            to.Ptr("mem"),
		MetricName:      to.Ptr(memMet),
		MetricNamespace: to.Ptr(cpuNS),
		Operator:        to.Ptr(armmonitor.OperatorLessThan),
		Threshold:       to.Ptr(float64(100)),
		TimeAggregation: to.Ptr(armmonitor.AggregationTypeEnumAverage),
	})

	alertClient, err := armmonitor.NewMetricAlertsClient("sub-1", fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := alertClient.CreateOrUpdate(ctx, "rg-1", "cpu-and-mem", alert, nil); err != nil {
		t.Fatalf("metric alert CreateOrUpdate: %v", err)
	}

	pushCPU(t, cloudP.Monitor, clock, vm1URI, 95)

	if n := len(cloudP.Monitor.ActionGroupDeliveries()); n != 0 {
		t.Fatalf("deliveries after CPU breach only = %d, want 0", n)
	}

	if err := cloudP.Monitor.PutMetricData(ctx, []mondriver.MetricDatum{{
		Namespace: cpuNS, MetricName: memMet, Value: 10, Timestamp: clock.Now(),
		Dimensions: map[string]string{"resourceId": vm1URI},
	}}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	deliveries := cloudP.Monitor.ActionGroupDeliveries()
	if len(deliveries) != 1 || deliveries[0].AlarmName != "cpu-and-mem" || deliveries[0].NewState != "ALARM" {
		t.Fatalf("deliveries after both breach = %+v, want one ALARM delivery", deliveries)
	}
}
