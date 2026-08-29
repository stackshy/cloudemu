package functions

import (
	"context"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// execLogGroup is the conventional Log Analytics table real Azure Functions
// writes host/execution logs under.
const execLogGroup = "FunctionAppLogs"

// surfaceInvokeLogs writes one invocation's execution log lines into Log
// Analytics under the function's stream, including any captured stdout/stderr
// from the real-engine path. Best-effort: with no log sink wired it is a no-op.
func (m *Mock) surfaceInvokeLogs(ctx context.Context, functionName, captured, funcError string) {
	if m.logs == nil {
		return
	}

	invocationID := idgen.UUID()
	now := m.opts.Clock.Now()

	lines := []string{fmt.Sprintf("Executing function=%s invocationId=%s", functionName, invocationID)}

	if captured != "" {
		lines = append(lines, strings.Split(strings.TrimRight(captured, "\n"), "\n")...)
	}

	if funcError != "" {
		lines = append(lines, fmt.Sprintf("Executed function=%s Failed: %s", functionName, funcError))
	} else {
		lines = append(lines, fmt.Sprintf("Executed function=%s Succeeded, invocationId=%s", functionName, invocationID))
	}

	_, _ = m.logs.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: execLogGroup})
	_, _ = m.logs.CreateLogStream(ctx, execLogGroup, functionName)

	events := make([]logdriver.LogEvent, 0, len(lines))
	for _, line := range lines {
		events = append(events, logdriver.LogEvent{Timestamp: now, Message: line})
	}

	_ = m.logs.PutLogEvents(ctx, execLogGroup, functionName, events)
}
