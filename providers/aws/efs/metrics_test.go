package efs_test

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/efs"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

func sampleCount(t *testing.T, cw *cloudwatch.Mock, name string, dims map[string]string, from, to time.Time) []float64 {
	t.Helper()

	res, err := cw.GetMetricData(context.Background(), mondriver.GetMetricInput{
		Namespace: "AWS/EFS", MetricName: name, Dimensions: dims,
		StartTime: from, EndTime: to, Period: 60, Stat: "SampleCount",
	})
	if err != nil {
		t.Fatalf("GetMetricData %s: %v", name, err)
	}

	return res.Values
}

// TestMetricsPublishedOnTick checks AWS/EFS metrics are published by the
// scheduler, not by API calls: nothing on create, then StorageBytes every 15
// minutes and PermittedThroughput (provisioned only) once a minute, whatever
// the request traffic.
func TestMetricsPublishedOnTick(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := config.NewFakeClock(start)
	opts := config.NewOptions(config.WithClock(clk))
	m := efs.New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{
		CreationToken: "tok", ThroughputMode: driver.ThroughputProvisioned, ProvisionedThroughputInMibps: 10,
	})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	burst, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "burst"})
	if err != nil {
		t.Fatalf("CreateFileSystem bursting: %v", err)
	}

	if names, _ := cw.ListMetrics(ctx, "AWS/EFS"); len(names) != 0 {
		t.Fatalf("metrics published on create: %v", names)
	}

	// Two ticks per minute for 30 minutes, with request traffic in between.
	for i := 0; i < 30; i++ {
		if _, err = m.DescribeFileSystems(ctx, fs.FileSystemID, ""); err != nil {
			t.Fatalf("DescribeFileSystems: %v", err)
		}

		m.Tick(clk.Now())
		m.Tick(clk.Now()) // a second tick in the same minute publishes nothing
		clk.Advance(time.Minute)
	}

	end := clk.Now()
	tput := sampleCount(t, cw, "PermittedThroughput", map[string]string{"FileSystemId": fs.FileSystemID}, start, end)

	if len(tput) != 30 {
		t.Fatalf("PermittedThroughput minutes = %d, want 30", len(tput))
	}

	for _, v := range tput {
		if v != 1 {
			t.Fatalf("PermittedThroughput SampleCount per minute = %v, want 1", tput)
		}
	}

	res, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace: "AWS/EFS", MetricName: "PermittedThroughput",
		Dimensions: map[string]string{"FileSystemId": fs.FileSystemID},
		StartTime:  start, EndTime: start.Add(time.Minute), Period: 60, Stat: "Maximum",
	})
	if err != nil || len(res.Values) != 1 || res.Values[0] != 10*(1<<20) || res.Unit != "Bytes/Second" {
		t.Fatalf("PermittedThroughput = %+v, %v; want 10 MiB/s in Bytes/Second", res, err)
	}

	if got := sampleCount(t, cw, "PermittedThroughput",
		map[string]string{"FileSystemId": burst.FileSystemID}, start, end); len(got) != 0 {
		t.Fatalf("bursting file system published PermittedThroughput: %v", got)
	}

	for _, class := range []string{"Total", "Standard", "IA"} {
		got := sampleCount(t, cw, "StorageBytes",
			map[string]string{"FileSystemId": fs.FileSystemID, "StorageClass": class}, start, end)
		if len(got) != 2 { // minutes 0 and 15
			t.Fatalf("StorageBytes %s samples over 30 min = %v, want 2 (every 15 minutes)", class, got)
		}
	}
}

// TestMetricsSkipCreatingAndDeleting checks a file system reports metrics only
// while it is available.
func TestMetricsSkipCreatingAndDeleting(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAsyncSettle())
	m := efs.New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	fs, err := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "tok"})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	if m.Tick(clk.Now()) {
		t.Fatal("Tick published for a creating file system")
	}

	clk.Advance(time.Minute)

	if !m.Tick(clk.Now()) {
		t.Fatal("Tick published nothing for an available file system")
	}

	if err = m.DeleteFileSystem(ctx, fs.FileSystemID); err != nil {
		t.Fatalf("DeleteFileSystem: %v", err)
	}

	deletedAt := clk.Now()

	clk.Advance(time.Hour)

	// This Tick reaps the file system (a state change) but publishes nothing.
	m.Tick(clk.Now())

	dims := map[string]string{"FileSystemId": fs.FileSystemID, "StorageClass": "Total"}
	if got := sampleCount(t, cw, "StorageBytes", dims, deletedAt.Add(time.Second), clk.Now().Add(time.Minute)); len(got) != 0 {
		t.Fatalf("Tick published for a deleted file system: %v", got)
	}

	if m.Tick(clk.Now()) {
		t.Fatal("Tick reported a change once the file system was gone")
	}
}

func TestAccountPreferencesDefault(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if got, err := m.DescribeAccountPreferences(ctx); err != nil || got != "LONG_ID" {
		t.Fatalf("unset preference = %q, %v; want LONG_ID", got, err)
	}

	for _, bad := range []string{"SHORT_ID", "long_id", "BOGUS", ""} {
		if _, err := m.PutAccountPreferences(ctx, bad); !errors.IsInvalidArgument(err) {
			t.Errorf("PutAccountPreferences(%q): want InvalidArgument, got %v", bad, err)
		}
	}

	if got, err := m.PutAccountPreferences(ctx, "LONG_ID"); err != nil || got != "LONG_ID" {
		t.Fatalf("PutAccountPreferences(LONG_ID) = %q, %v", got, err)
	}
}

// TestProtectionRejectedInReplication checks UpdateFileSystemProtection returns
// ReplicationAlreadyExists for a file system that is the source or the
// destination of a replication configuration.
func TestProtectionRejectedInReplication(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	src, _ := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "src"})
	dst, _ := m.CreateFileSystem(ctx, driver.CreateFileSystemInput{CreationToken: "dst"})

	if _, err := m.CreateReplicationConfiguration(ctx, src.FileSystemID,
		[]driver.DestinationToCreate{{FileSystemID: dst.FileSystemID}}); err != nil {
		t.Fatalf("CreateReplicationConfiguration: %v", err)
	}

	for _, id := range []string{src.FileSystemID, dst.FileSystemID} {
		_, err := m.UpdateFileSystemProtection(ctx, id, driver.ProtectionEnabled)
		if !isReplicationConflict(err) {
			t.Fatalf("UpdateFileSystemProtection(%s) in replication: want ReplicationAlreadyExists, got %v", id, err)
		}
	}

	if err := m.DeleteReplicationConfiguration(ctx, src.FileSystemID); err != nil {
		t.Fatalf("DeleteReplicationConfiguration: %v", err)
	}

	if _, err := m.UpdateFileSystemProtection(ctx, dst.FileSystemID, driver.ProtectionDisabled); err != nil {
		t.Fatalf("UpdateFileSystemProtection after replication removed: %v", err)
	}
}

func isReplicationConflict(err error) bool {
	var re *driver.ResourceError

	return stderrors.As(err, &re) && re.Kind == driver.KindReplication && errors.IsAlreadyExists(err)
}
