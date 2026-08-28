package sqs

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// TestConcurrentQueueRaceFree hammers a single queue with concurrent
// SendMessage/ReceiveMessages/SetQueueAttributes/GetQueueAttributes calls. Each
// path reads or mutates fields on the shared *queueData (messages, attributes,
// lastModifiedAt), so without queueData.mu the -race detector flags a data race.
// It must run clean under `go test -race`.
func TestConcurrentQueueRaceFree(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	q := createStdQueue(m, "hot-queue")
	url := q.URL

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
		go run(func() { _, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "x"}) })
		go run(func() {
			_, _ = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 5})
		})
		go run(func() { _ = m.SetQueueAttributes(ctx, url, map[string]int{"VisibilityTimeout": 45}) })
		go run(func() { _, _ = m.GetQueueAttributes(ctx, url) })
	}

	wg.Wait()

	if _, err := m.GetQueueAttributes(ctx, url); err != nil {
		t.Fatalf("GetQueueAttributes after storm: %v", err)
	}
}
