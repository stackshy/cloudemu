package cloudwatch

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

func validStreamConfig(name string) driver.MetricStreamConfig {
	return driver.MetricStreamConfig{
		Name:         name,
		FirehoseARN:  "arn:aws:firehose:us-east-1:123456789098:deliverystream/MyFirehose",
		RoleARN:      "arn:aws:iam::123456789098:role/MyFirehoseWriteAccessRole",
		OutputFormat: "json",
		IncludeFilters: []driver.MetricStreamFilter{
			{Namespace: "AWS/EC2"},
		},
	}
}

func TestPutGetMetricStream(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	t.Run("put valid then get", func(t *testing.T) {
		arn, err := m.PutMetricStream(ctx, validStreamConfig("my-stream"))
		requireNoError(t, err)
		if arn == "" {
			t.Fatal("expected a non-empty ARN")
		}

		s, err := m.GetMetricStream(ctx, "my-stream")
		requireNoError(t, err)
		assertEqual(t, "my-stream", s.Name)
		assertEqual(t, arn, s.ARN)
		assertEqual(t, "arn:aws:firehose:us-east-1:123456789098:deliverystream/MyFirehose", s.FirehoseARN)
		assertEqual(t, "json", s.OutputFormat)
		assertEqual(t, "running", s.State)
		assertEqual(t, 1, len(s.IncludeFilters))
		if s.CreationDate.IsZero() {
			t.Fatal("expected a non-zero CreationDate")
		}
	})

	t.Run("missing name is rejected", func(t *testing.T) {
		cfg := validStreamConfig("")
		_, err := m.PutMetricStream(ctx, cfg)
		assertError(t, err, true)
	})

	t.Run("missing FirehoseArn is rejected", func(t *testing.T) {
		cfg := validStreamConfig("no-firehose")
		cfg.FirehoseARN = ""
		_, err := m.PutMetricStream(ctx, cfg)
		assertError(t, err, true)
	})

	t.Run("missing RoleArn is rejected", func(t *testing.T) {
		cfg := validStreamConfig("no-role")
		cfg.RoleARN = ""
		_, err := m.PutMetricStream(ctx, cfg)
		assertError(t, err, true)
	})

	t.Run("invalid OutputFormat is rejected", func(t *testing.T) {
		cfg := validStreamConfig("bad-format")
		cfg.OutputFormat = "csv"
		_, err := m.PutMetricStream(ctx, cfg)
		assertError(t, err, true)
	})

	t.Run("include and exclude filters together is rejected", func(t *testing.T) {
		cfg := validStreamConfig("both-filters")
		cfg.ExcludeFilters = []driver.MetricStreamFilter{{Namespace: "AWS/ELB"}}
		_, err := m.PutMetricStream(ctx, cfg)
		assertError(t, err, true)
	})

	t.Run("get unknown is not found", func(t *testing.T) {
		_, err := m.GetMetricStream(ctx, "missing")
		assertError(t, err, true)
	})

	t.Run("update preserves state and creation date", func(t *testing.T) {
		_, err := m.PutMetricStream(ctx, validStreamConfig("persist"))
		requireNoError(t, err)

		requireNoError(t, m.StartMetricStreams(ctx, []string{"persist"}))
		requireNoError(t, m.StopMetricStreams(ctx, []string{"persist"}))

		before, err := m.GetMetricStream(ctx, "persist")
		requireNoError(t, err)
		assertEqual(t, "stopped", before.State)

		cfg := validStreamConfig("persist")
		cfg.OutputFormat = "opentelemetry1.0"
		_, err = m.PutMetricStream(ctx, cfg)
		requireNoError(t, err)

		after, err := m.GetMetricStream(ctx, "persist")
		requireNoError(t, err)
		assertEqual(t, "stopped", after.State) // state survives an update
		assertEqual(t, before.CreationDate, after.CreationDate)
		assertEqual(t, "opentelemetry1.0", after.OutputFormat)
	})
}

