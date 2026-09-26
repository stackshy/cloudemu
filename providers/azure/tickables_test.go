package azure_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure"
)

// TestTickablesRegistersMonitor checks the serve ticker reaches Azure
// Monitor, so due metric alerts are evaluated without a read.
func TestTickablesRegistersMonitor(t *testing.T) {
	p := azure.New()

	got := p.Tickables()
	if len(got) != 1 || got[0] != config.Tickable(p.Monitor) {
		t.Fatalf("Tickables() = %v, want only this provider's Monitor", got)
	}
}
