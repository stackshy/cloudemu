// Command compatgen runs CloudEmu's SDK-compatibility suite, joins the results
// against the generated coverage model (docs/coverage/coverage.json), and
// renders the compatibility matrix into docs/compat/.
//
// It is deliberately not a `go generate` target: unlike coverage (a pure AST
// parse), the compat matrix is evidence-based — it must actually run real SDK
// calls. Invoke it directly:
//
//	go run ./internal/compatgen
//
// CI runs it and then `git diff --exit-code docs/compat/` as a drift gate.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/compat"
)

const (
	moduleMarker = "go.mod"
	modulePath   = "github.com/stackshy/cloudemu/v2"
	coverageRel  = "docs/coverage/coverage.json"
	outputRel    = "docs/compat"
	compatFile   = "compat.json"
	readmeFile   = "README.md"
	testTarget   = "./compat/..."

	statusGreen = "green"
	statusAmber = "amber"

	sdkPass = "pass"

	dirPerm  = 0o750
	filePerm = 0o600
)

// errModuleRoot is returned when the module root cannot be located.
var errModuleRoot = errors.New("module root not found from working directory")

// providerOrder fixes the column order in the rendered matrix.
var providerOrder = []string{"aws", "azure", "gcp"} //nolint:gochecknoglobals // fixed display order for the matrix

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "compatgen:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	results, err := runSuite(root)
	if err != nil {
		return err
	}

	services, err := loadCoverage(filepath.Join(root, coverageRel))
	if err != nil {
		return err
	}

	entries := build(services, results)

	return writeOutputs(filepath.Join(root, outputRel), entries)
}

// coverage schema (subset of docs/coverage/coverage.json).
type covOperation struct {
	Name string `json:"name"`
}

type covService struct {
	Name       string            `json:"service"`
	Interface  string            `json:"interface"`
	Operations []covOperation    `json:"operations"`
	Providers  map[string]string `json:"providers"`
}

// compat.json schema.
type cell struct {
	Native string `json:"native"`
	SDKGo  string `json:"sdkGo,omitempty"` // "pass" when a real Go SDK call succeeded
}

type entry struct {
	Service   string          `json:"service"`
	Operation string          `json:"operation"`
	Providers map[string]cell `json:"providers"`
	Status    string          `json:"status"`
}

// runSuite runs the compat tests with a results directory set, then reads and
// merges every per-process result file back in.
func runSuite(root string) ([]compat.Result, error) {
	dir, err := os.MkdirTemp("", "cloudemu-compat-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	cmd := exec.CommandContext(context.Background(), "go", "test", testTarget)
	cmd.Dir = root
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), compat.EnvOut+"="+dir)

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("compat suite failed (fix red cells before regenerating): %w", err)
	}

	return mergeResults(dir)
}

func mergeResults(dir string) ([]compat.Result, error) {
	files, err := filepath.Glob(filepath.Join(dir, "compat-*.json"))
	if err != nil {
		return nil, err
	}

	var all []compat.Result

	for _, f := range files {
		data, readErr := os.ReadFile(f)
		if readErr != nil {
			return nil, readErr
		}

		var part []compat.Result
		if err := json.Unmarshal(data, &part); err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}

		all = append(all, part...)
	}

	return all, nil
}

func loadCoverage(path string) ([]covService, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var services []covService
	if err := json.Unmarshal(data, &services); err != nil {
		return nil, err
	}

	return services, nil
}

// build joins coverage against results. It emits rows only for services that a
// compat test actually exercised, so the matrix grows as the suite grows.
func build(services []covService, results []compat.Result) []entry {
	passed := passIndex(results) // provider|service|operation -> true
	touched := touchedServices(results)

	var entries []entry

	for _, svc := range services {
		if !touched[svc.Name] {
			continue
		}

		for _, op := range svc.Operations {
			e := entry{Service: svc.Name, Operation: op.Name, Providers: map[string]cell{}, Status: statusAmber}

			for _, prov := range providerOrder {
				native, ok := svc.Providers[prov]
				if !ok || native == "" {
					continue
				}

				c := cell{Native: native}
				if passed[key(prov, svc.Name, op.Name)] {
					c.SDKGo = sdkPass
					e.Status = statusGreen
				}

				e.Providers[prov] = c
			}

			entries = append(entries, e)
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Service != entries[j].Service {
			return entries[i].Service < entries[j].Service
		}

		return entries[i].Operation < entries[j].Operation
	})

	return entries
}

func passIndex(results []compat.Result) map[string]bool {
	idx := map[string]bool{}

	for _, r := range results {
		if r.Passed {
			idx[key(r.Provider, r.Service, r.Operation)] = true
		}
	}

	return idx
}

func touchedServices(results []compat.Result) map[string]bool {
	set := map[string]bool{}
	for _, r := range results {
		set[r.Service] = true
	}

	return set
}

func key(provider, service, operation string) string {
	return provider + "|" + service + "|" + operation
}

// repoRoot walks up from the working directory to the module root (the
// directory whose go.mod declares modulePath).
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		data, readErr := os.ReadFile(filepath.Join(dir, moduleMarker))
		if readErr == nil && strings.Contains(string(data), modulePath) {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%w: %s", errModuleRoot, modulePath)
		}

		dir = parent
	}
}

func writeOutputs(dir string, entries []entry) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, compatFile), append(data, '\n'), filePerm); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, readmeFile), []byte(renderReadme(entries)), filePerm)
}
