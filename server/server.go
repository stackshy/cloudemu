// Package server provides a pluggable SDK-compatible HTTP server.
//
// The core Server is protocol-agnostic: it dispatches incoming requests to
// registered Handlers via a Matches predicate. Each Handler is a self-contained
// package (e.g. server/aws/s3, server/aws/dynamodb) that speaks its own wire
// format. Adding a new service — AWS EC2, Azure Blob, GCP GCS — is one new
// package and one Register call; the core server never changes.
package server

import (
	"context"
	"net"
	"net/http"
)

// Handler is a self-contained protocol handler registered with a Server.
// Matches inspects the request and returns true if this handler should serve
// it; ServeHTTP writes the response. Handlers are evaluated in registration
// order, first match wins.
type Handler interface {
	Matches(r *http.Request) bool
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}

// Server routes incoming HTTP requests to registered Handlers.
// Server itself implements http.Handler, so httptest.NewServer(srv) works.
type Server struct {
	handlers []Handler
	listener net.Listener
}

// New creates a Server preloaded with the given handlers. Additional handlers
// can be added later via Register.
func New(handlers ...Handler) *Server {
	return &Server{handlers: handlers}
}

// Register appends a handler. Handlers registered earlier take precedence, so
// register more specific handlers before catch-all ones.
func (s *Server) Register(h Handler) {
	s.handlers = append(s.handlers, h)
}

// ServeHTTP dispatches to the first handler whose Matches returns true, or
// responds 501 Not Implemented if no handler matches.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, h := range s.handlers {
		if h.Matches(r) {
			h.ServeHTTP(w, r)
			return
		}
	}

	http.Error(w, "no handler registered for this request", http.StatusNotImplemented)
}

// Start listens on addr and serves requests. Blocks until Close is called.
func (s *Server) Start(addr string) error {
	var lc net.ListenConfig

	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}

	s.listener = ln

	srv := &http.Server{Handler: s} //nolint:gosec // local dev server, timeouts not needed

	return srv.Serve(ln)
}

// Close shuts down the listener started by Start.
func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}

	return nil
}
