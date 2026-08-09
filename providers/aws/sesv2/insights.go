package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// Metrics, message insights and recommendations depend on real send/engagement
// telemetry the emulator does not collect. BatchGetMetricData returns zeroed
// series, GetMessageInsights reflects a message the emulator actually recorded,
// and ListRecommendations/insights return empty or synthesized results.

// metricDataPoints is the number of synthesized data points per metric query.
const metricDataPoints = 3

// BatchGetMetricData returns a zeroed data series for each query ID.
func (*Mock) BatchGetMetricData(_ context.Context, queryIDs []string) (map[string][]int64, error) {
	out := make(map[string][]int64, len(queryIDs))
	for _, id := range queryIDs {
		out[id] = make([]int64, metricDataPoints)
	}

	return out, nil
}

// GetMessageInsights returns insight for a message the emulator recorded.
func (m *Mock) GetMessageInsights(_ context.Context, messageID string) (*driver.SentMessage, error) {
	m.sentMu.RLock()
	defer m.sentMu.RUnlock()

	for i := range m.sent {
		if m.sent[i].MessageID == messageID {
			msg := m.sent[i]

			return &msg, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "message %q does not exist", messageID)
}

// GetEmailAddressInsights returns a synthesized insights JSON blob.
func (*Mock) GetEmailAddressInsights(_ context.Context, emailAddress string) (string, error) {
	if emailAddress == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "EmailAddress is required")
	}

	return `{"EmailAddress":"` + emailAddress + `","Isp":"(synthesized)"}`, nil
}

// ListRecommendations returns an empty recommendation list.
func (*Mock) ListRecommendations(_ context.Context) ([]string, error) {
	return []string{}, nil
}
