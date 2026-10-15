package server

import (
	"net/http"
	"testing"
)

type pathHandler string

func (p pathHandler) Matches(r *http.Request) bool { return r.URL.Path == string(p) }

func (pathHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

func TestHandlersReturnsACopyInOrder(t *testing.T) {
	s := New(pathHandler("/a"))
	s.Register(pathHandler("/b"))

	got := s.Handlers()
	if len(got) != 2 || got[0] != pathHandler("/a") || got[1] != pathHandler("/b") {
		t.Fatalf("Handlers() = %v, want [/a /b]", got)
	}

	got[0] = pathHandler("/x")

	if s.Handlers()[0] != pathHandler("/a") {
		t.Fatal("changing the returned slice changed the server")
	}
}
