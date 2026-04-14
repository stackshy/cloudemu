// Package server provides an SDK-compatible HTTP server that translates
// real AWS SDK requests into CloudEmu driver calls.
package server

import (
	"context"
	"net"
	"net/http"

	dbdriver "github.com/stackshy/cloudemu/database/driver"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
)

// Drivers holds the driver interfaces the server dispatches to.
// Only non-nil drivers are served; requests for unsupported services
// return 501 Not Implemented.
type Drivers struct {
	Storage  storagedriver.Bucket
	Database dbdriver.Database
}

// Server exposes CloudEmu's in-memory backends over HTTP so that
// the real AWS SDK can be pointed at it via endpoint override.
type Server struct {
	drivers  Drivers
	mux      *http.ServeMux
	listener net.Listener
}

// New creates a Server backed by the given drivers.
func New(d Drivers) *Server {
	s := &Server{
		drivers: d,
		mux:     http.NewServeMux(),
	}
	s.mux.HandleFunc("/", s.route)

	return s
}

// Handler returns the http.Handler for use with httptest.NewServer.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Start listens on the given address and serves requests.
// It blocks until the server is closed.
func (s *Server) Start(addr string) error {
	var lc net.ListenConfig

	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}

	s.listener = ln

	srv := &http.Server{Handler: s.mux} //nolint:gosec // local dev server, timeouts not needed

	return srv.Serve(ln)
}

// Close shuts down the server.
func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}

	return nil
}

// route dispatches requests to the appropriate service handler.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	// DynamoDB uses X-Amz-Target header for dispatch.
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		if s.drivers.Database == nil {
			http.Error(w, "DynamoDB not configured", http.StatusNotImplemented)
			return
		}

		s.handleDynamoDB(w, r, target)

		return
	}

	// Default: S3 (REST path-style).
	if s.drivers.Storage == nil {
		http.Error(w, "S3 not configured", http.StatusNotImplemented)
		return
	}

	s.handleS3(w, r)
}
