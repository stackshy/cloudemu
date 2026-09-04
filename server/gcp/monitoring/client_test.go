package monitoring_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	monitoring "google.golang.org/api/monitoring/v3"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpmonitoring "github.com/stackshy/cloudemu/v2/providers/gcp/monitoring"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// newClient wires the real google.golang.org/api/monitoring/v3 REST client at
// the in-process GCP server, exercising the actual Cloud Monitoring wire shapes.
func newClient(t *testing.T, ts *httptest.Server) *monitoring.Service {
	t.Helper()

	svc, err := monitoring.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return svc
}

// TestTimeSeriesListAutoMetrics guards the BLOCKER: GCE auto-metrics emitted on
// RunInstances must be readable via timeSeries.list (was 501 — the only wire
// read path for every metric).
func TestTimeSeriesListAutoMetrics(t *testing.T) {
	cloudP := cloudemu.NewGCP()

	if _, err := cloudP.GCE.RunInstances(context.Background(),
		computedriver.InstanceConfig{ImageID: "debian-12", InstanceType: "e2-medium"}, 1); err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc := newClient(t, ts)

	resp, err := svc.Projects.TimeSeries.List("projects/p1").
		Filter(`metric.type="compute.googleapis.com/instance/cpu/utilization"`).
		IntervalStartTime(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)).
		IntervalEndTime(time.Now().Add(time.Hour).UTC().Format(time.RFC3339)).
		Do()
	if err != nil {
		t.Fatalf("timeSeries.list: %v", err)
	}

	if len(resp.TimeSeries) == 0 {
		t.Fatal("timeSeries.list returned no series for a launched instance")
	}

	if len(resp.TimeSeries[0].Points) == 0 {
		t.Error("series has no points")
	}

	if got := resp.TimeSeries[0].Metric.Type; got != "compute.googleapis.com/instance/cpu/utilization" {
		t.Errorf("metric.type=%q", got)
	}
}

// TestTimeSeriesCreate guards the BLOCKER: custom-metric ingestion via
// timeSeries.create (was 501), and that the ingested point reads back.
func TestTimeSeriesCreate(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc := newClient(t, ts)

	val := 42.5
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := svc.Projects.TimeSeries.Create("projects/p1", &monitoring.CreateTimeSeriesRequest{
		TimeSeries: []*monitoring.TimeSeries{{
			Metric:   &monitoring.Metric{Type: "custom.googleapis.com/my_metric", Labels: map[string]string{"env": "test"}},
			Resource: &monitoring.MonitoredResource{Type: "global"},
			Points: []*monitoring.Point{{
				Interval: &monitoring.TimeInterval{EndTime: now},
				Value:    &monitoring.TypedValue{DoubleValue: &val},
			}},
		}},
	}).Do()
	if err != nil {
		t.Fatalf("timeSeries.create: %v", err)
	}

	resp, err := svc.Projects.TimeSeries.List("projects/p1").
		Filter(`metric.type="custom.googleapis.com/my_metric"`).
		IntervalStartTime(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)).
		IntervalEndTime(time.Now().Add(time.Hour).UTC().Format(time.RFC3339)).
		Do()
	if err != nil {
		t.Fatalf("timeSeries.list: %v", err)
	}

	if len(resp.TimeSeries) != 1 {
		t.Fatalf("want 1 custom series, got %d", len(resp.TimeSeries))
	}

	pts := resp.TimeSeries[0].Points
	if len(pts) != 1 || pts[0].Value.DoubleValue == nil || *pts[0].Value.DoubleValue != val {
		t.Errorf("ingested point not read back: %+v", pts)
	}
}

// TestMetricDescriptors guards the HIGH finding: metricDescriptors list/get/
// create (was 501).
func TestMetricDescriptors(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc := newClient(t, ts)

	const mtype = "custom.googleapis.com/widgets"

	created, err := svc.Projects.MetricDescriptors.Create("projects/p1", &monitoring.MetricDescriptor{
		Type:       mtype,
		MetricKind: "GAUGE",
		ValueType:  "DOUBLE",
	}).Do()
	if err != nil {
		t.Fatalf("metricDescriptors.create: %v", err)
	}

	if created.Name == "" || created.Type != mtype {
		t.Errorf("create returned name=%q type=%q", created.Name, created.Type)
	}

	got, err := svc.Projects.MetricDescriptors.Get("projects/p1/metricDescriptors/" + mtype).Do()
	if err != nil {
		t.Fatalf("metricDescriptors.get: %v", err)
	}

	if got.Type != mtype {
		t.Errorf("get type=%q", got.Type)
	}

	list, err := svc.Projects.MetricDescriptors.List("projects/p1").Do()
	if err != nil {
		t.Fatalf("metricDescriptors.list: %v", err)
	}

	var found bool

	for _, d := range list.MetricDescriptors {
		if d.Type == mtype {
			found = true
		}
	}

	if !found {
		t.Error("created descriptor missing from list")
	}
}

