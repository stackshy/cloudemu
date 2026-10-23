package wire

import "context"

type shutdownKey struct{}

// WithShutdown returns base carrying shutdown, a context the server cancels
// when it starts shutting down. Servers set it as each connection's base
// context so long-lived streaming responses can end promptly; a graceful
// shutdown otherwise waits for them until its timeout.
func WithShutdown(base, shutdown context.Context) context.Context {
	return context.WithValue(base, shutdownKey{}, shutdown)
}

// StreamContext returns a context for a long-lived streaming response. It
// ends with the request (client disconnect) or when the server starts
// shutting down, whichever comes first. The caller must call cancel.
func StreamContext(req context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(req)

	shutdown, ok := req.Value(shutdownKey{}).(context.Context)
	if !ok {
		return ctx, cancel
	}

	stop := context.AfterFunc(shutdown, cancel)

	return ctx, func() {
		stop()
		cancel()
	}
}
