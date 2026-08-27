package monitor_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azmonitor "github.com/stackshy/cloudemu/v2/providers/azure/monitor"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// webhookPoster is a WebhookDeliverer that performs the real HTTP POST to a
// webhook receiver, so the firing test can assert an action group's webhook
// receiver was actually hit end-to-end.
type webhookPoster struct{}

func (webhookPoster) Deliver(ctx context.Context, uri, _ string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri, http.NoBody)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	return resp.Body.Close()
}

const (
	vm1URI = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm1"
	vm2URI = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm2"
	cpuNS  = "Microsoft.Compute/virtualMachines"
	cpuMet = "Percentage CPU"
)

// TestSDKAlarmBreachFiresActionGroup is the load-bearing regression for the
// action-group firing finding: an OK->ALARM transition must resolve the alert's
// AlarmActions ids against the registered action groups and deliver to each
// receiver (recorded, and — for webhook receivers — POSTed for real). Both the
// action group and the alert are created through the real armmonitor SDK; the
// breach is driven by pushing a datapoint at the fake clock's now.
func TestSDKAlarmBreachFiresActionGroup(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cloudP := cloudemu.NewAzure(config.WithClock(clock))
	cloudP.Monitor.SetWebhookDeliverer(webhookPoster{})

	srv := azureserver.New(azureserver.Drivers{Monitor: cloudP.Monitor})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	var webhookHits int32

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&webhookHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(webhookSrv.Close)

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
			EmailReceivers: []*armmonitor.EmailReceiver{
				{Name: to.Ptr("oncall"), EmailAddress: to.Ptr("oncall@example.com")},
			},
			WebhookReceivers: []*armmonitor.WebhookReceiver{
				{Name: to.Ptr("hook"), ServiceURI: to.Ptr(webhookSrv.URL)},
			},
		},
	}, nil); err != nil {
		t.Fatalf("action group CreateOrUpdate: %v", err)
	}

	const agID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/microsoft.insights/actionGroups/ops-ag"

	alertClient, err := armmonitor.NewMetricAlertsClient("sub-1", fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := alertClient.CreateOrUpdate(ctx, "rg-1", "cpu-hot", cpuAlert(vm1URI, agID, 50), nil); err != nil {
		t.Fatalf("metric alert CreateOrUpdate: %v", err)
	}

	// A breaching datapoint for vm1 drives the alarm OK->ALARM, which fires the
	// action group.
	pushCPU(t, cloudP.Monitor, clock, vm1URI, 95)

	deliveries := cloudP.Monitor.ActionGroupDeliveries()
	if len(deliveries) != 2 {
		t.Fatalf("deliveries = %d, want 2 (email + webhook); %+v", len(deliveries), deliveries)
	}

	types := map[string]bool{}
	for _, d := range deliveries {
		if d.AlarmName != "cpu-hot" || d.NewState != "ALARM" {
			t.Fatalf("delivery = %+v, want alarm cpu-hot -> ALARM", d)
		}

		types[d.ReceiverType] = true
	}

	if !types["email"] || !types["webhook"] {
		t.Fatalf("delivered receiver types = %v, want email+webhook", types)
	}

	if got := atomic.LoadInt32(&webhookHits); got != 1 {
		t.Fatalf("webhook receiver hits = %d, want 1 (webhook not delivered)", got)
	}
}

// TestSDKDimensionScopedAlertIgnoresOtherResource covers GAP-B: an alert scoped
// to vm1 must not fire on vm2's datapoints, even though both share the same
// namespace/metric. Only vm1's breaching datapoint drives it to ALARM.
func TestSDKDimensionScopedAlertIgnoresOtherResource(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cloudP := cloudemu.NewAzure(config.WithClock(clock))

	srv := azureserver.New(azureserver.Drivers{Monitor: cloudP.Monitor})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	alertClient, err := armmonitor.NewMetricAlertsClient("sub-1", fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := alertClient.CreateOrUpdate(ctx, "rg-1", "vm1-cpu", cpuAlert(vm1URI, "", 50), nil); err != nil {
		t.Fatalf("metric alert CreateOrUpdate: %v", err)
	}

	// vm2 breaches, but the alert is scoped to vm1 — it must stay out of ALARM.
	pushCPU(t, cloudP.Monitor, clock, vm2URI, 95)

	if state := alarmState(t, cloudP.Monitor, "vm1-cpu"); state == "ALARM" {
		t.Fatalf("alert fired on another resource's datapoint (state=%s)", state)
	}

	// vm1 breaches — now it must fire.
	pushCPU(t, cloudP.Monitor, clock, vm1URI, 95)

	if state := alarmState(t, cloudP.Monitor, "vm1-cpu"); state != "ALARM" {
		t.Fatalf("alert did not fire on its own resource's datapoint (state=%s)", state)
	}
}

// cpuAlert builds a single-resource Percentage CPU alert scoped to scopeURI,
// firing above threshold; when agID is non-empty it links that action group.
func cpuAlert(scopeURI, agID string, threshold float64) armmonitor.MetricAlertResource {
	props := &armmonitor.MetricAlertProperties{
		Severity:            to.Ptr[int32](3),
		Enabled:             to.Ptr(true),
		Scopes:              []*string{to.Ptr(scopeURI)},
		EvaluationFrequency: to.Ptr("PT1M"),
		WindowSize:          to.Ptr("PT5M"),
		Criteria: &armmonitor.MetricAlertSingleResourceMultipleMetricCriteria{
			AllOf: []*armmonitor.MetricCriteria{{
				Name:            to.Ptr("cpu"),
				MetricName:      to.Ptr(cpuMet),
				MetricNamespace: to.Ptr(cpuNS),
				Operator:        to.Ptr(armmonitor.OperatorGreaterThan),
				Threshold:       to.Ptr(threshold),
				TimeAggregation: to.Ptr(armmonitor.AggregationTypeEnumAverage),
			}},
		},
	}

	if agID != "" {
		props.Actions = []*armmonitor.MetricAlertAction{{ActionGroupID: to.Ptr(agID)}}
	}

	return armmonitor.MetricAlertResource{Location: to.Ptr("global"), Properties: props}
}

// pushCPU publishes one Percentage CPU datapoint for resourceURI at the clock's
// now, so it lands inside the alarm's evaluation window.
func pushCPU(t *testing.T, mon *azmonitor.Mock, clock *config.FakeClock, resourceURI string, value float64) {
	t.Helper()

	if err := mon.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace:  cpuNS,
		MetricName: cpuMet,
		Value:      value,
		Timestamp:  clock.Now(),
		Dimensions: map[string]string{"resourceId": resourceURI},
	}}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}
}

