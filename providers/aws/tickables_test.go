package aws_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws"
)

// TestTickablesRegistersTimeDrivenServices checks the serve ticker reaches the
// region's own CloudWatch, SSM and CloudFormation, so due alarms, parameter policies and
// settling stack operations run without a read.
func TestTickablesRegistersTimeDrivenServices(t *testing.T) {
	p := aws.New()

	got := p.Tickables()
	if len(got) != 3 || got[0] != config.Tickable(p.CloudWatch) || got[1] != config.Tickable(p.SSM) ||
		got[2] != config.Tickable(p.CloudFormation) {
		t.Fatalf("Tickables() = %v, want this provider's CloudWatch, SSM and CloudFormation", got)
	}
}
