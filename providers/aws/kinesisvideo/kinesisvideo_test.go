package kinesisvideo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/kinesisvideo"
	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

func newMock() *kinesisvideo.Mock {
	return kinesisvideo.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func createStream(t *testing.T, m *kinesisvideo.Mock, name string) *driver.StreamInfo {
	t.Helper()

	retention := int32(24)
	out, err := m.CreateStream(context.Background(), &driver.CreateStreamInput{
		StreamName:           name,
		MediaType:            "video/h264",
		DeviceName:           "dev",
		DataRetentionInHours: &retention,
		Tags:                 map[string]string{"a": "1"},
	})
	requireNoError(t, err)

	return out
}

func TestCreateStreamComputedFields(t *testing.T) {
	m := newMock()
	s := createStream(t, m, "cam")

	if s.Status != driver.StatusActive {
		t.Fatalf("status = %q, want ACTIVE", s.Status)
	}

	if !strings.Contains(s.StreamARN, ":kinesisvideo:") || !strings.Contains(s.StreamARN, ":stream/cam/") {
		t.Fatalf("arn = %q, want a kinesisvideo stream arn", s.StreamARN)
	}

	if s.Version == "" || s.CreationTime.IsZero() {
		t.Fatalf("version/creation not set: %+v", s)
	}
}

func TestStreamReadStabilityAndVersionBump(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	created := createStream(t, m, "cam")

	first, err := m.DescribeStream(ctx, driver.StreamRef{StreamName: "cam"})
	requireNoError(t, err)

	second, err := m.DescribeStream(ctx, driver.StreamRef{StreamARN: created.StreamARN})
	requireNoError(t, err)

	if first.StreamARN != second.StreamARN || first.Version != second.Version || !first.CreationTime.Equal(second.CreationTime) {
		t.Fatal("computed fields drifted across reads")
	}

	requireNoError(t, m.UpdateStream(ctx, &driver.UpdateStreamInput{
		StreamName:     "cam",
		CurrentVersion: first.Version,
		DeviceName:     "dev2",
	}))

	after, err := m.DescribeStream(ctx, driver.StreamRef{StreamName: "cam"})
	requireNoError(t, err)

	if after.Version == first.Version {
		t.Fatal("version did not change after update")
	}

	if after.StreamARN != first.StreamARN || !after.CreationTime.Equal(first.CreationTime) {
		t.Fatal("arn/creation drifted after update")
	}

	if after.DeviceName != "dev2" {
		t.Fatalf("deviceName = %q, want dev2", after.DeviceName)
	}
}

func TestUpdateDataRetention(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	s := createStream(t, m, "cam")

	requireNoError(t, m.UpdateDataRetention(ctx, &driver.UpdateDataRetentionInput{
		StreamName:                 "cam",
		CurrentVersion:             s.Version,
		Operation:                  driver.OperationIncreaseDataRetention,
		DataRetentionChangeInHours: 24,
	}))

	got, err := m.DescribeStream(ctx, driver.StreamRef{StreamName: "cam"})
	requireNoError(t, err)

	if got.DataRetentionInHours != 48 {
		t.Fatalf("retention = %d, want 48", got.DataRetentionInHours)
	}
}

func TestCreateDuplicateStreamInUse(t *testing.T) {
	m := newMock()
	createStream(t, m, "cam")

	_, err := m.CreateStream(context.Background(), &driver.CreateStreamInput{StreamName: "cam"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExResourceInUse {
		t.Fatalf("duplicate create: got %v, want ResourceInUse", err)
	}
}

func TestDeleteVersionGuardAndNotFound(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	s := createStream(t, m, "cam")

	if err := m.DeleteStream(ctx, driver.StreamRef{StreamName: "cam"}, "wrong-version"); err == nil {
		t.Fatal("delete with wrong version: want error")
	}

	requireNoError(t, m.DeleteStream(ctx, driver.StreamRef{StreamName: "cam"}, s.Version))

	_, err := m.DescribeStream(ctx, driver.StreamRef{StreamName: "cam"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExResourceNotFound {
		t.Fatalf("describe after delete: got %v, want ResourceNotFound", err)
	}
}

func TestStreamTags(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	createStream(t, m, "cam")

	ref := driver.StreamRef{StreamName: "cam"}
	requireNoError(t, m.TagStream(ctx, ref, map[string]string{"b": "2"}))

	tags, _, err := m.ListTagsForStream(ctx, ref, "")
	requireNoError(t, err)

	if tags["a"] != "1" || tags["b"] != "2" {
		t.Fatalf("tags = %v, want a=1 b=2", tags)
	}

	requireNoError(t, m.UntagStream(ctx, ref, []string{"a"}))

	tags, _, err = m.ListTagsForStream(ctx, ref, "")
	requireNoError(t, err)

	if _, ok := tags["a"]; ok {
		t.Fatalf("tag a still present after untag: %v", tags)
	}
}

func TestChannelLifecycleAndResourceTags(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	ch, err := m.CreateSignalingChannel(ctx, &driver.CreateChannelInput{
		ChannelName: "sig",
		Tags:        []driver.Tag{{Key: "env", Value: "test"}},
	})
	requireNoError(t, err)

	if ch.ChannelType != driver.ChannelTypeSingleMaster || ch.MessageTTLSeconds != 60 {
		t.Fatalf("channel defaults wrong: %+v", ch)
	}

	requireNoError(t, m.TagResource(ctx, ch.ChannelARN, map[string]string{"team": "video"}))

	tags, _, err := m.ListTagsForResource(ctx, ch.ChannelARN, "")
	requireNoError(t, err)

	if tags["env"] != "test" || tags["team"] != "video" {
		t.Fatalf("resource tags = %v", tags)
	}

	requireNoError(t, m.DeleteSignalingChannel(ctx, ch.ChannelARN, ch.Version))

	_, err = m.DescribeSignalingChannel(ctx, driver.ChannelRef{ChannelName: "sig"})
	if err == nil {
		t.Fatal("describe after channel delete: want error")
	}
}

func TestListStreamsFilterAndPaginate(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, n := range []string{"prod-1", "prod-2", "dev-1"} {
		createStream(t, m, n)
	}

	items, _, err := m.ListStreams(ctx, driver.Page{NameBeginsWith: "prod-"})
	requireNoError(t, err)

	if len(items) != 2 {
		t.Fatalf("BEGINS_WITH prod- returned %d, want 2", len(items))
	}

	page1, next, err := m.ListStreams(ctx, driver.Page{MaxResults: 1})
	requireNoError(t, err)

	if len(page1) != 1 || next == "" {
		t.Fatalf("page1 = %d items, next = %q, want 1 item + next", len(page1), next)
	}
}

func TestSnapshotRestoreIdentity(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	s := createStream(t, m, "cam")
	_, err := m.CreateSignalingChannel(ctx, &driver.CreateChannelInput{ChannelName: "sig"})
	requireNoError(t, err)

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, data))

	got, err := restored.DescribeStream(ctx, driver.StreamRef{StreamName: "cam"})
	requireNoError(t, err)

	if got.StreamARN != s.StreamARN || got.Version != s.Version || !got.CreationTime.Equal(s.CreationTime) {
		t.Fatal("restore did not preserve stream identity")
	}

	if _, err = restored.DescribeSignalingChannel(ctx, driver.ChannelRef{ChannelName: "sig"}); err != nil {
		t.Fatalf("restore lost channel: %v", err)
	}
}
