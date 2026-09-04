// Package timetravel adds named in-memory state snapshots to a running
// emulator, layered on the existing whole-emulator snapshot primitive.
//
// It holds a registry of name -> serialized state, where each serialized state
// is the marshaled persist.Snapshot produced by persist.ExportAll. On top of
// that it offers three operations beyond a plain save/restore:
//
//   - Rewind: restore live emulator state to a named snapshot (undo).
//   - Fork:   copy a stored snapshot under a new name, so a "what-if" branch
//     can be explored (rewind to it, mutate, re-save it) without disturbing the
//     branch it was forked from or any other branch.
//   - Save/List/Delete: manage the named snapshots by label.
//
// The registry is provider-agnostic: it drives the same capture/restore surface
// persist already uses, wired in as two funcs, and never touches individual
// provider mocks. Stored snapshots are independent byte copies, so forks are
// isolated by construction — mutating one branch cannot reach another. It is
// deterministic and fully in-memory; snapshot timestamps come from a
// config.Clock so tests observe fixed times.
package timetravel

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/persist"
)

// maxNameLen bounds a snapshot label.
const maxNameLen = 64

// nameRE matches an acceptable snapshot label: 1-64 chars of [A-Za-z0-9._-].
// The bare "." and ".." are rejected separately so a label can never be a
// path-traversal token if a caller ever persists it to disk.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,` + strconv.Itoa(maxNameLen) + `}$`)

// CaptureFunc returns the whole-emulator live state as a marshaled
// persist.Snapshot. Registry stores exactly these bytes under a label.
type CaptureFunc func() ([]byte, error)

// RestoreFunc replaces the whole-emulator live state from a marshaled
// persist.Snapshot. Registry passes back the bytes it stored.
type RestoreFunc func(state []byte) error

// entry is one stored named snapshot. data is an independent copy of the
// captured bytes, so forks never alias each other.
type entry struct {
	data       []byte
	createdAt  time.Time
	forkedFrom string
	providers  []string
}

// Info describes a stored snapshot without exposing its bytes.
type Info struct {
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"createdAt"`
	ForkedFrom string    `json:"forkedFrom,omitempty"`
	Providers  []string  `json:"providers,omitempty"`
	Size       int       `json:"size"`
}

// Registry is a thread-safe store of named emulator snapshots supporting
// save/list/delete plus rewind and fork.
type Registry struct {
	clock   config.Clock
	capture CaptureFunc
	restore RestoreFunc

	mu      sync.RWMutex
	entries map[string]*entry
}

// New builds a registry that captures and restores live state through the given
// funcs. A nil clock defaults to the real system clock.
func New(clock config.Clock, capture CaptureFunc, restore RestoreFunc) *Registry {
	if clock == nil {
		clock = config.RealClock{}
	}

	return &Registry{
		clock:   clock,
		capture: capture,
		restore: restore,
		entries: map[string]*entry{},
	}
}

// Save captures current live state and stores it under name, overwriting any
// existing snapshot with that name (so a branch can be re-saved after a rewind
// and mutation).
func (r *Registry) Save(name string) error {
	if !validName(name) {
		return errInvalidName(name)
	}

	data, err := r.capture()
	if err != nil {
		return cerrors.Newf(cerrors.Internal, "capture state: %v", err)
	}

	providers, err := providersOf(data)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries[name] = &entry{
		data:       cloneBytes(data),
		createdAt:  r.clock.Now(),
		providers:  providers,
		forkedFrom: "",
	}

	return nil
}

// Rewind restores live state to the snapshot stored under name.
func (r *Registry) Rewind(name string) error {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()

	if !ok {
		return errNotFound(name)
	}

	if err := r.restore(cloneBytes(e.data)); err != nil {
		return cerrors.Newf(cerrors.Internal, "restore state: %v", err)
	}

	return nil
}

// Fork copies the snapshot stored under from into a new, independent snapshot
// named to. The copy is a distinct byte slice, so rewinding to and re-saving
// either branch never affects the other. It fails if to already exists, so an
// existing branch is never silently clobbered.
func (r *Registry) Fork(from, to string) error {
	if !validName(to) {
		return errInvalidName(to)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	src, ok := r.entries[from]
	if !ok {
		return errNotFound(from)
	}

	if _, exists := r.entries[to]; exists {
		return cerrors.Newf(cerrors.AlreadyExists, "snapshot %q already exists", to)
	}

	r.entries[to] = &entry{
		data:       cloneBytes(src.data),
		createdAt:  r.clock.Now(),
		providers:  append([]string(nil), src.providers...),
		forkedFrom: from,
	}

	return nil
}

// Delete removes the snapshot stored under name.
func (r *Registry) Delete(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.entries[name]; !ok {
		return errNotFound(name)
	}

	delete(r.entries, name)

	return nil
}

// List returns the stored snapshots ordered by name.
func (r *Registry) List() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Info, 0, len(r.entries))
	for name, e := range r.entries {
		out = append(out, Info{
			Name:       name,
			CreatedAt:  e.createdAt,
			ForkedFrom: e.forkedFrom,
			Providers:  append([]string(nil), e.providers...),
			Size:       len(e.data),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// providersOf parses a captured snapshot to list the providers it holds, which
// doubles as a validation that the bytes are a well-formed snapshot of the
// version this build understands.
func providersOf(data []byte) ([]string, error) {
	var snap persist.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "parse captured snapshot: %v", err)
	}

	names := make([]string, 0, len(snap.Providers))
	for p := range snap.Providers {
		names = append(names, p)
	}

	sort.Strings(names)

	return names, nil
}

func validName(name string) bool {
	return name != "." && name != ".." && nameRE.MatchString(name)
}

func cloneBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)

	return c
}

func errNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "snapshot %q not found", name)
}

func errInvalidName(name string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"invalid snapshot name %q: 1-%d chars of [A-Za-z0-9._-], excluding . and ..", name, maxNameLen)
}
