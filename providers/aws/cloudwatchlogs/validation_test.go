package cloudwatchlogs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

func TestCreateLogGroupNameRules(t *testing.T) {
	tests := []struct {
		name    string
		group   string
		wantErr bool
	}{
		{name: "space", group: "bad name", wantErr: true},
		{name: "520 characters", group: strings.Repeat("a", 520), wantErr: true},
		{name: "reserved aws/ prefix", group: "aws/custom", wantErr: true},
		{name: "512 characters", group: strings.Repeat("a", 512)},
		{name: "lambda style path", group: "/aws/lambda/my-fn"},
		{name: "all allowed symbols", group: "/app/#svc_1.log-x"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newTestMock().CreateLogGroup(context.Background(), driver.LogGroupConfig{Name: tc.group})
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Equal(t, errors.InvalidArgument, errors.GetCode(err))
		})
	}
}

func TestCreateLogStreamNameRules(t *testing.T) {
	tests := []struct {
		name    string
		stream  string
		wantErr bool
	}{
		{name: "colon", stream: "a:b", wantErr: true},
		{name: "asterisk", stream: "a*b", wantErr: true},
		{name: "513 characters", stream: strings.Repeat("a", 513), wantErr: true},
		{name: "lambda style stream", stream: "2025/01/01/[$LATEST]abc123"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			_, err := m.CreateLogGroup(context.Background(), driver.LogGroupConfig{Name: "grp"})
			require.NoError(t, err)

			_, err = m.CreateLogStream(context.Background(), "grp", tc.stream)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Equal(t, errors.InvalidArgument, errors.GetCode(err))
		})
	}
}

func TestPutLogEventsBatchLimits(t *testing.T) {
	base := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	manyEvents := func(n int) []driver.LogEvent {
		out := make([]driver.LogEvent, n)
		for i := range out {
			out[i] = driver.LogEvent{Timestamp: base, Message: "m"}
		}

		return out
	}

	tests := []struct {
		name    string
		events  []driver.LogEvent
		wantErr bool
	}{
		{name: "10001 events", events: manyEvents(10001), wantErr: true},
		{name: "10000 events", events: manyEvents(10000)},
		// 1,048,551 bytes plus the 26 byte overhead is one byte over the cap.
		{name: "one byte over the cap", events: []driver.LogEvent{
			{Timestamp: base, Message: strings.Repeat("a", 1048551)},
		}, wantErr: true},
		{name: "exactly at the cap", events: []driver.LogEvent{
			{Timestamp: base, Message: strings.Repeat("a", 1048550)},
		}},
		{name: "empty message", events: []driver.LogEvent{
			{Timestamp: base, Message: "ok"}, {Timestamp: base, Message: ""},
		}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			setupGroupAndStream(t, m)

			err := m.PutLogEvents(context.Background(), "test-group", "test-stream", tc.events)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Equal(t, errors.InvalidArgument, errors.GetCode(err))
		})
	}
}
