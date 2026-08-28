package lambda

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// logGroupPrefix is the conventional CloudWatch Logs group real Lambda writes a
// function's invocation logs under: /aws/lambda/<function-name>.
const logGroupPrefix = "/aws/lambda/"

// surfaceInvokeLogs writes one invocation's log lines into CloudWatch Logs,
// mirroring the START/END/REPORT bookend real Lambda emits, plus any captured
// stdout/stderr (from the real-engine path). It is best-effort: with no log
// sink wired it is a no-op, so library users are unaffected.
func (m *Mock) surfaceInvokeLogs(ctx context.Context, functionName, executedVersion, captured, funcError string) {
	if m.logs == nil {
		return
	}

	requestID := idgen.UUID()
	now := m.opts.Clock.Now()
	group := logGroupPrefix + functionName
	stream := invokeLogStream(now, executedVersion, requestID)

	lines := invokeLogLines(executedVersion, requestID, captured, funcError)

	_, _ = m.logs.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: group})
	_, _ = m.logs.CreateLogStream(ctx, group, stream)

	events := make([]logdriver.LogEvent, 0, len(lines))
	for _, line := range lines {
		events = append(events, logdriver.LogEvent{Timestamp: now, Message: line})
	}

	_ = m.logs.PutLogEvents(ctx, group, stream, events)
}

// invokeLogStream builds the CloudWatch Logs stream name real Lambda uses for an
// invocation: "YYYY/MM/DD/[<version>]<requestId>".
func invokeLogStream(now time.Time, executedVersion, requestID string) string {
	return fmt.Sprintf("%s/[%s]%s", now.UTC().Format("2006/01/02"), executedVersion, requestID)
}

// invokeLogLines renders the log lines for one invocation: the START bookend,
// each captured stdout/stderr line, and the END/REPORT bookend, matching the
// shape real Lambda writes to CloudWatch Logs.
func invokeLogLines(executedVersion, requestID, captured, funcError string) []string {
	lines := []string{fmt.Sprintf("START RequestId: %s Version: %s", requestID, executedVersion)}

	if captured != "" {
		lines = append(lines, strings.Split(strings.TrimRight(captured, "\n"), "\n")...)
	}

	if funcError != "" {
		lines = append(lines, fmt.Sprintf("[ERROR] %s", funcError))
	}

	lines = append(lines,
		fmt.Sprintf("END RequestId: %s", requestID),
		fmt.Sprintf("REPORT RequestId: %s\tDuration: 1.00 ms\tBilled Duration: 1 ms", requestID),
	)

	return lines
}
