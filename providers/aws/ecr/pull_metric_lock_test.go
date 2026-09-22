package ecr

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// reentrantMonitoring forwards to a real CloudWatch mock but, on every
// PutMetricData, first calls back into the ECR mock — the shape of an alarm
// action (SNS -> Lambda) that reads the registry while the metric is recorded.
type reentrantMonitoring struct {
	mondriver.Monitoring
	ecr *Mock
}

func (r *reentrantMonitoring) PutMetricData(ctx context.Context, data []mondriver.MetricDatum) error {
	if _, err := r.ecr.ListImages(ctx, "locked-repo"); err != nil {
		return err
	}

	return r.Monitoring.PutMetricData(ctx, data)
}

// TestPullMetricPublishedOutsideLock pins that GetImage publishes
// RepositoryPullCount after releasing m.mu: a monitoring backend that re-enters
// the registry must not deadlock.
func TestPullMetricPublishedOutsideLock(t *testing.T) {
	m, _ := newTestMock()
	m.SetMonitoring(&reentrantMonitoring{Monitoring: cloudwatch.New(m.opts), ecr: m})

	createTestRepo(t, m, "locked-repo")
	pushTestImage(t, m, "locked-repo", "v1")

	done := make(chan error, 1)

	go func() {
		_, err := m.GetImage(context.Background(), "locked-repo", "v1")
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("GetImage: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetImage deadlocked: the pull metric was published while holding m.mu")
	}
}
