package serverkit

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
)

// TestBaseOptsAsyncSettle asserts --async-settle threads into the provider
// options (and stays off by default), so serve can opt into transient states.
func TestBaseOptsAsyncSettle(t *testing.T) {
	for _, want := range []bool{false, true} {
		opts := config.NewOptions(baseOptsFor(&Config{AsyncSettle: want})...)
		if opts.AsyncSettle != want {
			t.Fatalf("AsyncSettle=%v: options.AsyncSettle = %v", want, opts.AsyncSettle)
		}
	}
}
