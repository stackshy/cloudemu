package aws_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws"
)

// TestTickablesRegistersTimeDrivenServices checks the serve ticker reaches the
// region's own CloudWatch, SSM, CloudFormation and Batch, so due alarms,
// parameter policies, settling stack operations and Batch job state-change
// events run without a read.
func TestTickablesRegistersTimeDrivenServices(t *testing.T) {
	p := aws.New()

	got := p.Tickables()
	if len(got) != 4 || got[0] != config.Tickable(p.CloudWatch) || got[1] != config.Tickable(p.SSM) ||
		got[2] != config.Tickable(p.CloudFormation) || got[3] != config.Tickable(p.Batch) {
		t.Fatalf("Tickables() = %v, want this provider's CloudWatch, SSM, CloudFormation and Batch", got)
	}
}
