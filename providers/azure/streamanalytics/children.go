package streamanalytics

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

const (
	// KindTransformations / KindInputs / KindOutputs / KindFunctions are the ARM
	// child-collection type segments nested under a streaming job. A single
	// generic child store, keyed by kind, backs all four so their near-identical
	// CRUD lives in one place.
	KindTransformations = "transformations"
	KindInputs          = "inputs"
	KindOutputs         = "outputs"
	KindFunctions       = "functions"

	// TestSucceeded is the ResourceTestStatus a datasource test returns for a
	// reachable input/output/function in the emulator, which never fails a probe.
	TestSucceeded = "TestSucceeded"

	// streamingUnitsField is the transformation property the scale action tunes.
	streamingUnitsField = "streamingUnits"
)

// Child is a stored transformation / input / output / function under a streaming
// job. Its Properties block (datasource, serialization, query, binding, …) is
// held as raw JSON and round-trips verbatim, so the caller reads back exactly
// the type discriminator and properties it sent. Etag is minted once at create
// and stays stable, and is injected into the Properties block on the wire.
type Child struct {
	Subscription  string          `json:"subscription"`
	ResourceGroup string          `json:"resourceGroup"`
	JobName       string          `json:"jobName"`
	Kind          string          `json:"kind"`
	Name          string          `json:"name"`
	Properties    json.RawMessage `json:"properties,omitempty"`
	Etag          string          `json:"etag"`
}

// ARMID returns the fully-qualified ARM resource id of the child, nested under
// its parent job (e.g. .../streamingjobs/{job}/inputs/{name}).
func (c *Child) ARMID() string {
	return jobResourceID(c.Subscription, c.ResourceGroup, c.JobName) + "/" + c.Kind + "/" + c.Name
}

// ARMType returns the child's ARM resource type (e.g.
// Microsoft.StreamAnalytics/streamingjobs/inputs).
func (c *Child) ARMType() string {
	return providerNamespace + "/" + jobType + "/" + c.Kind
}

// jobResourceID returns a job's ARM resource id with the caller's casing
// preserved (unlike jobKey, which lower-cases for storage).
func jobResourceID(sub, rg, job string) string {
	return "/subscriptions/" + sub + "/resourceGroups/" + rg +
		"/providers/" + providerNamespace + "/" + jobType + "/" + job
}

// childKey is the case-insensitive store key for a child under a job.
func childKey(sub, rg, job, kind, name string) string {
	return jobKey(sub, rg, job) + "/" + kind + "/" + strings.ToLower(name)
}

// CreateOrUpdateChild creates or updates a transformation/input/output/function
// under its parent job. The parent job must exist — otherwise it returns a
// NotFound error (the wire layer maps it to ParentResourceNotFound). The etag is
// minted once at create and preserved across updates. It returns the stored
// child and whether it was newly created.
func (m *Mock) CreateOrUpdateChild(
	_ context.Context, sub, rg, job, kind, name string, properties json.RawMessage,
) (Child, bool, error) {
	if err := validateChild(sub, rg, job, kind, name); err != nil {
		return Child{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.jobs.Has(jobKey(sub, rg, job)) {
		return Child{}, false, cerrors.Newf(cerrors.NotFound, "streaming job %q not found", job)
	}

	k := childKey(sub, rg, job, kind, name)

	existing, existed := m.children.Get(k)
	created := !existed

	c := Child{Subscription: sub, ResourceGroup: rg, JobName: job, Kind: kind, Name: name}
	if existed {
		c.Etag = existing.Etag
	} else {
		c.Etag = idgen.SyntheticGUID("streamanalytics/child-etag/" + k)
	}

	if properties != nil {
		c.Properties = append(json.RawMessage(nil), properties...)
	} else if existed {
		c.Properties = existing.Properties
	}

	m.children.Set(k, &c)

	return cloneChild(&c), created, nil
}

// GetChild returns the named child, or a NotFound error.
func (m *Mock) GetChild(_ context.Context, sub, rg, job, kind, name string) (Child, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.children.Get(childKey(sub, rg, job, kind, name))
	if !ok {
		return Child{}, cerrors.Newf(cerrors.NotFound, "%s %q not found", childNoun(kind), name)
	}

	return cloneChild(c), nil
}

// DeleteChild removes the named child, reporting whether it existed.
func (m *Mock) DeleteChild(_ context.Context, sub, rg, job, kind, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.children.Delete(childKey(sub, rg, job, kind, name)), nil
}

// ListChildren returns every child of the given kind under a job, sorted by
// name.
func (m *Mock) ListChildren(_ context.Context, sub, rg, job, kind string) ([]Child, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.collectChildren(sub, rg, job, kind), nil
}

// JobChildren returns every child of a job across all kinds, sorted by kind then
// name, so a caller can embed the transformation/inputs/outputs/functions in a
// job response under a single lock.
func (m *Mock) JobChildren(_ context.Context, sub, rg, job string) ([]Child, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := jobKey(sub, rg, job) + "/"

	var out []Child

	for k, c := range m.children.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, cloneChild(c))
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}

		return out[i].Name < out[j].Name
	})

	return out, nil
}

