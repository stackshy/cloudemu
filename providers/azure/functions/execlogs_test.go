package functions

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/loganalytics"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInvokeSurfacesLogsToLogAnalytics verifies an Azure Functions invoke writes
// execution log lines into Log Analytics under the FunctionAppLogs table,
// retrievable via GetLogEvents.
func TestInvokeSurfacesLogsToLogAnalytics(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	la := loganalytics.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))),
		config.WithAccountID("test-sub"),
	))
	m.SetLogSink(la)

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "myFunc", Runtime: "dotnet6"})
	require.NoError(t, err)

	_, err = m.Invoke(ctx, driver.InvokeInput{FunctionName: "myFunc", Payload: []byte(`{}`)})
	require.NoError(t, err)

	events, err := la.GetLogEvents(ctx, &logdriver.LogQueryInput{
		LogGroup:  execLogGroup,
		LogStream: "myFunc",
	})
	require.NoError(t, err)

	joined := joinMessages(events)
	assert.Contains(t, joined, "Executing function=myFunc")
	assert.Contains(t, joined, "Succeeded")
}

// TestInvokeNoLogSinkDefault verifies the default path (no sink wired) is a
// no-op — no panic, and the invoke still succeeds.
func TestInvokeNoLogSinkDefault(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "myFunc", Runtime: "dotnet6"})
	require.NoError(t, err)

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "myFunc", Payload: []byte(`{}`)})
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
