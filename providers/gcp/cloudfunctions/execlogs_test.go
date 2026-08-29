package cloudfunctions

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/gcp/cloudlogging"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInvokeSurfacesLogsToCloudLogging verifies a Cloud Functions invoke writes
// execution log lines into Cloud Logging under the conventional log name,
// retrievable via GetLogEvents.
func TestInvokeSurfacesLogsToCloudLogging(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	cl := cloudlogging.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))),
		config.WithProjectID("test-project"),
	))
	m.SetLogSink(cl)

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "my-func", Runtime: "go121"})
	require.NoError(t, err)

	_, err = m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte(`{}`)})
	require.NoError(t, err)

	events, err := cl.GetLogEvents(ctx, &logdriver.LogQueryInput{
		LogGroup:  execLogGroup,
		LogStream: "my-func",
	})
	require.NoError(t, err)

	joined := joinMessages(events)
	assert.Contains(t, joined, "Function execution started")
	assert.Contains(t, joined, "finished")
}

// TestInvokeNoLogSinkDefault verifies the default path (no sink wired) is a
// no-op — no panic, and the invoke still succeeds.
func TestInvokeNoLogSinkDefault(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "my-func", Runtime: "go121"})
	require.NoError(t, err)

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte(`{}`)})
	require.NoError(t, err)
	assert.Equal(t, 200, out.StatusCode)
}

func joinMessages(events []logdriver.LogEvent) string {
	var b strings.Builder
	for i := range events {
		b.WriteString(events[i].Message)
		b.WriteByte('\n')
	}

	return b.String()
}