// TestNotificationChannels guards the HIGH finding: notificationChannels
// create/list/get (was 501) so alert policies can reference real channels.
func TestNotificationChannels(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc := newClient(t, ts)

	created, err := svc.Projects.NotificationChannels.Create("projects/p1", &monitoring.NotificationChannel{
		Type:        "email",
		DisplayName: "oncall",
		Labels:      map[string]string{"email_address": "oncall@example.com"},
	}).Do()
	if err != nil {
		t.Fatalf("notificationChannels.create: %v", err)
	}

	if created.Name == "" {
		t.Fatal("create returned empty channel name")
	}

	got, err := svc.Projects.NotificationChannels.Get(created.Name).Do()
	if err != nil {
		t.Fatalf("notificationChannels.get: %v", err)
	}

	if got.DisplayName != "oncall" {
		t.Errorf("get displayName=%q", got.DisplayName)
	}

	list, err := svc.Projects.NotificationChannels.List("projects/p1").Do()
	if err != nil {
		t.Fatalf("notificationChannels.list: %v", err)
	}

	if len(list.NotificationChannels) != 1 {
		t.Errorf("want 1 channel, got %d", len(list.NotificationChannels))
	}

	// A policy may now reference the channel that actually exists.
	if _, err := svc.Projects.AlertPolicies.Create("projects/p1", &monitoring.AlertPolicy{
		DisplayName:          "cpu",
		Combiner:             "OR",
		NotificationChannels: []string{created.Name},
	}).Do(); err != nil {
		t.Fatalf("alertPolicies.create referencing channel: %v", err)
	}
}

// TestAlertPolicyOpaqueIDAndRecords guards the WRONG_WIRE + records findings:
// two policies with the same displayName must not collapse (opaque numeric id),
// and creationRecord / condition names must be populated.
func TestAlertPolicyOpaqueIDAndRecords(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc := newClient(t, ts)

	mk := func() *monitoring.AlertPolicy {
		return &monitoring.AlertPolicy{
			DisplayName: "high-cpu",
			Combiner:    "OR",
			Conditions:  []*monitoring.Condition{{DisplayName: "cpu>80"}},
		}
	}

	first, err := svc.Projects.AlertPolicies.Create("projects/p1", mk()).Do()
	if err != nil {
		t.Fatalf("create #1: %v", err)
	}

	second, err := svc.Projects.AlertPolicies.Create("projects/p1", mk()).Do()
	if err != nil {
		t.Fatalf("create #2: %v", err)
	}

	if first.Name == second.Name {
		t.Fatalf("duplicate displayName collapsed to one name: %q", first.Name)
	}

	if first.CreationRecord == nil || first.CreationRecord.MutateTime == "" {
		t.Error("creationRecord not populated")
	}

	if len(first.Conditions) != 1 || first.Conditions[0].Name == "" {
		t.Errorf("condition name not populated: %+v", first.Conditions)
	}

	list, err := svc.Projects.AlertPolicies.List("projects/p1").Do()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(list.AlertPolicies) != 2 {
		t.Errorf("want 2 policies (no collapse), got %d", len(list.AlertPolicies))
	}
}

// TestAlertPolicyPatch guards the patch finding: alertPolicies.patch edits a
// policy (was POST/GET/DELETE only), applying only the masked field.
func TestAlertPolicyPatch(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc := newClient(t, ts)

	created, err := svc.Projects.AlertPolicies.Create("projects/p1", &monitoring.AlertPolicy{
		DisplayName: "editable",
		Combiner:    "AND",
		Enabled:     true,
	}).Do()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	patched, err := svc.Projects.AlertPolicies.Patch(created.Name, &monitoring.AlertPolicy{
		Combiner: "OR",
	}).UpdateMask("combiner").Do()
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if patched.Combiner != "OR" {
		t.Errorf("after patch combiner=%q want OR", patched.Combiner)
	}

	if !patched.Enabled {
		t.Error("patch omitting enabled silently disabled the policy")
	}
}

