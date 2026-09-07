package streamanalytics_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/streamanalytics"
)

func newMock() *streamanalytics.Mock {
	return streamanalytics.New(config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(1_700_000_000, 0)))))
}

func iptr(v int) *int       { return &v }
func sptr(v string) *string { return &v }

func createJob(t *testing.T, m *streamanalytics.Mock) streamanalytics.StreamingJob {
	t.Helper()

	in := &streamanalytics.JobInput{
		Tags:                              map[string]string{"env": "dev"},
		EventsOutOfOrderPolicy:            sptr("Drop"),
		OutputErrorPolicy:                 sptr("Drop"),
		EventsOutOfOrderMaxDelayInSeconds: iptr(0),
	}

	j, isNew, err := m.CreateOrUpdateJob(context.Background(), "sub", "rg", "job1", "West US", in)
	if err != nil || !isNew {
		t.Fatalf("create job: err=%v isNew=%v", err, isNew)
	}

	return j
}

func TestCreateJobComputedFields(t *testing.T) {
	m := newMock()
	j := createJob(t, m)

	if j.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", j.ProvisioningState)
	}

	if j.JobState != streamanalytics.JobStateCreated {
		t.Errorf("jobState = %q, want Created", j.JobState)
	}

	if j.JobID == "" || j.Etag == "" {
		t.Errorf("jobId/etag not minted: %q / %q", j.JobID, j.Etag)
	}

	if j.SkuName != "Standard" || j.CompatibilityLevel != "1.0" || j.DataLocale != "en-US" {
		t.Errorf("defaults wrong: sku=%q compat=%q locale=%q", j.SkuName, j.CompatibilityLevel, j.DataLocale)
	}

	if j.EventsOutOfOrderMaxDelayInSeconds == nil || *j.EventsOutOfOrderMaxDelayInSeconds != 0 {
		t.Errorf("eventsOutOfOrderMaxDelayInSeconds should round-trip 0, got %v", j.EventsOutOfOrderMaxDelayInSeconds)
	}
}

func TestComputedFieldsStableAcrossReadsAndPatch(t *testing.T) {
	m := newMock()
	created := createJob(t, m)

	got1, err := m.GetJob(context.Background(), "sub", "rg", "job1")
	requireNoErr(t, err)

	got2, err := m.GetJob(context.Background(), "sub", "rg", "job1")
	requireNoErr(t, err)

	if got1.JobID != created.JobID || got1.Etag != created.Etag || got1.CreatedDate != created.CreatedDate {
		t.Errorf("computed fields drifted on read")
	}

	if got1.JobID != got2.JobID || got1.Etag != got2.Etag || got1.CreatedDate != got2.CreatedDate ||
		got1.JobState != got2.JobState {
		t.Errorf("two GETs differ on computed fields")
	}

	// A PATCH that changes a tag must not perturb the minted fields.
	patched, _, err := m.CreateOrUpdateJob(context.Background(), "sub", "rg", "job1", "West US",
		&streamanalytics.JobInput{Tags: map[string]string{"env": "prod"}})
	requireNoErr(t, err)

	if patched.JobID != created.JobID || patched.Etag != created.Etag ||
		patched.CreatedDate != created.CreatedDate {
		t.Errorf("computed fields drifted on patch: jobId %q->%q etag %q->%q date %q->%q",
			created.JobID, patched.JobID, created.Etag, patched.Etag, created.CreatedDate, patched.CreatedDate)
	}

	if patched.Tags["env"] != "prod" {
		t.Errorf("tag replace failed: %v", patched.Tags)
	}
}

func TestStartStopStateMachine(t *testing.T) {
	m := newMock()
	createJob(t, m)
	ctx := context.Background()

	started, err := m.StartJob(ctx, "sub", "rg", "job1", "JobStartTime", "")
	requireNoErr(t, err)

	if started.JobState != streamanalytics.JobStateRunning {
		t.Fatalf("after start jobState = %q, want Running", started.JobState)
	}

	if started.OutputStartMode != "JobStartTime" {
		t.Errorf("outputStartMode = %q, want JobStartTime", started.OutputStartMode)
	}

	stopped, err := m.StopJob(ctx, "sub", "rg", "job1")
	requireNoErr(t, err)

	if stopped.JobState != streamanalytics.JobStateStopped {
		t.Fatalf("after stop jobState = %q, want Stopped", stopped.JobState)
	}
}

