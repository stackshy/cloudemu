package cloudlogging

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
	"github.com/stretchr/testify/require"
)

// TestPutLogEventsConcurrentWithGetLogGroupRace locks the fix for a
// Get-then-naked-mutate race: PutLogEvents used to fetch the stored *logGroup
// and then mutate its info.StoredBytes field in place, so a concurrent
// GetLogGroup/ListLogGroups reading that same info struct without the
// store's own lock could observe a torn write. PutLogEvents now
// copy-on-writes the logGroup before mutating and replaces it via
// memstore.Update, so this must stay clean under `go test -race`.
func TestPutLogEventsConcurrentWithGetLogGroupRace(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	setupGroupAndStream(t, m)

	const (
		iters   = 200
		message = "hello"
	)

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()

		for range iters {
			_ = m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
				{Message: message},
			})
		}
	}()

	go func() {
		defer wg.Done()

		for range iters {
			_, _ = m.GetLogGroup(ctx, "test-group")
			_, _ = m.ListLogGroups(ctx, scope.Scope{})
		}
	}()

	wg.Wait()

	info, err := m.GetLogGroup(ctx, "test-group")
	require.NoError(t, err)
	require.Equal(t, int64(iters*len(message)), info.StoredBytes)
}
