package gcp_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/gcp"
)

// TestTickablesRegistersCloudMonitoring checks the serve ticker reaches Cloud
// Monitoring, so due alert policies are evaluated without a read.
func TestTickablesRegistersCloudMonitoring(t *testing.T) {
	p := gcp.New()

	got := p.Tickables()
	if len(got) != 1 || got[0] != config.Tickable(p.CloudMonitoring) {
		t.Fatalf("Tickables() = %v, want only this provider's CloudMonitoring", got)
	}
}