// TestChild reports the datasource test status for a child. The emulator has no
// live datasource to probe, so a stored child always tests successfully; a
// missing child is a NotFound.
func (m *Mock) TestChild(_ context.Context, sub, rg, job, kind, name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.children.Has(childKey(sub, rg, job, kind, name)) {
		return "", cerrors.Newf(cerrors.NotFound, "%s %q not found", childNoun(kind), name)
	}

	return TestSucceeded, nil
}

// collectChildren returns the children of a kind under a job, sorted by name.
// The caller holds the lock.
func (m *Mock) collectChildren(sub, rg, job, kind string) []Child {
	prefix := childKey(sub, rg, job, kind, "")

	var out []Child

	for k, c := range m.children.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, cloneChild(c))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// scaleTransformation tunes the streamingUnits of the job's first transformation
// to units, rewriting only that field of the raw properties block. It is a
// no-op when the job has no transformation. The caller holds the write lock.
func (m *Mock) scaleTransformation(sub, rg, job string, units int) {
	children := m.collectChildren(sub, rg, job, KindTransformations)
	if len(children) == 0 {
		return
	}

	name := children[0].Name
	k := childKey(sub, rg, job, KindTransformations, name)

	stored, ok := m.children.Get(k)
	if !ok {
		return
	}

	props := map[string]json.RawMessage{}
	if len(stored.Properties) > 0 {
		if err := json.Unmarshal(stored.Properties, &props); err != nil {
			return
		}
	}

	props[streamingUnitsField] = json.RawMessage(strconv.Itoa(units))

	raw, err := json.Marshal(props)
	if err != nil {
		return
	}

	updated := *stored
	updated.Properties = raw
	m.children.Set(k, &updated)
}

// childNoun maps a child kind to a singular noun for error messages.
func childNoun(kind string) string {
	switch kind {
	case KindInputs:
		return "input"
	case KindOutputs:
		return "output"
	case KindFunctions:
		return "function"
	case KindTransformations:
		return "transformation"
	default:
		return "child resource"
	}
}

// validateChild rejects a child create/update with missing required fields or an
// unknown kind.
func validateChild(sub, rg, job, kind, name string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case job == "":
		return cerrors.New(cerrors.InvalidArgument, "job name is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "child name is required")
	case !isKnownKind(kind):
		return cerrors.Newf(cerrors.InvalidArgument, "unknown child kind %q", kind)
	default:
		return nil
	}
}

// isKnownKind reports whether kind is one of the four child collections.
func isKnownKind(kind string) bool {
	switch kind {
	case KindTransformations, KindInputs, KindOutputs, KindFunctions:
		return true
	default:
		return false
	}
}

// cloneChild deep-copies a stored child so callers never alias the backing
// store.
func cloneChild(c *Child) Child {
	out := *c
	if c.Properties != nil {
		out.Properties = append(json.RawMessage(nil), c.Properties...)
	}

	return out
}
