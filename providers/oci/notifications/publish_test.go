package notifications_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/monitoring"
	"github.com/stackshy/cloudemu/v2/providers/oci/notifications"
)

func TestPublishToAnUnknownTopic(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	_, err := m.PublishMessage(ctx, "ocid1.onstopic.oc1..missing", notifications.MessageSpec{Body: "hi"})
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestPublishRequiresABody(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	id := newTopic(t, m, "alpha", compartment)

	_, err := m.PublishMessage(ctx, id, notifications.MessageSpec{Title: "deploy"})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

func TestDeliveriesOfAnUnknownSubscription(t *testing.T) {
	assert.Empty(t, newMock(t).Deliveries("ocid1.onssubscription.oc1..missing"))
}

// SetMonitoring points ONS at the monitoring mock, which then carries the
// publish counters.
func TestPublishEmitsMetrics(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithRegion(region), config.WithCompartmentID(compartment))
	m := notifications.New(opts)
	mon := monitoring.New(opts)

	m.SetMonitoring(mon)

	id := newTopic(t, m, "alpha", compartment)
	_, err := m.PublishMessage(ctx, id, notifications.MessageSpec{Body: "shipped"})
	require.NoError(t, err)

	names, err := mon.ListMetrics(ctx, "oci_notification")
	require.NoError(t, err)
	assert.Contains(t, names, "PublishedMessages")
	assert.Contains(t, names, "DeliveredMessages")
}

// ONS caps a published message body at 64 KB.
func TestPublishMessageSizeCap(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	id := newTopic(t, m, "alpha", compartment)

	_, err := m.PublishMessage(ctx, id, notifications.MessageSpec{Body: strings.Repeat("x", 64*1024)})
	require.NoError(t, err)

	_, err = m.PublishMessage(ctx, id, notifications.MessageSpec{Body: strings.Repeat("x", 64*1024+1)})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "65536")
}
