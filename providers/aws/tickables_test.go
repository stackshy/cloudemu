package aws_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws"
)

// TestTickablesRegistersCloudWatch checks the serve ticker reaches the
// region's own CloudWatch, so due alarms are evaluated without a read.
func TestTickablesRegistersCloudWatch(t *testing.T) {
	p := aws.New()

	got := p.Tickables()
	if len(got) != 1 || got[0] != config.Tickable(p.CloudWatch) {
		t.Fatalf("Tickables() = %v, want only this provider's CloudWatch", got)
	}
}
