package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/topology"
	"github.com/stackshy/cloudemu/v2/providers/aws/ec2"
	"github.com/stackshy/cloudemu/v2/providers/aws/route53"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
)

func emptyNetEngine() *topology.Engine {
	o := config.NewOptions()

	return topology.New(ec2.New(o), vpc.New(o), route53.New(o))
}

func TestNetPort(t *testing.T) {
	if p, err := netPort(""); err != nil || p != 0 {
		t.Fatalf("netPort(empty) = %d, %v", p, err)
	}
	if p, err := netPort("5432"); err != nil || p != 5432 {
		t.Fatalf("netPort(5432) = %d, %v", p, err)
	}
	if _, err := netPort("nope"); err == nil {
		t.Fatal("netPort(nope) = nil error, want error")
	}
}

func TestServeCanConnectValidation(t *testing.T) {
	eng := emptyNetEngine()

	// Missing from/to → 400.
	rec := httptest.NewRecorder()
	serveCanConnect(rec, httptest.NewRequest(http.MethodGet, "/_cloudemu/net/can-connect", nil), eng)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing params = %d, want 400", rec.Code)
	}

	// Invalid port → 400.
	rec = httptest.NewRecorder()
	serveCanConnect(rec, httptest.NewRequest(http.MethodGet, "/_cloudemu/net/can-connect?from=i-a&to=i-b&port=x", nil), eng)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad port = %d, want 400", rec.Code)
	}

	// Unknown instance → engine NotFound → 400 with an error body.
	rec = httptest.NewRecorder()
	serveCanConnect(rec, httptest.NewRequest(http.MethodGet, "/_cloudemu/net/can-connect?from=i-a&to=i-b&port=80", nil), eng)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "error") {
		t.Fatalf("unknown instance = %d %q, want 400 error", rec.Code, rec.Body.String())
	}
}

func TestServeTraceValidation(t *testing.T) {
	eng := emptyNetEngine()

	// Missing destination IP → 400.
	rec := httptest.NewRecorder()
	serveTrace(rec, httptest.NewRequest(http.MethodGet, "/_cloudemu/net/trace?from=i-a", nil), eng)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing dest = %d, want 400", rec.Code)
	}

	// Unknown instance → engine error → 400.
	rec = httptest.NewRecorder()
	serveTrace(rec, httptest.NewRequest(http.MethodGet, "/_cloudemu/net/trace?from=i-a&to=10.0.0.5", nil), eng)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown instance trace = %d, want 400", rec.Code)
	}
}