// TestAlertPolicyPatchResyncsChannels guards the patch channel gap: editing a
// policy's notificationChannels via PATCH must re-sync them onto the backing
// alarm's AlarmActions (so a later breach delivers to the new set) while
// preserving the alarm's current state/history; an unrelated patch (displayName)
// must leave the channels untouched.
func TestAlertPolicyPatchResyncsChannels(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc := newClient(t, ts)
	ctx := context.Background()

	const chanA = "projects/p1/notificationChannels/chanA"
	const chanB = "projects/p1/notificationChannels/chanB"

	created, err := svc.Projects.AlertPolicies.Create("projects/p1", &monitoring.AlertPolicy{
		DisplayName:          "editable",
		NotificationChannels: []string{chanA},
	}).Do()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	id := created.Name[strings.LastIndex(created.Name, "/")+1:]

	// #806 create path: notificationChannels wired onto the alarm's AlarmActions.
	if got := alarmActions(t, cloudP.CloudMonitoring, id); !equalStrings(got, []string{chanA}) {
		t.Fatalf("after create AlarmActions=%v want [%s]", got, chanA)
	}

	// Simulate a prior breach so the patch's state/history preservation is provable.
	if err := cloudP.CloudMonitoring.SetAlarmState(ctx, id, "ALARM", "breach"); err != nil {
		t.Fatalf("set alarm state: %v", err)
	}

	histBefore, err := cloudP.CloudMonitoring.GetAlarmHistory(ctx, id, 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}

	// Unrelated patch (displayName only): channels must be untouched.
	if _, err := svc.Projects.AlertPolicies.Patch(created.Name, &monitoring.AlertPolicy{
		DisplayName: "renamed",
	}).UpdateMask("displayName").Do(); err != nil {
		t.Fatalf("patch displayName: %v", err)
	}

	if got := alarmActions(t, cloudP.CloudMonitoring, id); !equalStrings(got, []string{chanA}) {
		t.Fatalf("displayName patch changed AlarmActions to %v want [%s]", got, chanA)
	}

	// Patch notificationChannels to B: re-synced onto the alarm.
	if _, err := svc.Projects.AlertPolicies.Patch(created.Name, &monitoring.AlertPolicy{
		NotificationChannels: []string{chanB},
	}).UpdateMask("notificationChannels").Do(); err != nil {
		t.Fatalf("patch channels: %v", err)
	}

	if got := alarmActions(t, cloudP.CloudMonitoring, id); !equalStrings(got, []string{chanB}) {
		t.Fatalf("after channel patch AlarmActions=%v want [%s]", got, chanB)
	}

	// State and history preserved across the channel patch (no CreateAlarm reset).
	alarms, err := cloudP.CloudMonitoring.DescribeAlarms(ctx, []string{id})
	if err != nil || len(alarms) != 1 {
		t.Fatalf("describe: err=%v n=%d", err, len(alarms))
	}

	if alarms[0].State != "ALARM" {
		t.Errorf("channel patch reset alarm state to %q want ALARM", alarms[0].State)
	}

	histAfter, err := cloudP.CloudMonitoring.GetAlarmHistory(ctx, id, 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}

	if len(histAfter) != len(histBefore) {
		t.Errorf("channel patch changed history len %d -> %d", len(histBefore), len(histAfter))
	}
}

