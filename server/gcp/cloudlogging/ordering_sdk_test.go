package cloudlogging_test

import (
	"context"
	"testing"

	logging "google.golang.org/api/logging/v2"
)

// TestSDKListOrderingDeterministic locks the #259 ordering fix at the wire
// level: Projects.Logs.List must return the same sequence of log names on
// every call, regardless of the order the logs were created in.
func TestSDKListOrderingDeterministic(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	// Create five logs in a deliberately non-sorted order; a write lazily
	// creates the log group.
	for _, id := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		if _, err := svc.Entries.Write(&logging.WriteLogEntriesRequest{
			LogName: "projects/" + testProject + "/logs/" + id,
			Entries: []*logging.LogEntry{
				{TextPayload: "entry for " + id},
			},
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("Entries.Write(%s): %v", id, err)
		}
	}

	listLogs := func() []string {
		var names []string

		call := svc.Projects.Logs.List("projects/" + testProject).Context(ctx)
		if err := call.Pages(ctx, func(page *logging.ListLogsResponse) error {
			names = append(names, page.LogNames...)
			return nil
		}); err != nil {
			t.Fatalf("Projects.Logs.List: %v", err)
		}

		return names
	}

	first := listLogs()
	if len(first) != 5 {
		t.Fatalf("got %d logs, want 5: %v", len(first), first)
	}

	for i := 0; i < 4; i++ {
		got := listLogs()
		if len(got) != len(first) {
			t.Fatalf("list #%d returned %d logs, want %d: %v", i+2, len(got), len(first), got)
		}

		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("list #%d order diverged at index %d: got %q, want %q (full: %v vs %v)",
					i+2, j, got[j], first[j], got, first)
			}
		}
	}
}
