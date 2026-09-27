package aws_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws"
)

// TestTickablesRegistersTimeDrivenServices checks the serve ticker reaches the
// region's own CloudWatch and SSM, so due alarms and parameter policies run
// without a read.
func TestTickablesRegistersTimeDrivenServices(t *testing.T) {
	p := aws.New()

	got := p.Tickables()
	if len(got) != 2 || got[0] != config.Tickable(p.CloudWatch) || got[1] != config.Tickable(p.SSM) {
		t.Fatalf("Tickables() = %v, want this provider's CloudWatch and SSM", got)
	}
}
