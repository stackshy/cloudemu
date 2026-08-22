// Package compat is the shared harness for CloudEmu's SDK-compatibility suite.
//
// It boots the wire server in-process, hands back real cloud-SDK clients
// pointed at it, and records one pass/fail result per (provider, service,
// operation) a real client exercises. The matrix generator
// (internal/compatgen) joins those results against docs/coverage/coverage.json
// to render the published compatibility matrix.
//
// Results are persisted only when EnvOut names a directory — that is, when the
// generator drives the run. A plain `go test ./compat/...` leaves EnvOut unset,
// so the suite simply asserts (and still fails on any incompatibility).
package compat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvOut names the directory each test process writes its result file to.
// Unset means "assert only, persist nothing".
const EnvOut = "CLOUDEMU_COMPAT_OUT"

// ClientGoSDK marks results produced through a real Go cloud SDK.
const ClientGoSDK = "sdkGo"

const (
	dirPerm  = 0o750
	filePerm = 0o600
)

// Result is one compatibility observation: did a real client's call for
// (Provider, Service, Operation) succeed against the emulator?
type Result struct {
	Provider  string `json:"provider"`
	Service   string `json:"service"`
	Operation string `json:"operation"`
	Client    string `json:"client"`
	Passed    bool   `json:"passed"`
}

// TB is the subset of *testing.T the harness needs. Depending on an interface
// rather than the concrete type keeps this package free of a direct testing
// dependency and makes the harness itself unit-testable.
type TB interface {
	Helper()
	Cleanup(func())
	Name() string
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// Session is a booted emulator for one provider. It accumulates results and
// flushes them on test cleanup. Construct it via a provider boot helper
// (BootAWS, ...).
type Session struct {
	tb       TB
	provider string
	endpoint string
	results  []Result
}

// Op runs one real SDK call and records whether it succeeded for the given
// portable (service, operation) — the same identity used in coverage.json.
// A failure records a red cell and fails the test, so the suite doubles as a
// regression net.
func (s *Session) Op(service, operation string, fn func() error) {
	s.tb.Helper()

	err := fn()
	s.results = append(s.results, Result{
		Provider:  s.provider,
		Service:   service,
		Operation: operation,
		Client:    ClientGoSDK,
		Passed:    err == nil,
	})

	if err != nil {
		s.tb.Errorf("compat %s/%s %s.%s: %v", s.provider, ClientGoSDK, service, operation, err)
	}
}

// Endpoint is the base URL of the booted wire server.
func (s *Session) Endpoint() string { return s.endpoint }

func (s *Session) flush() {
	dir := os.Getenv(EnvOut)
	if dir == "" || len(s.results) == 0 {
		return
	}

	//nolint:gosec // dir is an operator-provided results directory (EnvOut), not user input
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		s.tb.Errorf("compat: mkdir results dir: %v", err)
		return
	}

	data, err := json.MarshalIndent(s.results, "", "  ")
	if err != nil {
		s.tb.Errorf("compat: marshal results: %v", err)
		return
	}

	name := fmt.Sprintf("compat-%d-%s.json", os.Getpid(), sanitize(s.tb.Name()))

	//nolint:gosec // path composed from EnvOut + sanitized test name, not user input
	if err := os.WriteFile(filepath.Join(dir, name), data, filePerm); err != nil {
		s.tb.Errorf("compat: write results: %v", err)
	}
}

func sanitize(name string) string {
	return strings.NewReplacer("/", "_", " ", "_", "\\", "_").Replace(name)
}
