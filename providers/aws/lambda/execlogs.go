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

// logTailBytes is how much of the execution log Invoke returns for
// LogType=Tail: "The last 4 KB of the execution log, which is base64-encoded."
// See https://docs.aws.amazon.com/lambda/latest/api/API_Invoke.html
const logTailBytes = 4 * 1024

// surfaceInvokeLogs renders one invocation's execution log (the START/END/
// REPORT bookend real Lambda emits, plus any captured stdout/stderr from the
// real-engine path), writes it into CloudWatch Logs when a log sink is wired
// (best-effort: with no sink, library users are unaffected), and returns its
// tail, the last 4 KB that Invoke reports for LogType=Tail.
func (m *Mock) surfaceInvokeLogs(ctx context.Context, functionName, executedVersion, captured, funcError string) string {
	requestID := idgen.UUID()
	lines := invokeLogLines(executedVersion, requestID, captured, funcError)
	tail := logTail(lines)

	if m.logs == nil {
		return tail
	}

	now := m.opts.Clock.Now()
	group := logGroupPrefix + functionName
	stream := invokeLogStream(now, executedVersion, requestID)

	_, _ = m.logs.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: group})
	_, _ = m.logs.CreateLogStream(ctx, group, stream)

	events := make([]logdriver.LogEvent, 0, len(lines))
	for _, line := range lines {
		// CloudWatch Logs rejects an empty message, and one empty line would
		// fail the whole batch, so blank output lines are skipped.
		if line == "" {
			continue
		}

		events = append(events, logdriver.LogEvent{Timestamp: now, Message: line})
	}

	_ = m.logs.PutLogEvents(ctx, group, stream, events)

	return tail
}

// logTail joins the execution-log lines (newline-terminated, as the log is
// written) and keeps at most the last logTailBytes of it.
func logTail(lines []string) string {
	var b strings.Builder

	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}

	full := b.String()
	if len(full) > logTailBytes {
		full = full[len(full)-logTailBytes:]
	}

	return full
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
