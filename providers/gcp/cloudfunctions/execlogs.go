package cloudfunctions

import (
	"context"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// execLogGroup is the conventional Cloud Logging log name real Cloud Functions
// writes execution logs under.
const execLogGroup = "cloudfunctions.googleapis.com/cloud-functions"

// surfaceInvokeLogs writes one invocation's execution log lines into Cloud
// Logging under the function's stream, including any captured stdout/stderr
// from the real-engine path. Best-effort: with no log sink wired it is a no-op.
func (m *Mock) surfaceInvokeLogs(ctx context.Context, functionName, captured, funcError string) {
	if m.logs == nil {
		return
	}

	executionID := idgen.UUID()
	now := m.opts.Clock.Now()

	lines := []string{fmt.Sprintf("Function execution started (execution_id: %s)", executionID)}

	if captured != "" {
		lines = append(lines, strings.Split(strings.TrimRight(captured, "\n"), "\n")...)
	}

	if funcError != "" {
		lines = append(lines, fmt.Sprintf("Error: %s", funcError))
	}

	lines = append(lines, fmt.Sprintf("Function execution took 1 ms, finished (execution_id: %s)", executionID))

	_, _ = m.logs.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: execLogGroup})
	_, _ = m.logs.CreateLogStream(ctx, execLogGroup, functionName)

	events := make([]logdriver.LogEvent, 0, len(lines))
	for _, line := range lines {
		events = append(events, logdriver.LogEvent{Timestamp: now, Message: line})
	}

	_ = m.logs.PutLogEvents(ctx, execLogGroup, functionName, events)
}
