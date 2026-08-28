package dynamodb

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// TestConcurrentItemRaceFree hammers a single table and item with concurrent
// PutItem/UpdateItem/GetItem/Scan calls. The item paths use copy-on-write (Set a
// fresh map) and clone-on-read, so a shared item map is never mutated in place;
// this test guards that invariant under `go test -race`.
func TestConcurrentItemRaceFree(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestTable(m, "hot-table")

	key := map[string]any{"pk": "p", "sk": "s"}

	const (
		workers    = 8
		iterations = 40
	)

	var wg sync.WaitGroup

	run := func(fn func()) {
		defer wg.Done()

		for i := 0; i < iterations; i++ {
			fn()
		}
	}

	wg.Add(4 * workers)

	for w := 0; w < workers; w++ {
		go run(func() {
			_ = m.PutItem(ctx, "hot-table", map[string]any{"pk": "p", "sk": "s", "n": 1})
		})
		go run(func() {
			_, _ = m.UpdateItem(ctx, driver.UpdateItemInput{
				Table:   "hot-table",
				Key:     key,
				Actions: []driver.UpdateAction{{Action: "SET", Field: "n", Value: 2}},
			})
		})
		go run(func() { _, _ = m.GetItem(ctx, "hot-table", key) })
		go run(func() { _, _ = m.Scan(ctx, driver.ScanInput{Table: "hot-table"}) })
	}

	wg.Wait()

	if _, err := m.DescribeTable(ctx, "hot-table"); err != nil {
		t.Fatalf("DescribeTable after storm: %v", err)
	}
}