func TestListMetricStreams(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.PutMetricStream(ctx, validStreamConfig("b-stream"))
	requireNoError(t, err)
	_, err = m.PutMetricStream(ctx, validStreamConfig("a-stream"))
	requireNoError(t, err)

	entries, err := m.ListMetricStreams(ctx)
	requireNoError(t, err)
	assertEqual(t, 2, len(entries))
	assertEqual(t, "a-stream", entries[0].Name)
	assertEqual(t, "b-stream", entries[1].Name)
	if entries[0].ARN == "" {
		t.Fatal("expected a non-empty ARN on the list entry")
	}
}

func TestDeleteMetricStream(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.PutMetricStream(ctx, validStreamConfig("gone-soon"))
	requireNoError(t, err)

	requireNoError(t, m.DeleteMetricStream(ctx, "gone-soon"))

	_, err = m.GetMetricStream(ctx, "gone-soon")
	assertError(t, err, true)

	t.Run("deleting an unknown name is a no-op, matching real CloudWatch", func(t *testing.T) {
		requireNoError(t, m.DeleteMetricStream(ctx, "never-existed"))
	})
}

func TestStartStopMetricStreams(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.PutMetricStream(ctx, validStreamConfig("toggle"))
	requireNoError(t, err)

	requireNoError(t, m.StopMetricStreams(ctx, []string{"toggle"}))
	s, err := m.GetMetricStream(ctx, "toggle")
	requireNoError(t, err)
	assertEqual(t, "stopped", s.State)

	requireNoError(t, m.StartMetricStreams(ctx, []string{"toggle"}))
	s, err = m.GetMetricStream(ctx, "toggle")
	requireNoError(t, err)
	assertEqual(t, "running", s.State)

	t.Run("unknown names are silently skipped", func(t *testing.T) {
		requireNoError(t, m.StartMetricStreams(ctx, []string{"never-existed"}))
		requireNoError(t, m.StopMetricStreams(ctx, []string{"never-existed"}))
	})
}

func TestMetricStreamTags(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := validStreamConfig("tagged")
	cfg.Tags = map[string]string{"env": "prod"}
	_, err := m.PutMetricStream(ctx, cfg)
	requireNoError(t, err)

	tags, err := m.MetricStreamTags(ctx, "tagged")
	requireNoError(t, err)
	assertEqual(t, "prod", tags["env"])

	requireNoError(t, m.AddMetricStreamTags(ctx, "tagged", map[string]string{"team": "sre"}))
	tags, err = m.MetricStreamTags(ctx, "tagged")
	requireNoError(t, err)
	assertEqual(t, "prod", tags["env"])
	assertEqual(t, "sre", tags["team"])

	requireNoError(t, m.RemoveMetricStreamTags(ctx, "tagged", []string{"env"}))
	tags, err = m.MetricStreamTags(ctx, "tagged")
	requireNoError(t, err)
	if _, ok := tags["env"]; ok {
		t.Fatal("expected env tag to be removed")
	}
	assertEqual(t, "sre", tags["team"])

	t.Run("an update ignores Tags (real PutMetricStream semantics)", func(t *testing.T) {
		again := validStreamConfig("tagged")
		again.Tags = map[string]string{"ignored": "yes"}
		_, err := m.PutMetricStream(ctx, again)
		requireNoError(t, err)

		tags, err := m.MetricStreamTags(ctx, "tagged")
		requireNoError(t, err)
		if _, ok := tags["ignored"]; ok {
			t.Fatal("expected update Tags to be ignored")
		}
		assertEqual(t, "sre", tags["team"])
	})

	t.Run("tag operations on unknown stream are not found", func(t *testing.T) {
		_, err := m.MetricStreamTags(ctx, "missing")
		assertError(t, err, true)

		assertError(t, m.AddMetricStreamTags(ctx, "missing", map[string]string{"a": "b"}), true)
		assertError(t, m.RemoveMetricStreamTags(ctx, "missing", []string{"a"}), true)
	})
}
