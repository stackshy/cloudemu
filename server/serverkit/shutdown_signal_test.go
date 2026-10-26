package serverkit

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// TestWithShutdownSignalEndsLongLivedHandlers: a handler holding a response
// open until the server shuts down (as a SubscribeToShard event stream does)
// sees wire.ShutdownSignal fire as soon as Shutdown begins, so a graceful
// shutdown completes promptly rather than waiting out its timeout.
func TestWithShutdownSignalEndsLongLivedHandlers(t *testing.T) {
	entered := make(chan struct{})

	srv := withShutdownSignal(&http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(entered)

			select {
			case <-wire.ShutdownSignal(r.Context()):
			case <-time.After(30 * time.Second):
			}
		}),
	})

	var lc net.ListenConfig

	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() { _ = srv.Serve(ln) }()

	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+ln.Addr().String(), http.NoBody)

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never started")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("Shutdown took %v; the long-lived handler did not see the shutdown signal", took)
	}
}
