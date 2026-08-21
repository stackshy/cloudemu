package logging_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	ocilogging "github.com/stackshy/cloudemu/v2/providers/oci/logging"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// concurrency is how many goroutines each phase runs.
const concurrency = 16

// TestConcurrentOperations exercises every store the mock holds from many
// goroutines at once, so -race catches a lock a method forgot to take.
func TestConcurrentOperations(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	g := newGroup(t, m, compartmentA, "app-logs")

	logs := make([]string, concurrency)
	for i := range logs {
		logs[i] = newCustomLog(t, m, g.ID, "log-"+strconv.Itoa(i)).ID
	}

	base := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup

	for i := range concurrency {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			_ = m.PutLogs(ctx, logs[i], []ocilogging.LogEntryBatch{{
				Entries: []ocilogging.LogEntryItem{{Data: "entry-" + strconv.Itoa(i), Time: base}},
			}})

			_, _ = m.GetLog(ctx, g.ID, logs[i])
			_, _ = m.ListLogs(ctx, g.ID, ocilogging.LogFilter{})
			_, _ = m.ListGroups(ctx, compartmentA, "")
			_, _ = m.GetGroup(ctx, g.ID)
			_, _ = m.Entries(ctx, logs[i])

			_, _ = m.SearchLogs(ctx, ocilogging.SearchRequest{
				Query:     `search "` + compartmentA + `"`,
				TimeStart: base.Add(-time.Hour),
				TimeEnd:   base.Add(time.Hour),
			})

			_, _ = m.ListLogGroups(ctx, scope.Scope{Compartment: compartmentA})
			_, _ = m.ListLogStreams(ctx, "app-logs")
			_, _ = m.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "app-logs"})
			_, _ = m.FilterLogEvents(ctx, &driver.FilterLogEventsInput{LogGroup: "app-logs"})
			_ = m.PutLogEvents(ctx, "app-logs", "log-"+strconv.Itoa(i), []driver.LogEvent{
				{Timestamp: base, Message: "portable"},
			})
		}(i)
	}

	wg.Wait()

	entries, err := m.Entries(ctx, logs[0])
	require.NoError(t, err)
	require.Len(t, entries, 2)
}

// TestConcurrentCreateAndDelete races creates against deletes across both
// stores, where a group delete walks the logs.
func TestConcurrentCreateAndDelete(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	var wg sync.WaitGroup

	for i := range concurrency {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			name := "group-" + strconv.Itoa(i)

			created, err := m.CreateGroup(ctx, ocilogging.LogGroupSpec{
				CompartmentID: compartmentA,
				DisplayName:   name,
			})
			if err != nil {
				return
			}

			_, _ = m.CreateLog(ctx, created.ID, ocilogging.LogSpec{DisplayName: "stdout", IsEnabled: true})
			_ = m.MoveGroup(ctx, created.ID, compartmentB)
			_ = m.DeleteGroup(ctx, created.ID)
		}(i)
	}

	wg.Wait()

	groups, err := m.ListGroups(ctx, compartmentA, "")
	require.NoError(t, err)
	require.Empty(t, groups)
}