// alarmActions returns the backing alarm's AlarmActions for the given policy id.
func alarmActions(t *testing.T, mon *gcpmonitoring.Mock, id string) []string {
	t.Helper()

	alarms, err := mon.DescribeAlarms(context.Background(), []string{id})
	if err != nil || len(alarms) != 1 {
		t.Fatalf("describe alarm %s: err=%v n=%d", id, err, len(alarms))
	}

	return alarms[0].AlarmActions
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// newMonServer spins the in-process GCP server over a fresh CloudMonitoring mock.
func newMonServer(t *testing.T) (*monitoring.Service, *httptest.Server) {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return newClient(t, ts), ts
}

// TestAlertPolicyEnabledDefaultsToTrue guards the enabled-default divergence: a
// create that omits enabled must read back as enabled:true (Cloud Monitoring
// treats an unset value on write as enabled and always returns the field). The
// bug was `enabled` being a bool with omitempty — an omitted/false value was
// dropped, so an omitting create read back as disabled.
func TestAlertPolicyEnabledDefaultsToTrue(t *testing.T) {
	svc, _ := newMonServer(t)

	created, err := svc.Projects.AlertPolicies.Create("projects/p1", &monitoring.AlertPolicy{
		DisplayName: "no-enabled",
		Combiner:    "OR",
	}).Do()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !created.Enabled {
		t.Error("create omitting enabled read back disabled; want default enabled:true")
	}

	got, err := svc.Projects.AlertPolicies.Get(created.Name).Do()
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if !got.Enabled {
		t.Error("get of an enabled-by-default policy returned enabled:false")
	}
}

// TestAlertPolicyEnabledFalseRoundTrips guards that an explicit enabled:false
// survives create→read as a literal false in the wire JSON (omitempty on a bool
// would drop it, so a disabled policy would read back with no enabled field).
func TestAlertPolicyEnabledFalseRoundTrips(t *testing.T) {
	svc, ts := newMonServer(t)

	created, err := svc.Projects.AlertPolicies.Create("projects/p1", &monitoring.AlertPolicy{
		DisplayName:     "disabled",
		Combiner:        "OR",
		Enabled:         false,
		ForceSendFields: []string{"Enabled"},
	}).Do()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if created.Enabled {
		t.Fatal("explicit enabled:false read back as true")
	}

	// Inspect the raw GET body: enabled must be present and literally false, not
	// omitted, matching real Cloud Monitoring which always returns the field.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v3/"+created.Name, nil)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("raw get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}

	v, ok := raw["enabled"]
	if !ok {
		t.Fatal("enabled field omitted from response; real Cloud Monitoring always returns it")
	}

	if b, _ := v.(bool); b {
		t.Errorf("enabled=%v want false", v)
	}
}

// TestAlertPoliciesListDeterministicOrder guards the list-order divergence:
// alertPolicies.list must return a stable order across calls (creation order),
// not the random map iteration order that reads as perpetual drift to Terraform.
func TestAlertPoliciesListDeterministicOrder(t *testing.T) {
	svc, _ := newMonServer(t)

	const n = 12

	created := make([]string, 0, n)

	for i := 0; i < n; i++ {
		pol, err := svc.Projects.AlertPolicies.Create("projects/p1", &monitoring.AlertPolicy{
			DisplayName: "p",
			Combiner:    "OR",
		}).Do()
		if err != nil {
			t.Fatalf("create #%d: %v", i, err)
		}

		created = append(created, pol.Name)
	}

	first := listPolicyNames(t, svc)

	if len(first) != n {
		t.Fatalf("want %d policies, got %d", n, len(first))
	}

	// Order must equal creation order and be identical across repeated calls.
	for i := range created {
		if first[i] != created[i] {
			t.Fatalf("list[%d]=%s want creation order %s", i, first[i], created[i])
		}
	}

	for call := 0; call < 5; call++ {
		got := listPolicyNames(t, svc)
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("list order not stable on call %d at %d: %s != %s", call, i, got[i], first[i])
			}
		}
	}
}

func listPolicyNames(t *testing.T, svc *monitoring.Service) []string {
	t.Helper()

	list, err := svc.Projects.AlertPolicies.List("projects/p1").Do()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	names := make([]string, 0, len(list.AlertPolicies))
	for _, p := range list.AlertPolicies {
		names = append(names, p.Name)
	}

	return names
}

// TestNotificationChannelsListDeterministicOrder guards that
// notificationChannels.list returns a stable order (sorted by resource name)
// across calls rather than the backing store's random map order.
func TestNotificationChannelsListDeterministicOrder(t *testing.T) {
	svc, _ := newMonServer(t)

	const n = 12

	for i := 0; i < n; i++ {
		if _, err := svc.Projects.NotificationChannels.Create("projects/p1", &monitoring.NotificationChannel{
			Type:        "email",
			DisplayName: "c",
			Labels:      map[string]string{"email_address": "a@example.com"},
		}).Do(); err != nil {
			t.Fatalf("create #%d: %v", i, err)
		}
	}

	first := listChannelNames(t, svc)

	if len(first) != n {
		t.Fatalf("want %d channels, got %d", n, len(first))
	}

	if !sortedAscending(first) {
		t.Errorf("channels not sorted by name: %v", first)
	}

	for call := 0; call < 5; call++ {
		got := listChannelNames(t, svc)
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("channel list order not stable on call %d at %d: %s != %s", call, i, got[i], first[i])
			}
		}
	}
}

func listChannelNames(t *testing.T, svc *monitoring.Service) []string {
	t.Helper()

	list, err := svc.Projects.NotificationChannels.List("projects/p1").Do()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	names := make([]string, 0, len(list.NotificationChannels))
	for _, c := range list.NotificationChannels {
		names = append(names, c.Name)
	}

	return names
}

func sortedAscending(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}

	return true
}