// createWebhookAlert creates, over the real armmonitor SDK, an action group with
// a single webhook receiver at webhookURL and a Percentage CPU alert (threshold
// 50) scoped to vm1 that links it. It returns the action group id.
func createWebhookAlert(t *testing.T, ts *httptest.Server, webhookURL string) string {
	t.Helper()

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
			WebhookReceivers: []*armmonitor.WebhookReceiver{
				{Name: to.Ptr("hook"), ServiceURI: to.Ptr(webhookURL)},
			},
		},
	}, nil); err != nil {
		t.Fatalf("action group CreateOrUpdate: %v", err)
	}

	const agID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/microsoft.insights/actionGroups/ops-ag"

	alertClient, err := armmonitor.NewMetricAlertsClient("sub-1", fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := alertClient.CreateOrUpdate(ctx, "rg-1", "cpu-hot", cpuAlert(vm1URI, agID, 50), nil); err != nil {
		t.Fatalf("metric alert CreateOrUpdate: %v", err)
	}

	return agID
}

// TestSDKAlarmBreachDefaultDelivererPOSTsWebhook is the load-bearing regression
// for the "webhook never delivered in production" finding: with NO deliverer
// injected, the real-HTTP default installed by monitor.New must POST the alert
// payload to an action group's webhook receiver on an OK->ALARM breach. The
// action group and the alert are created through the real armmonitor SDK; only
// the metric datapoint is injected via the driver, because Azure custom-metric
// ingestion has no ARM wire path (unlike CloudWatch PutMetricData).
func TestSDKAlarmBreachDefaultDelivererPOSTsWebhook(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cloudP := cloudemu.NewAzure(config.WithClock(clock))
	// Deliberately no SetWebhookDeliverer: the production default must deliver.

	srv := azureserver.New(azureserver.Drivers{Monitor: cloudP.Monitor})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	var (
		hits int32
		body atomic.Value
	)

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body.Store(string(b))
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(webhookSrv.Close)

	createWebhookAlert(t, ts, webhookSrv.URL)

	// Delivery is synchronous inside PutMetricData -> fireActionGroups, so the
	// receiver is hit before PutMetricData returns.
	pushCPU(t, cloudP.Monitor, clock, vm1URI, 95)

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("webhook receiver hits = %d, want 1 (default deliverer did not POST)", got)
	}

	payload, _ := body.Load().(string)
	if !strings.Contains(payload, `"alertName":"cpu-hot"`) || !strings.Contains(payload, `"newState":"ALARM"`) {
		t.Fatalf("webhook payload = %q, want alertName cpu-hot / newState ALARM", payload)
	}
}

// TestSDKAlarmBreachUnreachableWebhookBestEffort covers the best-effort
// contract: an unreachable webhook endpoint must not fail the breach.
// PutMetricData returns no error, the alarm still transitions to ALARM, and the
// delivery is still recorded even though the POST could not complete.
func TestSDKAlarmBreachUnreachableWebhookBestEffort(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cloudP := cloudemu.NewAzure(config.WithClock(clock))

	srv := azureserver.New(azureserver.Drivers{Monitor: cloudP.Monitor})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	// Close the receiver immediately so its URL refuses connections — a fast,
	// deterministic stand-in for an unreachable webhook endpoint.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	createWebhookAlert(t, ts, deadURL)

	// pushCPU fatals if PutMetricData returns an error, so a surfaced delivery
	// error would fail here — the breach must succeed regardless.
	pushCPU(t, cloudP.Monitor, clock, vm1URI, 95)

	if state := alarmState(t, cloudP.Monitor, "cpu-hot"); state != "ALARM" {
		t.Fatalf("alarm state = %s, want ALARM (breach must succeed despite a dead webhook)", state)
	}

	deliveries := cloudP.Monitor.ActionGroupDeliveries()
	if len(deliveries) != 1 || deliveries[0].ReceiverType != "webhook" {
		t.Fatalf("deliveries = %+v, want 1 recorded webhook delivery", deliveries)
	}
}

// alarmState returns the current state of the named alarm.
func alarmState(t *testing.T, mon *azmonitor.Mock, name string) string {
	t.Helper()

	alarms, err := mon.DescribeAlarms(context.Background(), []string{name})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}

	if len(alarms) != 1 {
		t.Fatalf("alarms = %d, want 1", len(alarms))
	}

	return alarms[0].State
}
