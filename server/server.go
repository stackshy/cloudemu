// Package server provides a pluggable SDK-compatible HTTP server.
//
// The core Server is protocol-agnostic: it dispatches incoming requests to
// registered Handlers via a Matches predicate. Each Handler is a self-contained
// package (e.g. server/aws/s3, server/aws/dynamodb) that speaks its own wire
// format. Adding a new service — AWS EC2, Azure Blob, GCP GCS — is one new
// package and one Register call; the core server never changes.
package server

import "net/http"

// Handler is a self-contained protocol handler registered with a Server.
// Matches inspects the request and returns true if this handler should serve
// it; ServeHTTP writes the response. Handlers are evaluated in registration
// order, first match wins.
type Handler interface {
	Matches(r *http.Request) bool
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}

// Server routes incoming HTTP requests to registered Handlers. Server itself
// implements http.Handler, so httptest.NewServer(srv) works.
type Server struct {
	handlers []Handler
	// observer, when set, is called after a handler serves a request. It is a
	// generic post-dispatch hook (protocol-agnostic) used, for example, to record
	// API activity for CloudTrail. Nil by default, so it adds no behavior.
	observer func(*http.Request)
	// preDispatch, when set, runs before handler matching. It may authenticate
	// the request, replace it (e.g. attaching a resolved principal to the
	// context), and report whether dispatch should proceed. When it returns
	// proceed=false it has already written the response. Nil by default, so it
	// adds no behavior — the default request path is byte-for-byte unchanged.
	preDispatch func(http.ResponseWriter, *http.Request) (*http.Request, bool)
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

// SetObserver installs a post-dispatch hook called with each request a handler
// served. It is generic and optional; passing nil disables it.
func (s *Server) SetObserver(fn func(*http.Request)) {
	s.observer = fn
}

// SetPreDispatch installs a hook that runs before handler matching. It may
// authenticate the request and return a replacement request (e.g. with a
// principal on its context); returning proceed=false stops dispatch after the
// hook has written the response. It is generic and optional; passing nil
// disables it, restoring the default request path exactly.
func (s *Server) SetPreDispatch(fn func(http.ResponseWriter, *http.Request) (*http.Request, bool)) {
	s.preDispatch = fn
}

// ServeHTTP dispatches to the first handler whose Matches returns true, or
// responds 501 Not Implemented if no handler matches.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.preDispatch != nil {
		var proceed bool
		if r, proceed = s.preDispatch(w, r); !proceed {
			return
		}
	}

	for _, h := range s.handlers {
		if h.Matches(r) {
			h.ServeHTTP(w, r)

			if s.observer != nil {
				s.observer(r)
			}

			return
		}
	}

	http.Error(w, "no handler registered for this request", http.StatusNotImplemented)
}
