package lambda

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestInvokeSurfacesLogsToCloudWatch verifies a Lambda invoke writes
// START/END/REPORT lines into the conventional /aws/lambda/<name> log group,
// retrievable via CloudWatch Logs GetLogEvents.
func TestInvokeSurfacesLogsToCloudWatch(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	cwl := cloudwatchlogs.New(config.NewOptions(config.WithRegion("us-east-1")))
	m.SetLogSink(cwl)

	_, err := m.CreateFunction(ctx, defaultFuncConfig())
	requireNoError(t, err)

	_, err = m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte(`{}`)})
	requireNoError(t, err)

	events, err := cwl.GetLogEvents(ctx, &logdriver.LogQueryInput{LogGroup: "/aws/lambda/my-func"})
	requireNoError(t, err)

	joined := joinMessages(events)
	for _, want := range []string{"START RequestId:", "END RequestId:", "REPORT RequestId:"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in surfaced logs: %q", want, joined)
		}
	}
}

// TestInvokeSurfacesHandlerErrorLog verifies a handler error is surfaced as an
// [ERROR] line alongside the START/END bookend.
func TestInvokeSurfacesHandlerErrorLog(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	cwl := cloudwatchlogs.New(config.NewOptions(config.WithRegion("us-east-1")))
	m.SetLogSink(cwl)

	_, err := m.CreateFunction(ctx, defaultFuncConfig())
	requireNoError(t, err)

	m.RegisterHandler("my-func", func(context.Context, []byte) ([]byte, error) {
		return nil, context.Canceled
	})

	_, err = m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte(`{}`)})
	requireNoError(t, err)

	events, err := cwl.GetLogEvents(ctx, &logdriver.LogQueryInput{LogGroup: "/aws/lambda/my-func"})
	requireNoError(t, err)

	if !strings.Contains(joinMessages(events), "[ERROR]") {
		t.Fatalf("expected an [ERROR] line, got: %q", joinMessages(events))
	}
}

// TestInvokeNoLogSinkDefault verifies the default path (no sink wired) is a
// no-op — no panic, and the invoke still succeeds.
func TestInvokeNoLogSinkDefault(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, defaultFuncConfig())
	requireNoError(t, err)

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte(`{}`)})
	requireNoError(t, err)
	assertEqual(t, 200, out.StatusCode)
}

func joinMessages(events []logdriver.LogEvent) string {
	var b strings.Builder
	for i := range events {
		b.WriteString(events[i].Message)
		b.WriteByte('\n')
	}

	return b.String()
}
