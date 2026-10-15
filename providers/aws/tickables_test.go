package aws_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws"
)

// TestTickablesRegistersTimeDrivenServices checks the serve ticker reaches the
// region's own SQS, CloudWatch, SSM and CloudFormation, so queue gauges, due
// alarms, parameter policies and settling stack operations run without a read.
// SQS comes first so the alarms evaluated in the same tick see its gauges.
func TestTickablesRegistersTimeDrivenServices(t *testing.T) {
	p := aws.New()

	got := p.Tickables()
	if len(got) != 4 || got[0] != config.Tickable(p.SQS) || got[1] != config.Tickable(p.CloudWatch) ||
		got[2] != config.Tickable(p.SSM) || got[3] != config.Tickable(p.CloudFormation) {
		t.Fatalf("Tickables() = %v, want this provider's SQS, CloudWatch, SSM and CloudFormation", got)
	}
}
