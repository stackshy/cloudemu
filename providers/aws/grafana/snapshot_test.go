package grafana_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/grafana"
)

func TestSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	w := createWS(t, src)

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst := grafana.New(config.NewOptions())
	requireNoError(t, dst.Restore(ctx, data))

	got, err := dst.DescribeWorkspace(ctx, w.ID)
	requireNoError(t, err)

	if got.ID != w.ID || got.Arn != w.Arn || got.Endpoint != w.Endpoint ||
		got.Name != w.Name || got.GrafanaVersion != w.GrafanaVersion {
		t.Fatal("restored workspace does not match original identity")
	}

	tags, err := dst.ListTagsForResource(ctx, w.Arn)
	requireNoError(t, err)

	if tags["a"] != "1" {
		t.Fatalf("restored tags = %v, want a=1", tags)
	}
}

func TestSnapshotEmpty(t *testing.T) {
	ctx := context.Background()

	data, err := newMock().Snapshot(ctx, false)
	requireNoError(t, err)

	if err := grafana.New(config.NewOptions()).Restore(ctx, data); err != nil {
		t.Fatalf("restore empty snapshot: %v", err)
	}
}
