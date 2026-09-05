// Package grpc is the standalone emulator's gRPC transport foundation.
//
// It wraps a *grpc.Server preconfigured with only the two transport-level
// services every gRPC endpoint should expose — the standard health service
// (grpc.health.v1.Health) and server reflection — plus a listener lifecycle
// (Serve/Shutdown) shaped like the *http.Server one serverkit already drives,
// so a gRPC endpoint can sit beside the REST endpoints on its own TCP port
// without duplicating the bind/serve/shutdown loop.
//
// It registers NO application (cloud service) servers. Those are layered on top
// by callers via Register, once a service's proto stubs and driver adapter
// exist — this package is the transport only.
package grpc

import (
	"context"
	"errors"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Server is a gRPC transport endpoint: a *grpc.Server with the health and
// reflection services registered, ready to Serve on a net.Listener.
type Server struct {
	srv    *grpc.Server
	health *health.Server
}

// New builds a gRPC server with the health and reflection services registered
// and the overall serving status set to SERVING. It registers no application
// services; callers add those with Register.
func New(opts ...grpc.ServerOption) *Server {
	srv := grpc.NewServer(opts...)

	h := health.NewServer()
	// The empty service name is the server-wide health of the whole endpoint,
	// which a bare `grpc_health_v1.Health/Check` (no service field) queries.
	h.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, h)

	reflection.Register(srv)

	return &Server{srv: srv, health: h}
}

// Register exposes the underlying grpc.ServiceRegistrar so callers can attach
// application service servers on top of this transport. It is the single seam
// PR-2 (Bigtable Admin over gRPC) wires its generated server stubs through.
func (s *Server) Register(desc *grpc.ServiceDesc, impl any) {
	s.srv.RegisterService(desc, impl)
}

// SetServingStatus updates the health status reported for a service name (empty
// name = the whole server), letting a caller mark a service NOT_SERVING while it
// warms up. The transport itself starts SERVING.
func (s *Server) SetServingStatus(service string, status healthpb.HealthCheckResponse_ServingStatus) {
	s.health.SetServingStatus(service, status)
}

// Serve runs the gRPC server on ln until Shutdown (or Stop). A clean stop is
// reported as nil — mirroring how the HTTP path treats http.ErrServerClosed — so
// serverkit's serve loop never surfaces an ordinary shutdown as a fatal error.
func (s *Server) Serve(ln net.Listener) error {
	if err := s.srv.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}

	return nil
}

// Shutdown gracefully stops the server, honoring ctx: it waits for in-flight
// RPCs to drain, but hard-stops (grpc.Server.Stop) if ctx is canceled or its
// deadline passes first, so a slow client cannot outlast the shutdown grace
// period. It returns ctx.Err() only when the hard-stop path was taken.
func (s *Server) Shutdown(ctx context.Context) error {
	done := make(chan struct{})

	go func() {
		s.srv.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.srv.Stop()
		<-done // GracefulStop returns once Stop has unblocked it

		return ctx.Err()
	}
}
