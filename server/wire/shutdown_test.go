package wire_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

func TestShutdownSignal(t *testing.T) {
	if ch := wire.ShutdownSignal(context.Background()); ch != nil {
		t.Fatalf("ShutdownSignal without a notifier = %v, want nil", ch)
	}

	n := wire.NewShutdownNotifier()
	ch := wire.ShutdownSignal(n.Context(context.Background()))

	select {
	case <-ch:
		t.Fatal("signal fired before Notify")
	default:
	}

	n.Notify()
	n.Notify() // idempotent

	select {
	case <-ch:
	default:
		t.Fatal("signal did not fire after Notify")
	}
}
