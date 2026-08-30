// Command coveragegen walks the driver interfaces under services/*/driver and
// the provider factories under providers/* and emits the capability coverage
// docs (docs/coverage/) plus a machine-readable coverage.json.
//
// It is the single source of truth for "what cloudemu can do": every operation
// listed is a method on a driver interface the code actually declares, so the
// docs can never promise a capability the emulator does not implement.
//
// Run it with `go generate ./...` from the repo root.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const modulePath = "github.com/stackshy/cloudemu/v2"

// errNoModule is returned when the module root cannot be located.
var errNoModule = errors.New("could not locate go.mod for module")

// providerOrder fixes the column order in the generated tables.
var providerOrder = []string{"aws", "azure", "gcp", "oci"} //nolint:gochecknoglobals // generator config

// Operation is a single supported call on a service.
type Operation struct {
	Name string `json:"name"`
	Doc  string `json:"doc,omitempty"`
}

// Capability is an optional interface a provider may implement (discovered by
// type assertion), separate from the mandatory primary interface.
type Capability struct {
	Name       string      `json:"name"`
	Doc        string      `json:"doc,omitempty"`
	Operations []Operation `json:"operations"`
}

// Service is one abstract service and everything the drivers say about it.
type Service struct {
	Name         string            `json:"service"`
	Interface    string            `json:"interface"`
	Operations   []Operation       `json:"operations"`
	Capabilities []Capability      `json:"optionalCapabilities,omitempty"`
	Providers    map[string]string `json:"providers"` // provider -> native name

	// providerMethods maps a provider to the method-set of the concrete mock
	// backing this service for that provider. It gates which optional
	// capabilities each provider page lists (a provider implements a capability
	// only when its mock has every method of that capability's interface).
	// Unexported, so it is never serialized: the public coverage schema — and
	// coverage.json, which the compat matrix reads — is unchanged.
	providerMethods map[string]map[string]bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "coveragegen:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	services, err := collectServices(filepath.Join(root, "services"))
	if err != nil {
		return err
	}

	if err := attachProviders(root, services); err != nil {
		return err
	}

	if err := synthesizeNativeServices(root, services); err != nil {
		return err
	}

	ordered := sortedServices(services)
	if err := render(root, ordered); err != nil {
		return err
	}

	fmt.Printf("coveragegen: wrote coverage for %d services\n", len(ordered))

	warnings, checkErr := checkRegistrations(root, ordered)
	if checkErr != nil {
		return checkErr
	}

	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, w)
	}

	return nil
}

func sortedServices(services map[string]*Service) []*Service {
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}

	sort.Strings(names)

	ordered := make([]*Service, 0, len(names))
	for _, n := range names {
		ordered = append(ordered, services[n])
	}

	return ordered
}

// repoRoot finds the module root by walking up to the go.mod that declares modulePath.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		data, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if readErr == nil && strings.Contains(string(data), modulePath) {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%w %s", errNoModule, modulePath)
		}

		dir = parent
	}
}
