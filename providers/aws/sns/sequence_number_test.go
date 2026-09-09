package sns

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPublishFifoReturnsSequenceNumber locks the real-SNS contract that Publish
// to a FIFO topic returns a large, monotonically increasing SequenceNumber,
// while a standard topic returns none.
func TestPublishFifoReturnsSequenceNumber(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateTopic(ctx, driver.TopicConfig{
		Name: "orders.fifo", FifoTopic: true, ContentBasedDeduplication: true,
	})
	require.NoError(t, err)

	first, err := m.Publish(ctx, driver.PublishInput{
		TopicID: "orders.fifo", Message: "m1", MessageGroupID: "g1",
	})
	require.NoError(t, err)
	assert.Len(t, first.SequenceNumber, 20, "SequenceNumber is a fixed-width numeric string")
	assert.NotEqual(t, "00000000000000000000", first.SequenceNumber, "SequenceNumber must be non-zero")

	second, err := m.Publish(ctx, driver.PublishInput{
		TopicID: "orders.fifo", Message: "m2", MessageGroupID: "g1",
	})
	require.NoError(t, err)
	assert.Greater(t, second.SequenceNumber, first.SequenceNumber,
		"SequenceNumber must increase for successive publishes")
}

// TestPublishStandardOmitsSequenceNumber asserts a standard topic's publish
// carries no SequenceNumber (real SNS returns the element only for FIFO topics).
func TestPublishStandardOmitsSequenceNumber(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateTopic(ctx, driver.TopicConfig{Name: "plain"})
	require.NoError(t, err)

	out, err := m.Publish(ctx, driver.PublishInput{TopicID: "plain", Message: "hi"})
	require.NoError(t, err)
	assert.Empty(t, out.SequenceNumber, "standard topics return no SequenceNumber")
}
