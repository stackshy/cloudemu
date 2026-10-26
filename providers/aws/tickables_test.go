package aws_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws"
)

// TestTickablesRegistersTimeDrivenServices checks the serve ticker reaches the
// region's own SQS, EFS, Kafka, CloudWatch, SSM, CloudFormation and Batch, so
// queue gauges, EFS and Kafka metrics, due alarms, parameter policies, settling
// stack operations and Batch job state-change events run without a read. SQS,
// EFS and Kafka come before CloudWatch so alarms see the metrics published in
// the same tick.
func TestTickablesRegistersTimeDrivenServices(t *testing.T) {
	p := aws.New()

	got := p.Tickables()
	want := []config.Tickable{p.SQS, p.EFS, p.Kafka, p.CloudWatch, p.SSM, p.CloudFormation, p.Batch}
	if len(got) != len(want) {
		t.Fatalf("Tickables() = %v, want this provider's SQS, EFS, Kafka, CloudWatch, SSM, CloudFormation and Batch", got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Tickables()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
