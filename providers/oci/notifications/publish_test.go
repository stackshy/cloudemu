package notifications_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/monitoring"
	"github.com/stackshy/cloudemu/v2/providers/oci/notifications"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
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

// ONS metrics land in the topic's own compartment, keyed by resourceId, as
// real oci_notification metrics are, not in the default compartment.
func TestPublishEmitsMetricsIntoTheTopicCompartment(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithRegion(region), config.WithCompartmentID(compartment))
	m := notifications.New(opts)
	mon := monitoring.New(opts)

	m.SetMonitoring(mon)

	id := newTopic(t, m, "alpha", otherCompartment)
	_, err := m.PublishMessage(ctx, id, notifications.MessageSpec{Body: "shipped"})
	require.NoError(t, err)

	metrics, err := mon.ListOCIMetrics(ctx, otherCompartment, monitoring.OCIMetricFilter{Namespace: "oci_notification"})
	require.NoError(t, err)

	names := make([]string, 0, len(metrics))
	for i := range metrics {
		names = append(names, metrics[i].Name)
		assert.Equal(t, map[string]string{"resourceId": id}, metrics[i].Dimensions)
	}

	assert.ElementsMatch(t, []string{"PublishedMessages", "DeliveredMessages"}, names)

	inDefault, err := mon.ListOCIMetrics(ctx, compartment, monitoring.OCIMetricFilter{Namespace: "oci_notification"})
	require.NoError(t, err)
	assert.Empty(t, inDefault)
}

// The reserved oci_ namespace is open only to a sibling service's own path;
// the portable PutMetricData still rejects it.
func TestReservedNamespaceStaysClosedToPortableCallers(t *testing.T) {
	ctx := context.Background()
	mon := monitoring.New(config.NewOptions(config.WithCompartmentID(compartment)))

	err := mon.PutMetricData(ctx, []mondriver.MetricDatum{{
		Namespace: "oci_notification", MetricName: "Forged", Value: 1,
	}})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

// A busy subscription keeps only its most recent deliveries.
func TestDeliveryHistoryIsBounded(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	topicID := newTopic(t, m, "alpha", compartment)

	sub, err := m.CreateSubscription(ctx, notifications.SubscriptionSpec{
		TopicID: topicID, CompartmentID: compartment,
		Protocol: notifications.ProtocolFunctions, Endpoint: "ocid1.fnfunc.oc1..x",
	})
	require.NoError(t, err)

	const sent = 105
	for i := range sent {
		_, err := m.PublishMessage(ctx, topicID, notifications.MessageSpec{Body: strconv.Itoa(i)})
		require.NoError(t, err)
	}

	got := m.Deliveries(sub.ID)
	require.Len(t, got, 100)
	assert.Equal(t, "5", got[0].Body, "the oldest five were dropped")
	assert.Equal(t, "104", got[len(got)-1].Body)
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