func TestIllegalTransitionsRejected(t *testing.T) {
	m := newMock()
	createJob(t, m)
	ctx := context.Background()

	// Stopping a freshly-created (never started) job is illegal.
	if _, err := m.StopJob(ctx, "sub", "rg", "job1"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("stop of Created job: err=%v, want FailedPrecondition", err)
	}

	if _, err := m.StartJob(ctx, "sub", "rg", "job1", "JobStartTime", ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Starting an already-running job is illegal.
	if _, err := m.StartJob(ctx, "sub", "rg", "job1", "JobStartTime", ""); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("double start: err=%v, want FailedPrecondition", err)
	}

	if _, err := m.StopJob(ctx, "sub", "rg", "job1"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Stopping an already-stopped job is illegal.
	if _, err := m.StopJob(ctx, "sub", "rg", "job1"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("double stop: err=%v, want FailedPrecondition", err)
	}
}

func TestScaleUpdatesTransformationStreamingUnits(t *testing.T) {
	m := newMock()
	createJob(t, m)
	ctx := context.Background()

	_, _, err := m.CreateOrUpdateChild(ctx, "sub", "rg", "job1", streamanalytics.KindTransformations,
		"main", json.RawMessage(`{"streamingUnits":1,"query":"SELECT * INTO o FROM i"}`))
	requireNoErr(t, err)

	if _, err = m.StartJob(ctx, "sub", "rg", "job1", "JobStartTime", ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, err = m.ScaleJob(ctx, "sub", "rg", "job1", iptr(6)); err != nil {
		t.Fatalf("scale: %v", err)
	}

	tr, err := m.GetChild(ctx, "sub", "rg", "job1", streamanalytics.KindTransformations, "main")
	requireNoErr(t, err)

	var props map[string]any
	if err = json.Unmarshal(tr.Properties, &props); err != nil {
		t.Fatalf("unmarshal transformation: %v", err)
	}

	if props["streamingUnits"] != float64(6) {
		t.Errorf("streamingUnits = %v, want 6", props["streamingUnits"])
	}

	if props["query"] != "SELECT * INTO o FROM i" {
		t.Errorf("query not preserved: %v", props["query"])
	}
}

func TestChildDatasourceRoundTripsVerbatim(t *testing.T) {
	m := newMock()
	createJob(t, m)
	ctx := context.Background()

	raw := json.RawMessage(`{"type":"Stream","datasource":{"type":"Microsoft.ServiceBus/EventHub",` +
		`"properties":{"eventHubName":"eh","serviceBusNamespace":"ns"}},"serialization":{"type":"Json"}}`)

	c, isNew, err := m.CreateOrUpdateChild(ctx, "sub", "rg", "job1", streamanalytics.KindInputs, "in1", raw)
	requireNoErr(t, err)

	if !isNew {
		t.Fatalf("expected new child")
	}

	if c.ARMType() != "Microsoft.StreamAnalytics/streamingjobs/inputs" {
		t.Errorf("child type = %q", c.ARMType())
	}

	var want, got map[string]any
	_ = json.Unmarshal(raw, &want)
	_ = json.Unmarshal(c.Properties, &got)

	ds, _ := got["datasource"].(map[string]any)
	if ds["type"] != "Microsoft.ServiceBus/EventHub" {
		t.Errorf("datasource type discriminator not preserved: %v", got["datasource"])
	}
}

func TestTestChildStatus(t *testing.T) {
	m := newMock()
	createJob(t, m)
	ctx := context.Background()

	_, _, err := m.CreateOrUpdateChild(ctx, "sub", "rg", "job1", streamanalytics.KindOutputs, "out1",
		json.RawMessage(`{"datasource":{"type":"Microsoft.Storage/Blob"}}`))
	requireNoErr(t, err)

	status, err := m.TestChild(ctx, "sub", "rg", "job1", streamanalytics.KindOutputs, "out1")
	requireNoErr(t, err)

	if status != streamanalytics.TestSucceeded {
		t.Errorf("test status = %q, want TestSucceeded", status)
	}

	if _, err = m.TestChild(ctx, "sub", "rg", "job1", streamanalytics.KindOutputs, "missing"); !cerrors.IsNotFound(err) {
		t.Errorf("test of missing output: err=%v, want NotFound", err)
	}
}

func TestChildRequiresParentJob(t *testing.T) {
	m := newMock()

	_, _, err := m.CreateOrUpdateChild(context.Background(), "sub", "rg", "ghost",
		streamanalytics.KindInputs, "in1", json.RawMessage(`{}`))
	if !cerrors.IsNotFound(err) {
		t.Errorf("child under missing job: err=%v, want NotFound", err)
	}
}

func TestDeleteJobCascadesChildren(t *testing.T) {
	m := newMock()
	createJob(t, m)
	ctx := context.Background()

	for _, kind := range []string{streamanalytics.KindInputs, streamanalytics.KindOutputs} {
		_, _, err := m.CreateOrUpdateChild(ctx, "sub", "rg", "job1", kind, "c1", json.RawMessage(`{}`))
		requireNoErr(t, err)
	}

	existed, err := m.DeleteJob(ctx, "sub", "rg", "job1")
	requireNoErr(t, err)

	if !existed {
		t.Fatalf("job should have existed")
	}

	if _, err = m.GetChild(ctx, "sub", "rg", "job1", streamanalytics.KindInputs, "c1"); !cerrors.IsNotFound(err) {
		t.Errorf("child survived job delete: %v", err)
	}
}

func TestPurgeResourceGroupSparesSiblings(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.CreateOrUpdateJob(ctx, "sub", "rg1", "j1", "West US", &streamanalytics.JobInput{}); err != nil {
		t.Fatalf("create j1: %v", err)
	}

	if _, _, err := m.CreateOrUpdateJob(ctx, "sub", "rg2", "j2", "West US", &streamanalytics.JobInput{}); err != nil {
		t.Fatalf("create j2: %v", err)
	}

	if _, _, err := m.CreateOrUpdateChild(ctx, "sub", "rg1", "j1",
		streamanalytics.KindInputs, "in", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("create child: %v", err)
	}

	requireNoErr(t, m.PurgeResourceGroup(ctx, "sub", "rg1"))

	if m.JobExists(ctx, "sub", "rg1", "j1") {
		t.Errorf("rg1 job survived purge")
	}

	if !m.JobExists(ctx, "sub", "rg2", "j2") {
		t.Errorf("rg2 job wrongly purged")
	}
}

func TestListJobsByScope(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, name := range []string{"b", "a", "c"} {
		if _, _, err := m.CreateOrUpdateJob(ctx, "sub", "rg", name, "West US", &streamanalytics.JobInput{}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	byRG, err := m.ListJobsByResourceGroup(ctx, "sub", "rg")
	requireNoErr(t, err)

	if len(byRG) != 3 || byRG[0].Name != "a" || byRG[2].Name != "c" {
		t.Errorf("list-by-rg not sorted: %+v", byRG)
	}

	bySub, err := m.ListJobsBySubscription(ctx, "sub")
	requireNoErr(t, err)

	if len(bySub) != 3 {
		t.Errorf("list-by-sub count = %d, want 3", len(bySub))
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock()
	createJob(t, m)
	ctx := context.Background()

	if _, _, err := m.CreateOrUpdateChild(ctx, "sub", "rg", "job1",
		streamanalytics.KindInputs, "in1", json.RawMessage(`{"type":"Stream"}`)); err != nil {
		t.Fatalf("child: %v", err)
	}

	data, err := m.Snapshot(ctx, false)
	requireNoErr(t, err)

	restored := streamanalytics.New(config.NewOptions())
	requireNoErr(t, restored.Restore(ctx, data))

	j, err := restored.GetJob(ctx, "sub", "rg", "job1")
	requireNoErr(t, err)

	if j.JobID == "" {
		t.Errorf("restored job missing jobId")
	}

	if _, err = restored.GetChild(ctx, "sub", "rg", "job1", streamanalytics.KindInputs, "in1"); err != nil {
		t.Errorf("restored child missing: %v", err)
	}
}

func requireNoErr(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
