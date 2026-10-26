package wire

import (
	"context"
	"sync"
)

type shutdownSignalKey struct{}

// WithShutdownSignal returns a copy of ctx that carries ch as the server's
// shutdown signal. A server installs it as the base context of every request
// and closes ch when a graceful shutdown begins, so long-lived handlers (event
// streams, watches) can end promptly instead of holding the shutdown open
// until its timeout. Unlike canceling the request context, it leaves ordinary
// in-flight requests untouched.
func WithShutdownSignal(ctx context.Context, ch <-chan struct{}) context.Context {
	return context.WithValue(ctx, shutdownSignalKey{}, ch)
}

// ShutdownSignal returns the channel that closes when the serving process
// begins shutting down, or nil (which never fires in a select) when ctx carries
// none, e.g. under httptest or an embedding application's own server.
func ShutdownSignal(ctx context.Context) <-chan struct{} {
	ch, _ := ctx.Value(shutdownSignalKey{}).(<-chan struct{})

	return ch
}

// ShutdownNotifier owns one shutdown signal: Context stamps it onto a base
// context and Notify closes it (safe to call more than once).
type ShutdownNotifier struct {
	once sync.Once
	ch   chan struct{}
}

// NewShutdownNotifier returns an open shutdown signal.
func NewShutdownNotifier() *ShutdownNotifier {
	return &ShutdownNotifier{ch: make(chan struct{})}
}

// Context returns ctx carrying this notifier's signal.
func (n *ShutdownNotifier) Context(ctx context.Context) context.Context {
	return WithShutdownSignal(ctx, n.ch)
}

// Notify closes the signal, ending every handler waiting on it.
func (n *ShutdownNotifier) Notify() {
	n.once.Do(func() { close(n.ch) })
}
