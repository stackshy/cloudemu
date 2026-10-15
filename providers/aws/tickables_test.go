package aws_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws"
)

// TestTickablesRegistersTimeDrivenServices checks the serve ticker reaches the
// region's own EFS, CloudWatch, SSM and CloudFormation, so EFS metrics, due
// alarms, parameter policies and settling stack operations run without a read.
// EFS comes before CloudWatch so alarms see the metrics published in the same tick.
func TestTickablesRegistersTimeDrivenServices(t *testing.T) {
	p := aws.New()

	got := p.Tickables()
	if len(got) != 4 || got[0] != config.Tickable(p.EFS) || got[1] != config.Tickable(p.CloudWatch) ||
		got[2] != config.Tickable(p.SSM) || got[3] != config.Tickable(p.CloudFormation) {
		t.Fatalf("Tickables() = %v, want this provider's EFS, CloudWatch, SSM and CloudFormation", got)
	}
}
